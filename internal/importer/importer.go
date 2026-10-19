// Package importer turns an uploaded EPUB into volumes, chapters,
// paragraphs and segments in the database.
package importer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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
				"Status": store.BookFailed, "Error": store.CleanText(err.Error()),
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

// Description is a book description as stored: valid text of a bounded
// length (a whole book's worth can hide in the metadata).
func Description(d string) string {
	const limit = 5000
	if r := []rune(d); len(r) > limit {
		d = string(r[:limit])
	}
	return store.CleanText(d)
}

// FillDescriptions reads the description of the books imported before it
// was kept from their EPUB (none when the file is gone).
func (im *Importer) FillDescriptions(ctx context.Context) error {
	books, err := im.Store.BooksWithoutDescription(ctx)
	if err != nil {
		return err
	}
	for i := range books {
		desc := im.ReadDescription(books[i].ID, books[i].EpubPath)
		err := im.Store.SetBookDescription(ctx, books[i].ID, desc)
		if err != nil && !errors.Is(err, store.ErrNotFound) { // deleted meanwhile
			return err
		}
	}
	return nil
}

// ReadDescription reads a book's description from its EPUB ("" when the
// file is gone or unreadable).
func (im *Importer) ReadDescription(bookID int64, epubPath string) string {
	if epubPath == "" {
		return ""
	}
	b, f, err := OpenEPUB(filepath.Join(im.DataDir, epubPath))
	if err != nil {
		im.Log.Warn("read the book's description", "book", bookID, "err", err)
		return ""
	}
	_ = f.Close()
	return Description(b.Description)
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
		"Description":    Description(b.Description),
		"QuoteStyle":     style.String(),
		"ChapterCount":   w.chapters,
		"WordCount":      w.words,
		"CoverPath":      coverPath,
		"CoverMediaType": coverType,
	})
}

// coverExt is the file extension of each image type allowed as a cover.
var coverExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
}

// CoverType returns the media type of a cover from the first bytes of its
// content (not from what an EPUB or export claims): only plain images are
// accepted, never SVG or HTML, which a browser would run as script.
func CoverType(head []byte) (mediaType, ext string, ok bool) {
	mediaType = http.DetectContentType(head)
	ext, ok = coverExt[mediaType]
	return mediaType, ext, ok
}

// AllowedCoverType reports whether a stored cover may be served as is.
func AllowedCoverType(mediaType string) bool {
	_, ok := coverExt[mediaType]
	return ok
}

// SaveCover writes a cover image read from r to the book's directory and
// returns its path (relative to the data dir) and media type.
func SaveCover(dataDir string, bookID int64, r io.Reader) (string, string, error) {
	br := bufio.NewReader(io.LimitReader(r, maxCover))
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	mediaType, ext, ok := CoverType(head)
	if !ok {
		return "", "", fmt.Errorf("unsupported cover type %s", mediaType)
	}
	rel := filepath.Join(BookDir(bookID), "cover"+ext)
	dst, err := os.Create(filepath.Join(dataDir, rel)) //nolint:gosec // path is built from the data dir and a book ID
	if err != nil {
		return "", "", err
	}
	_, err = io.Copy(dst, br)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", err
	}
	return rel, mediaType, nil
}

const maxCover = 20 << 20

// saveCover copies the cover image next to the EPUB. Failures only lose
// the cover, so they are logged, not returned.
func (im *Importer) saveCover(b *epub.Book, bookID int64) (string, string) {
	if b.CoverHref == "" {
		return "", ""
	}
	src, err := b.Open(b.CoverHref)
	if err != nil {
		im.Log.Warn("cover missing", "book", bookID, "href", b.CoverHref, "err", err)
		return "", ""
	}
	defer func() { _ = src.Close() }()
	rel, mediaType, err := SaveCover(im.DataDir, bookID, src)
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
		emph := make([]segment.Span, len(blk.Emphasis))
		for i, e := range blk.Emphasis {
			emph[i] = segment.Span{Start: e.Start, End: e.End}
		}
		parts = w.seg.Paragraph(blk.Text, emph...)
	}
	c.Paragraphs = append(c.Paragraphs, p)
	c.Chapter.WordCount += p.WordCount

	for _, part := range parts {
		kind := store.SegmentNarration
		switch part.Kind {
		case segment.Quote:
			kind = store.SegmentQuote
		case segment.Thought:
			kind = store.SegmentThought
		}
		for _, piece := range segment.ChunkPart(part, segment.MaxChunk) {
			spans := make([][2]int, len(piece.Emphasis))
			for i, e := range piece.Emphasis {
				spans[i] = [2]int{e.Start, e.End}
			}
			c.Segments = append(c.Segments, store.Segment{
				// Index into Paragraphs; CreateChapterContent swaps in the ID.
				ParagraphID: int64(pi),
				Idx:         len(c.Segments),
				Kind:        kind,
				QuoteRef:    piece.Quote,
				Text:        piece.Text,
				Emphasis:    store.EncodeSpans(spans),
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
