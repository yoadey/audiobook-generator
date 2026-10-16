package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestTouchMatchingChapters(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := &store.Book{Title: "B", Status: store.BookImported}
		require.NoError(t, s.CreateBook(ctx, b))
		mk := func(title, text string) int64 {
			c := &store.ChapterContent{
				Chapter:    store.Chapter{BookID: b.ID, Title: title},
				Paragraphs: []store.Paragraph{{Kind: store.ParagraphText, Text: text}},
			}
			require.NoError(t, s.CreateChapterContent(ctx, c))
			return c.Chapter.ID
		}
		inText := mk("One", "ÉMILE came home.")
		inTitle := mk("Émile returns", "Nothing here.")
		none := mk("Two", "Nobody.")
		match := func(text string) bool { return strings.Contains(strings.ToLower(text), "émile") }
		require.NoError(t, s.TouchMatchingChapters(ctx, b.ID, match))
		for id, want := range map[int64]int{inText: 1, inTitle: 1, none: 0} {
			c, err := s.GetChapter(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, c.VoiceRev, c.Title)
		}
	})
}
