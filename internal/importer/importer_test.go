package importer_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func sample(t testing.TB) []byte {
	t.Helper()
	data, err := epubtest.Sample()
	require.NoError(t, err)
	return data
}

func setup(t *testing.T, s *store.Store, epubData []byte) (*importer.Importer, *store.Book) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	b := &store.Book{Title: "pending", Status: store.BookImporting}
	require.NoError(t, s.CreateBook(ctx, b))
	b.EpubPath = importer.SourcePath(b.ID)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, importer.BookDir(b.ID)), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, b.EpubPath), epubData, 0o600))
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"EpubPath": b.EpubPath}))
	return &importer.Importer{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}, b
}

func TestImport(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		im, b := setup(t, s, sample(t))
		var last float64
		require.NoError(t, im.Import(ctx, b.ID, func(p float64) { last = p }))
		assert.InDelta(t, 1.0, last, 0.001)

		book, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.BookImported, book.Status)
		assert.Equal(t, "en", book.Language)
		assert.Equal(t, "curly_double", book.QuoteStyle)
		require.NotNil(t, book.Description)
		assert.Equal(t, "Two mornings in a small town: will it rain?\n\nA sample book for the tests.", *book.Description)
		assert.Equal(t, 4, book.ChapterCount)
		assert.Equal(t, "image/png", book.CoverMediaType)
		cover, err := os.ReadFile(filepath.Join(im.DataDir, book.CoverPath))
		require.NoError(t, err)
		assert.Equal(t, epubtest.TinyPNG, cover)

		vols, err := s.ListVolumes(ctx, b.ID)
		require.NoError(t, err)
		require.Len(t, vols, 2)
		assert.Equal(t, "Book One", vols[0].Title)
		assert.Equal(t, 2, vols[0].ChapterCount)

		chapters, total, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		require.EqualValues(t, 4, total)
		var titles []string
		for _, c := range chapters {
			titles = append(titles, c.Title)
		}
		assert.Equal(t, []string{"Copyright", "Chapter 1", "Chapter 2", "Chapter 3"}, titles)
		assert.True(t, chapters[0].Excluded)
		assert.Nil(t, chapters[0].VolumeID)
		assert.Equal(t, vols[0].ID, *chapters[1].VolumeID)

		// Chapter 1: three quotations, the last spanning two paragraphs.
		ch1 := chapters[1]
		assert.Equal(t, 3, ch1.QuoteCount)
		paras, segs, err := s.ChapterText(ctx, ch1.ID)
		require.NoError(t, err)
		var kinds []store.ParagraphKind
		for _, p := range paras {
			kinds = append(kinds, p.Kind)
		}
		assert.Equal(t, []store.ParagraphKind{
			store.ParagraphHeading, store.ParagraphText, store.ParagraphText, store.ParagraphText,
			store.ParagraphSceneBreak, store.ParagraphText,
		}, kinds)
		type seg struct {
			Kind  store.SegmentKind
			Quote int
			Text  string
		}
		var got []seg
		for _, s := range segs {
			got = append(got, seg{s.Kind, s.QuoteRef, s.Text})
		}
		assert.Equal(t, []seg{
			{store.SegmentNarration, 0, "Chapter 1"},
			{store.SegmentQuote, 1, "“Good morning,”"},
			{store.SegmentNarration, 0, "said Anna."},
			{store.SegmentQuote, 2, "“Is it raining?”"},
			{store.SegmentNarration, 0, "Tom looked outside."},
			{store.SegmentQuote, 3, "“It was raining earlier,"},
			{store.SegmentQuote, 3, "“but it has stopped now.”"},
			{store.SegmentNarration, 0, "Later that day, nothing happened."},
		}, got)
		assert.Equal(t, paras[3].ID, segs[6].ParagraphID)

		// The long paragraph of Chapter 3 is chunked for TTS.
		_, segs3, err := s.ChapterText(ctx, chapters[3].ID)
		require.NoError(t, err)
		assert.Greater(t, len(segs3), 3)
		for _, s := range segs3 {
			assert.LessOrEqual(t, len([]rune(s.Text)), 400)
		}

		// Re-import is idempotent.
		require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
		_, total, err = s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		assert.EqualValues(t, 4, total)
	})
}

// Books imported before descriptions were kept get theirs from the EPUB;
// one whose EPUB is gone gets none and is not tried again.
func TestFillDescriptions(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		im, b := setup(t, s, sample(t))
		gone := &store.Book{Title: "gone", Status: store.BookReady, EpubPath: "books/99/source.epub"}
		importing := &store.Book{Title: "importing", Status: store.BookImporting}
		require.NoError(t, s.CreateBook(ctx, gone))
		require.NoError(t, s.CreateBook(ctx, importing))
		require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"Status": store.BookImported}))

		require.NoError(t, im.FillDescriptions(ctx))
		book, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		require.NotNil(t, book.Description)
		assert.Contains(t, *book.Description, "Two mornings")
		g, err := s.GetBook(ctx, gone.ID)
		require.NoError(t, err)
		require.NotNil(t, g.Description)
		assert.Empty(t, *g.Description)
		i, err := s.GetBook(ctx, importing.ID)
		require.NoError(t, err)
		assert.Nil(t, i.Description, "the import sets it")

		left, err := s.BooksWithoutDescription(ctx)
		require.NoError(t, err)
		assert.Empty(t, left)
	})
}

