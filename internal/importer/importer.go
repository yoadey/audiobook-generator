// Package importer turns an uploaded EPUB into volumes, chapters,
// paragraphs and segments in the database.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path"
	"path/filepath"

	"github.com/yoadey/audiobook-generator/internal/epub"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/segment"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType is the job type of an import.
const JobType = "import_book"

// Importer imports books stored below DataDir.
type Importer struct {
	Store   *store.Store
	DataDir string
	Log     *slog.Logger
}

// BookDir is the directory of a book's files, relative to the data dir.
func BookDir(bookID int64) string {
	return filepath.Join("books", fmt.Sprint(bookID))
}

// SourcePath is where the uploaded EPUB is kept, relative to the data dir.
func SourcePath(bookID int64) string {
	return filepath.Join(BookDir(bookID), "source.epub")
}

// Handler is the job handler for JobType.
func (im *Importer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: "import",
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.BookID == nil {
				return jobs.Permanent(errors.New("import job without book"))
			}
			return im.Import(ctx, *j.BookID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.BookID == nil {
				return
			}
			if uerr := im.Store.UpdateBook(ctx, *j.BookID, map[string]any{
				"Status": store.BookFailed, "Error": err.Error(),
			}); uerr != nil {
				im.Log.Error("mark book failed", "book", *j.BookID, "err", uerr)
			}
		},
	}
}

// OpenEPUB opens and validates an EPUB file.
func OpenEPUB(p string) (*epub.Book, *os.File, error) {
	f, err := os.Open(p) //nolint:gosec // path is built from the data dir and a book ID
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	b, err := epub.Open(f, st.Size())
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return b, f, nil
}

