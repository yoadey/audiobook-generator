package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestTouchVoicesOnlyWithCharacterVoices(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		var revs []func() int
		for _, voices := range []bool{true, false} {
			b := &store.Book{Title: "B"}
			require.NoError(t, s.CreateBook(ctx, b))
			require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"CharacterVoices": voices}))
			anna := &store.Character{BookID: b.ID, Name: "Anna"}
			require.NoError(t, s.CreateCharacter(ctx, anna))
			ch := &store.Chapter{BookID: b.ID, Title: "One", QuoteCount: 1}
			require.NoError(t, s.DB().Create(ch).Error)
			require.NoError(t, s.DB().Create(&store.Segment{ChapterID: ch.ID, ParagraphID: 1, Kind: store.SegmentQuote,
				QuoteRef: 1, Text: "“Hi.”", SpeakerCharacterID: &anna.ID}).Error)
			require.NoError(t, s.TouchBookVoices(ctx, b.ID))
			require.NoError(t, s.TouchCharacterVoices(ctx, anna.ID))
			revs = append(revs, func() int {
				got, err := s.GetChapter(ctx, ch.ID)
				require.NoError(t, err)
				return got.VoiceRev
			})
		}
		assert.Equal(t, 2, revs[0](), "with character voices: outdated twice")
		assert.Equal(t, 0, revs[1](), "the narrator reads everything: nothing changed")
	})
}