func TestDescription(t *testing.T) {
	assert.Equal(t, "ok\uFFFD", importer.Description("o\x00k\xff"))
	assert.Equal(t, strings.Repeat("ä", 5000), importer.Description(strings.Repeat("ä", 5001)))
}

func TestMergeAndSplit(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		im, b := setup(t, s, sample(t))
		require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
		chapters, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		ch1, ch2 := chapters[1], chapters[2]

		merged, err := s.MergeChapterWithNext(ctx, ch1.ID)
		require.NoError(t, err)
		assert.Equal(t, ch1.WordCount+ch2.WordCount, merged.WordCount)
		assert.Equal(t, 4, merged.QuoteCount)
		_, err = s.GetChapter(ctx, ch2.ID)
		assert.ErrorIs(t, err, store.ErrNotFound)

		after, total, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		require.EqualValues(t, 3, total)
		for i, c := range after {
			assert.Equal(t, i, c.Idx, "indexes stay dense")
		}
		paras, segs, err := s.ChapterText(ctx, ch1.ID)
		require.NoError(t, err)
		last := segs[len(segs)-1]
		assert.Equal(t, "“Bye,”", segs[len(segs)-2].Text)
		assert.Equal(t, 4, segs[len(segs)-2].QuoteRef)
		assert.Equal(t, "said Tom.", last.Text)
		for i, p := range paras {
			assert.Equal(t, i, p.Idx)
		}

		// Split it again at the "Chapter 2" heading.
		var at store.Paragraph
		for _, p := range paras {
			if p.Text == "Chapter 2" {
				at = p
			}
		}
		created, err := s.SplitChapter(ctx, ch1.ID, at.ID, "Chapter 2 (restored)")
		require.NoError(t, err)
		assert.Equal(t, ch1.Idx+1, created.Idx)
		assert.Equal(t, ch2.WordCount, created.WordCount)
		assert.Equal(t, 1, created.QuoteCount)
		assert.Equal(t, ch1.VolumeID, created.VolumeID)
		_, segs2, err := s.ChapterText(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, segs2[0].Idx)
		assert.Equal(t, 1, segs2[1].QuoteRef)

		orig, err := s.GetChapter(ctx, ch1.ID)
		require.NoError(t, err)
		assert.Equal(t, ch1.WordCount, orig.WordCount)

		// Invalid operations.
		lastCh := after[len(after)-1]
		_, err = s.MergeChapterWithNext(ctx, lastCh.ID)
		assert.ErrorIs(t, err, store.ErrInvalid)
		_, err = s.SplitChapter(ctx, ch1.ID, paras[0].ID, "x")
		assert.ErrorIs(t, err, store.ErrInvalid)

		// Delete removes everything.
		require.NoError(t, s.DeleteBook(ctx, b.ID))
		_, total, err = s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		assert.Zero(t, total)
	})
}

func TestImportRejectsGarbage(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		im, b := setup(t, s, []byte("not a zip"))
		err := im.Import(context.Background(), b.ID, func(float64) {})
		assert.ErrorContains(t, err, "open EPUB")
	})
}

func TestImportThoughtsAndStyles(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		data, err := epubtest.Build(epubtest.Options{
			Title: "Thinking", Language: "en",
			Docs: []epubtest.Doc{{Name: "c1.xhtml", Body: `<h1>One</h1>
<p>'Come in,' said Ann. 'Sit.'</p>
<p>She frowned. <i>This is wrong.</i> It was <em>very</em> late.</p>
<p>'Fine.' He left.</p>`}},
			TOC: []epubtest.Entry{{Title: "One", Href: "c1.xhtml"}},
		})
		require.NoError(t, err)
		im, b := setup(t, s, data)
		require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
		book, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, "straight_single", book.QuoteStyle)

		cs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 10)
		require.NoError(t, err)
		require.Len(t, cs, 1)
		assert.Equal(t, 4, cs[0].QuoteCount, "three quotations and a thought")
		_, segs, err := s.ChapterText(ctx, cs[0].ID)
		require.NoError(t, err)
		type seg struct {
			Kind     store.SegmentKind
			Ref      int
			Text     string
			Emphasis string
		}
		var got []seg
		for _, sg := range segs {
			got = append(got, seg{sg.Kind, sg.QuoteRef, sg.Text, sg.Emphasis})
		}
		assert.Equal(t, []seg{
			{store.SegmentNarration, 0, "One", ""},
			{store.SegmentQuote, 1, "'Come in,'", ""},
			{store.SegmentNarration, 0, "said Ann.", ""},
			{store.SegmentQuote, 2, "'Sit.'", ""},
			{store.SegmentNarration, 0, "She frowned.", ""},
			{store.SegmentThought, 3, "This is wrong.", "[[0,14]]"},
			{store.SegmentNarration, 0, "It was very late.", "[[7,11]]"},
			{store.SegmentQuote, 4, "'Fine.'", ""},
			{store.SegmentNarration, 0, "He left.", ""},
		}, got)
	})
}
