// Package transfer exports a book with everything its analysis cost
// (characters, voices, speakers, chapter edits, pronunciations, settings)
// into a zip file and restores such a file as a new book, e.g. on another
// instance or after switching databases. Audio is not included; it is
// rendered again.
//
// The zip holds book.json (a Header), one JSON Lines file per table
// (store.ExportTables, rows in the database's own field names), the
// original EPUB and the cover.
package transfer

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// Format identifies an export; Version is bumped on incompatible changes.
const (
	Format  = "audiobook-generator-book"
	Version = 1
)

// JobType restores an uploaded export into its book.
const JobType = "restore_book"

// Header is book.json.
type Header struct {
	Format     string     `json:"format"`
	Version    int        `json:"version"`
	Schema     string     `json:"schema"` // database migration at export time
	ExportedAt time.Time  `json:"exportedAt"`
	Book       store.Book `json:"book"`
}

const (
	headerFile = "book.json"
	epubFile   = "source.epub"
	coverFile  = "cover"
)

// RestorePath is where an uploaded export waits for its restore job.
func RestorePath(bookID int64) string {
	return filepath.Join(importer.BookDir(bookID), "restore.zip")
}

// Export writes a book's export to w.
func Export(ctx context.Context, st *store.Store, dataDir string, bookID int64, w io.Writer) error {
	b, err := st.GetBook(ctx, bookID)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	hw, err := zw.Create(headerFile)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(hw).Encode(Header{
		Format: Format, Version: Version, Schema: store.LatestSchemaVersion(), ExportedAt: time.Now().UTC(), Book: *b,
	}); err != nil {
		return err
	}
	var (
		cur   string
		enc   *json.Encoder
		files = map[string]bool{}
	)
	err = st.ExportBook(ctx, bookID, func(table string, row any) error {
		if table != cur {
			fw, err := zw.Create(table + ".jsonl")
			if err != nil {
				return err
			}
			cur, enc, files[table] = table, json.NewEncoder(fw), true
		}
		return enc.Encode(row)
	})
	if err != nil {
		return err
	}
	if b.EpubPath != "" {
		if err := addFile(zw, epubFile, filepath.Join(dataDir, b.EpubPath)); err != nil {
			return err
		}
	}
	if b.CoverPath != "" {
		if err := addFile(zw, coverFile+path.Ext(b.CoverPath), filepath.Join(dataDir, b.CoverPath)); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, name, src string) error {
	f, err := os.Open(src) //nolint:gosec // a path of the book, from the database
	if errors.Is(err, os.ErrNotExist) {
		return nil // nothing to include
	} else if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// The EPUB and the cover are compressed already.
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// ErrNotExport means a file is not an export of this service.
var ErrNotExport = errors.New("not a book export of this service")

// ReadHeader reads and checks the header of an export.
func ReadHeader(zipPath string) (*Header, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, ErrNotExport
	}
	defer func() { _ = zr.Close() }()
	return readHeader(&zr.Reader)
}

func readHeader(zr *zip.Reader) (*Header, error) {
	f := find(zr, headerFile)
	if f == nil {
		return nil, ErrNotExport
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	var h Header
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&h); err != nil || h.Format != Format {
		return nil, ErrNotExport
	}
	if h.Version > Version {
		return nil, fmt.Errorf("the export is from a newer version (format %d, this one reads up to %d)", h.Version, Version)
	}
	if strings.TrimSpace(h.Book.Title) == "" {
		return nil, fmt.Errorf("%w: no title", ErrNotExport)
	}
	return &h, nil
}

func find(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Restorer restores uploaded exports.
type Restorer struct {
	Store   *store.Store
	DataDir string
	Log     *slog.Logger
}

// Handler is the job handler for JobType.
func (r *Restorer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: "import",
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.BookID == nil {
				return jobs.Permanent(errors.New("restore job without book"))
			}
			return r.Restore(ctx, *j.BookID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.BookID == nil {
				return
			}
			if uerr := r.Store.UpdateBook(ctx, *j.BookID, map[string]any{
				"Status": store.BookFailed, "Error": store.CleanText(err.Error()),
			}); uerr != nil {
				r.Log.Error("mark book failed", "book", *j.BookID, "err", uerr)
			}
		},
	}
}

// jsonl reads rows of one table.
type jsonl struct {
	dec *json.Decoder
}

func (l *jsonl) Next(dst any) (bool, error) {
	if !l.dec.More() {
		return false, nil
	}
	if err := l.dec.Decode(dst); err != nil {
		return false, err
	}
	return true, nil
}

// Restore fills book bookID from its uploaded export. It is idempotent: a
// failed attempt can run again.
func (r *Restorer) Restore(ctx context.Context, bookID int64, progress func(float64)) error {
	src := filepath.Join(r.DataDir, RestorePath(bookID))
	zr, err := zip.OpenReader(src)
	if err != nil {
		return jobs.Permanent(fmt.Errorf("open export: %w", err))
	}
	defer func() { _ = zr.Close() }()
	h, err := readHeader(&zr.Reader)
	if err != nil {
		return jobs.Permanent(err)
	}

	var open io.ReadCloser
	closeOpen := func() {
		if open != nil {
			_ = open.Close()
			open = nil
		}
	}
	defer closeOpen()
	done := 0
	err = r.Store.RestoreBook(ctx, bookID, func(table string) (store.RowReader, error) {
		closeOpen()
		progress(0.9 * float64(done) / float64(len(store.ExportTables)))
		done++
		f := find(&zr.Reader, table+".jsonl")
		if f == nil {
			return nil, nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		open = rc
		return &jsonl{dec: json.NewDecoder(bufio.NewReaderSize(rc, 1<<16))}, nil
	})
	closeOpen()
	if err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return jobs.Permanent(err)
		}
		return err
	}

	progress(0.9)
	fields := restoredBook(&h.Book)
	if f := find(&zr.Reader, epubFile); f != nil {
		rel := importer.SourcePath(bookID)
		if err := extract(f, filepath.Join(r.DataDir, rel)); err != nil {
			return err
		}
		fields["EpubPath"] = rel
	}
	for _, f := range zr.File {
		if ext := path.Ext(f.Name); strings.TrimSuffix(f.Name, ext) == coverFile && h.Book.CoverPath != "" &&
			validCoverExt(ext) {
			rel := filepath.Join(importer.BookDir(bookID), coverFile+ext)
			if err := extract(f, filepath.Join(r.DataDir, rel)); err != nil {
				return err
			}
			fields["CoverPath"], fields["CoverMediaType"] = rel, h.Book.CoverMediaType
		}
	}
	if err := r.Store.UpdateBook(ctx, bookID, fields); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		r.Log.Warn("remove restored export", "book", bookID, "err", err)
	}
	progress(1)
	r.Log.Info("book restored", "book", bookID, "title", h.Book.Title, "schema", h.Schema)
	return nil
}

func validCoverExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
		return true
	}
	return false
}

// restoredBook returns the book's restored settings and states. Work that
// was in progress is left resumable: a running analysis is paused, a
// running casting starts over.
func restoredBook(b *store.Book) map[string]any {
	status := b.Status
	if status != store.BookReady {
		status = store.BookImported
	}
	analysis := b.AnalysisStatus
	switch analysis {
	case store.AnalysisQueued, store.AnalysisRunning, store.AnalysisMerging:
		analysis = store.AnalysisPaused
	case "":
		analysis = store.AnalysisNone
	}
	casting := b.CastingStatus
	switch casting {
	case store.CastingQueued, store.CastingRunning, "":
		casting = store.CastingNone
	}
	speed := b.NarratorSpeed
	if speed <= 0 {
		speed = 1
	}
	return map[string]any{
		"Title": b.Title, "Author": b.Author, "Language": b.Language, "QuoteStyle": b.QuoteStyle,
		"WordCount": b.WordCount, "ChapterCount": b.ChapterCount,
		"NarratorVoice": b.NarratorVoice, "NarratorSpeed": speed, "Lookahead": b.Lookahead,
		"AnalysisStatus": analysis, "AnalysisError": b.AnalysisError,
		"AnalysisStartedAt": b.AnalysisStartedAt, "AnalysisFinishedAt": b.AnalysisFinishedAt,
		"CastingStatus": casting, "CastingError": b.CastingError, "CastingFinishedAt": b.CastingFinishedAt,
		"Status": status, "Progress": 1.0, "Error": "",
	}
}

// maxExtract bounds a file taken from an export.
const maxExtract = 2 << 30

// extract writes a zip entry to dst atomically.
func extract(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp) //nolint:gosec // a path of the book, built from the data dir
	if err != nil {
		return err
	}
	// The zip's size is bounded by the upload limit, its content is not.
	n, err := io.Copy(out, io.LimitReader(rc, maxExtract+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxExtract {
		err = jobs.Permanent(fmt.Errorf("%s is larger than %d MiB", f.Name, maxExtract>>20))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
