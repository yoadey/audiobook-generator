package attribution

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func TestNumberedKeepsItalics(t *testing.T) {
	paras := []store.Paragraph{
		{ID: 1, Kind: store.ParagraphHeading, Text: "One"},
		{ID: 2, Kind: store.ParagraphText, Text: "She frowned. This is a long thought, in two pieces. “Hello.”", WordCount: 10},
		{ID: 3, Kind: store.ParagraphSceneBreak},
	}
	byPara := map[int64][]store.Segment{2: {
		{Kind: store.SegmentNarration, Text: "She frowned."},
		{Kind: store.SegmentThought, QuoteRef: 1, Text: "This is a long thought,", Emphasis: "[[0,23]]"},
		{Kind: store.SegmentThought, QuoteRef: 1, Text: "in two pieces.", Emphasis: "[[0,13]]"},
		{Kind: store.SegmentQuote, QuoteRef: 2, Text: "“Hello.”"},
	}}
	ctx, text, n := numbered(paras, byPara, 0, 1, 3)
	assert.Equal(t, "# One", ctx)
	assert.Equal(t, "[p2] She frowned. *This is a long thought,* *in two pieces*. “Hello.”\n\n* * *", text)
	assert.Equal(t, 1, n)
}

func TestDirectionAndVoicing(t *testing.T) {
	assert.Equal(t, "barely above a whisper, trembling", direction(" “barely above a whisper,\n trembling.” "))
	assert.Empty(t, direction("Neutral"))
	long := direction(strings.Repeat("very ", 60))
	assert.LessOrEqual(t, len([]rune(long)), maxDirection)
	assert.Equal(t, store.VoicingRemote, voicingOf("Off-voice"))
	assert.Equal(t, store.VoicingTelepathy, voicingOf("telepathy"))
	assert.Empty(t, voicingOf("narration"))
}
