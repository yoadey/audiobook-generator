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
	"bytes"
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
	"github.com/yoadey/audiobook-generator/internal/lexicon"
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

// Limits on what an export may contain (it is user input): an export has
// a dozen entries; a row is one JSON line.
const (
	maxEntries   = 64
	maxEntrySize = 8 << 30
	maxRowSize   = 32 << 20
)

func readHeader(zr *zip.Reader) (*Header, error) {
	if len(zr.File) > maxEntries {
		return nil, fmt.Errorf("%w: too many files", ErrNotExport)
	}
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

// jsonl reads rows of one table, one JSON object per line. Lines are
// bounded, so a crafted export cannot make a single row exhaust memory.
type jsonl struct {
	r *bufio.Reader
	// keep, when set, drops rows it returns false for.
	keep func(row any) bool
}

func (l *jsonl) Next(dst any) (bool, error) {
	for {
		line, err := l.line()
		if err != nil || line == nil {
			return false, err
		}
		if err := json.Unmarshal(line, dst); err != nil {
			return false, jobs.Permanent(fmt.Errorf("invalid row: %w", err))
		}
		if l.keep == nil || l.keep(dst) {
			return true, nil
		}
	}
}

// line returns the next non-empty line (nil at the end).
func (l *jsonl) line() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := l.r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxRowSize {
			return nil, jobs.Permanent(fmt.Errorf("a row is larger than %d MiB", maxRowSize>>20))
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) || err == nil:
			if t := bytes.TrimSpace(buf); len(t) > 0 {
				return t, nil
			}
			if err != nil {
				return nil, nil
			}
			buf = buf[:0]
		default:
			return nil, err
		}
	}
}

// Restore fills book bookID from its uploaded export. It is idempotent: a
// failed attempt can run again.
func (r *Restorer) Restore(ctx context.Context, bookID int64, progress func(float64)) error {
	src := filepath.Join(r.DataDir, RestorePath(bookID))
	zr, err := zip.OpenReader(src)
	if errors.Is(err, os.ErrNotExist) {
		// Removed after a finished restore (a crash before the job was
		// marked done brings it here again).
		if b, gerr := r.Store.GetBook(ctx, bookID); gerr == nil && b.Status != store.BookImporting {
			return nil
		}
	}
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
		rows := &jsonl{r: bufio.NewReaderSize(io.LimitReader(rc, maxEntrySize), 1<<16)}
		if table == store.TableLexicon {
			rows.keep = validLexiconRow
		}
		return rows, nil
	})
	closeOpen()
	if err != nil {
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
	if f := coverEntry(&zr.Reader); f != nil && h.Book.CoverPath != "" {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		rel, mediaType, err := importer.SaveCover(r.DataDir, bookID, rc)
		_ = rc.Close()
		if err != nil {
			r.Log.Warn("restore cover", "book", bookID, "err", err) // the book works without
		} else {
			fields["CoverPath"], fields["CoverMediaType"] = rel, mediaType
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

// coverEntry returns the export's cover (its type is taken from the
// content, not from the name or the header).
func coverEntry(zr *zip.Reader) *zip.File {
	for _, f := range zr.File {
		if strings.TrimSuffix(f.Name, path.Ext(f.Name)) == coverFile {
			return f
		}
	}
	return nil
}

var (
	knownAnalysis = map[store.AnalysisStatus]bool{
		store.AnalysisNone: true, store.AnalysisPaused: true, store.AnalysisDone: true, store.AnalysisFailed: true,
	}
	knownCasting = map[store.CastingStatus]bool{
		store.CastingNone: true, store.CastingDone: true, store.CastingFailed: true,
	}
)

// validLexiconRow keeps the pronunciations the API would accept.
func validLexiconRow(row any) bool {
	e, ok := row.(*store.LexiconEntry)
	if !ok {
		return false
	}
	e.Term, e.Replacement = lexicon.Normalize(e.Term, e.Replacement)
	return lexicon.Validate(e.Term, e.Replacement, e.IsPhonemes) == nil
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
	if speed < 0.5 || speed > 2 {
		speed = 1
	}
	lookahead := min(max(b.Lookahead, 0), 50) // the API's bounds
	if !knownAnalysis[analysis] {
		analysis = store.AnalysisNone
	}
	if !knownCasting[casting] {
		casting = store.CastingNone
	}
	return map[string]any{
		"Title": b.Title, "Author": b.Author, "Language": b.Language, "QuoteStyle": b.QuoteStyle,
		"WordCount": b.WordCount, "ChapterCount": b.ChapterCount,
		"NarratorVoice": b.NarratorVoice, "NarratorSpeed": speed, "Lookahead": lookahead,
		"PreviewText":    truncateRunes(store.CleanText(b.PreviewText), 500),
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

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