// Import (re-)imports a book from scratch. It is idempotent, so a failed
// attempt can simply be retried.
func (im *Importer) Import(ctx context.Context, bookID int64, progress func(float64)) error {
	book, err := im.Store.GetBook(ctx, bookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	b, f, err := OpenEPUB(filepath.Join(im.DataDir, book.EpubPath))
	if err != nil {
		return jobs.Permanent(fmt.Errorf("open EPUB: %w", err))
	}
	defer func() { _ = f.Close() }()

	setProgress := func(p float64) {
		progress(p)
		_ = im.Store.UpdateBook(ctx, bookID, map[string]any{"Progress": p})
	}

	// Pass 1: structure and quote style.
	var quotes segment.StyleCounter
	outline, err := epub.Plan(b, func(blk epub.Block) {
		if blk.Kind == epub.Text {
			quotes.Add(blk.Text)
		}
	})
	if err != nil {
		return jobs.Permanent(fmt.Errorf("read EPUB: %w", err))
	}
	if len(outline.Chapters) == 0 {
		return jobs.Permanent(errors.New("the EPUB contains no readable text"))
	}
	style := quotes.Style()
	setProgress(0.05)

	if err := im.Store.ResetBookContent(ctx, bookID); err != nil {
		return err
	}
	vols := make([]store.Volume, len(outline.Volumes))
	for i, v := range outline.Volumes {
		vols[i] = store.Volume{BookID: bookID, Idx: i, Title: v.Title}
	}
	if err := im.Store.CreateVolumes(ctx, vols); err != nil {
		return err
	}

	// Pass 2: text, chapter by chapter.
	w := writer{
		ctx: ctx, st: im.Store, bookID: bookID, outline: outline, volumes: vols, style: style,
		progress: func(done int) {
			setProgress(0.05 + 0.95*float64(done)/float64(len(outline.Chapters)))
		},
	}
	if err := b.Walk(epub.Visitor{Block: w.block}); err != nil {
		return fmt.Errorf("import text: %w", err)
	}
	if err := w.flush(); err != nil {
		return err
	}

	coverPath, coverType := im.saveCover(b, bookID)
	return im.Store.UpdateBook(ctx, bookID, map[string]any{
		"Status":         store.BookImported,
		"Progress":       1.0,
		"Error":          "",
		"Language":       b.Language,
		"QuoteStyle":     style.String(),
		"ChapterCount":   w.chapters,
		"WordCount":      w.words,
		"CoverPath":      coverPath,
		"CoverMediaType": coverType,
	})
}

// saveCover copies the cover image next to the EPUB. Failures only lose
// the cover, so they are logged, not returned.
func (im *Importer) saveCover(b *epub.Book, bookID int64) (string, string) {
	if b.CoverHref == "" {
		return "", ""
	}
	mediaType := b.CoverMediaType
	if mediaType == "" {
		mediaType = mime.TypeByExtension(path.Ext(b.CoverHref))
	}
	rel := filepath.Join(BookDir(bookID), "cover"+path.Ext(b.CoverHref))
	src, err := b.Open(b.CoverHref)
	if err != nil {
		im.Log.Warn("cover missing", "book", bookID, "href", b.CoverHref, "err", err)
		return "", ""
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(filepath.Join(im.DataDir, rel)) //nolint:gosec // path is built from the data dir and a book ID
	if err != nil {
		im.Log.Warn("save cover", "book", bookID, "err", err)
		return "", ""
	}
	_, err = io.Copy(dst, io.LimitReader(src, 20<<20))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		im.Log.Warn("save cover", "book", bookID, "err", err)
		return "", ""
	}
	return rel, mediaType
}

// writer collects the blocks of the current chapter and stores each
// chapter once its last block has been seen.
type writer struct {
	ctx      context.Context
	st       *store.Store
	bookID   int64
	outline  *epub.Outline
	volumes  []store.Volume
	style    segment.Style
	progress func(done int)

	ci       int // index of the current outline chapter
	cur      *store.ChapterContent
	seg      *segment.Segmenter
	chapters int
	words    int64
}

func (w *writer) block(blk epub.Block) error {
	for w.ci < len(w.outline.Chapters) && blk.Index >= w.outline.Chapters[w.ci].End {
		if err := w.flush(); err != nil {
			return err
		}
		w.ci++
	}
	if w.ci >= len(w.outline.Chapters) || blk.Index < w.outline.Chapters[w.ci].Start {
		return nil // between chapters (e.g. a dropped title page)
	}
	if w.cur == nil {
		oc := w.outline.Chapters[w.ci]
		var volumeID *int64
		if oc.Volume >= 0 {
			volumeID = &w.volumes[oc.Volume].ID
		}
		w.cur = &store.ChapterContent{Chapter: store.Chapter{
			BookID: w.bookID, VolumeID: volumeID, Idx: w.chapters, Title: oc.Title,
			Excluded: oc.Excluded, Status: store.ChapterImported,
		}}
		w.seg = segment.New(w.style)
	}
	w.add(blk)
	return nil
}

func (w *writer) add(blk epub.Block) {
	c := w.cur
	pi := len(c.Paragraphs)
	p := store.Paragraph{Idx: pi, Text: blk.Text, WordCount: epub.WordCount(blk.Text)}
	var parts []segment.Part
	switch blk.Kind {
	case epub.SceneBreak:
		p.Kind = store.ParagraphSceneBreak
		p.WordCount = 0
		w.seg.Break()
	case epub.Heading:
		p.Kind = store.ParagraphHeading
		parts = []segment.Part{{Kind: segment.Narration, Text: blk.Text}}
	default:
		p.Kind = store.ParagraphText
		parts = w.seg.Paragraph(blk.Text)
	}
	c.Paragraphs = append(c.Paragraphs, p)
	c.Chapter.WordCount += p.WordCount

	for _, part := range parts {
		kind := store.SegmentNarration
		if part.Kind == segment.Quote {
			kind = store.SegmentQuote
		}
		for _, text := range segment.Chunk(part.Text, segment.MaxChunk) {
			c.Segments = append(c.Segments, store.Segment{
				// Index into Paragraphs; CreateChapterContent swaps in the ID.
				ParagraphID: int64(pi),
				Idx:         len(c.Segments),
				Kind:        kind,
				QuoteRef:    part.Quote,
				Text:        text,
			})
		}
	}
}

func (w *writer) flush() error {
	if w.cur == nil {
		return nil
	}
	w.cur.Chapter.QuoteCount = w.seg.Quotes()
	if err := w.st.CreateChapterContent(w.ctx, w.cur); err != nil {
		return fmt.Errorf("store chapter %q: %w", w.cur.Chapter.Title, err)
	}
	w.chapters++
	w.words += int64(w.cur.Chapter.WordCount)
	w.cur = nil
	w.progress(w.ci + 1)
	return nil
}
