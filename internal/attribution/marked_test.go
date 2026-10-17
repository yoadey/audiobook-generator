package attribution

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func TestMarkedThoughts(t *testing.T) {
	paras := []store.Paragraph{{ID: 1, Kind: store.ParagraphText}}
	segs := []store.Segment{
		{ID: 1, ParagraphID: 1, Kind: store.SegmentNarration, Text: "She frowned."},
		{ID: 2, ParagraphID: 1, Kind: store.SegmentThought, QuoteRef: 1, Text: "This is a long thought,"},
		{ID: 3, ParagraphID: 1, Kind: store.SegmentThought, QuoteRef: 1, Text: "in two pieces."},
		{ID: 4, ParagraphID: 1, Kind: store.SegmentQuote, QuoteRef: 2, Text: "“Hello.”"},
	}
	_, text, refs := marked(paras, segs, 0, 0, 1)
	assert.Equal(t, "She frowned. [q1]*This is a long thought, in two pieces.* [q2]“Hello.”", text)
	assert.Equal(t, []int{1, 2}, refs)
}
