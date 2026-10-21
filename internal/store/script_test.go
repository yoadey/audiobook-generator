package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestApplyScript(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		_, cs := bookWithChapters(t, s, 1)
		ch := cs[0]
		paras, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		// The second paragraph was prepared (a spoken form on its segment).
		require.NoError(t, s.DB().Model(&store.Segment{}).Where("id = ?", segs[1].ID).
			Update("spoken_as", `[{"a":0,"b":4,"t":"Text"}]`).Error)
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", ch.ID).
			Updates(map[string]any{"prep_status": store.PrepDone, "prep_part": 2}).Error)

		script := []store.ScriptParagraph{
			// As before: kept, labeled.
			{ParagraphID: paras[0].ID, Segments: []store.ScriptSegment{{Kind: store.SegmentNarration, Text: "Text one."}}},
			// Split anew: a thought and narration.
			{ParagraphID: paras[1].ID, Direction: "softly", Segments: []store.ScriptSegment{
				{Kind: store.SegmentThought, Voicing: store.VoicingThought, Text: "Text", Quote: 1, Emotion: "sad",
					Direction: "wistful", Confidence: 0.8},
				{Kind: store.SegmentNarration, Text: "two."},
			}},
		}
		assert.ErrorIs(t, s.ApplyScript(ctx, ch.ID, script, 0, 2), store.ErrStale, "not running")
		_, err = s.TransitionAttribution(ctx, ch.ID, store.AttributionRunning, nil, store.AttributionNone)
		require.NoError(t, err)
		require.NoError(t, s.ApplyScript(ctx, ch.ID, script, 0, 2))

		paras, after, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		require.Len(t, after, 3)
		assert.Equal(t, segs[0].ID, after[0].ID, "kept")
		assert.Equal(t, []int{0, 1, 2}, []int{after[0].Idx, after[1].Idx, after[2].Idx})
		assert.Equal(t, store.SegmentThought, after[1].Kind)
		assert.Equal(t, 1, after[1].QuoteRef)
		assert.Equal(t, "wistful", after[1].Direction)
		assert.Equal(t, store.VoicingThought, after[1].Voicing)
		assert.Equal(t, "softly", paras[1].Direction)
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, got.QuoteCount)
		assert.Equal(t, 2, got.AttributionPart)
		assert.Equal(t, store.PrepNone, got.PrepStatus, "its marks were on a replaced segment")

		// A paragraph with a speaker chosen by hand stays as it is.
		require.NoError(t, s.SetQuoteSpeaker(ctx, ch.ID, 1, nil, "angry"))
		_, err = s.TransitionAttribution(ctx, ch.ID, store.AttributionRunning, map[string]any{"attribution_part": 0},
			store.AttributionRunning)
		require.NoError(t, err)
		require.NoError(t, s.ApplyScript(ctx, ch.ID, []store.ScriptParagraph{{ParagraphID: paras[1].ID,
			Segments: []store.ScriptSegment{{Kind: store.SegmentNarration, Text: "Text two."}}}}, 0, 2))
		_, again, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, after[1].ID, again[1].ID)
		assert.Equal(t, store.SegmentThought, again[1].Kind)

		// ResetAttribution forgets directions, not the split.
		require.NoError(t, s.ResetAttribution(ctx, ch.ID))
		paras, again, err = s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		assert.Empty(t, paras[1].Direction)
		assert.Len(t, again, 3)
	})
}
