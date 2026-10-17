package prepare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func TestInterpret(t *testing.T) {
	paras := []store.Paragraph{
		{ID: 1, Kind: store.ParagraphText, Text: "“Run,” said Ryoka. She had Lv. 5 and 20 HP. The door was open."},
		{ID: 2, Kind: store.ParagraphSceneBreak},
		{ID: 3, Kind: store.ParagraphText, Text: "It was over."},
	}
	segs := []store.Segment{
		{ID: 10, ParagraphID: 1, Text: "“Run,”", QuoteRef: 1, Kind: store.SegmentQuote},
		{ID: 11, ParagraphID: 1, Text: "said Ryoka. She had Lv. 5 and 20 HP. The door was open.", Kind: store.SegmentNarration},
		{ID: 12, ParagraphID: 3, Text: "It was over.", Kind: store.SegmentNarration},
	}
	ans := &answer{}
	ans.Moods = append(ans.Moods,
		struct {
			P    int    `json:"p"`
			Mood string `json:"mood"`
		}{1, "Tense"}, struct {
			P    int    `json:"p"`
			Mood string `json:"mood"`
		}{3, "furious"}, struct {
			P    int    `json:"p"`
			Mood string `json:"mood"`
		}{9, "calm"})
	type pause = struct {
		P      int    `json:"p"`
		After  string `json:"after"`
		Length string `json:"length"`
	}
	ans.Pauses = []pause{
		{1, "and 20 HP.", "long"},     // found: a long pause before "The door"
		{1, "The door was open.", ""}, // only one per paragraph
		{3, "It was", "short"},        // not at a clause end
		{3, "the end.", "short"},      // not in the text
	}
	type spoken = struct {
		P    int    `json:"p"`
		Text string `json:"text"`
		Say  string `json:"say"`
	}
	ans.Spoken = []spoken{
		{1, "Lv. 5", "level five"},
		{1, "HP", "[hit points](/x/)"},  // Kokoro syntax is dropped
		{1, "the door was", "a gate"},   // prose: not reworded
		{1, "Lv. 5 and", "level"},       // overlaps the first
		{1, "Mars", "the red planet"},   // not in the text
		{3, "over", "done"},             // prose
		{1, "Run,”", "Run quickly"},     // in the first segment
		{1, "said Ryoka. She", "x x x"}, // prose? capitalized: allowed but harmless
	}
	type pron = struct {
		Term string `json:"term"`
		Say  string `json:"say"`
		Note string `json:"note"`
	}
	ans.Pronunciations = []pron{
		{"Ryoka", "Ree-oh-kah", "invented name"},
		{"Zorblax", "zor-blax", "not in the text"},
		{"Erin", "air-in", "known already"},
		{"door", "door", "same"},
		{"Ryoka", "rye-oh-ka", "twice"},
	}
	got := interpret(paras, segs, 0, 3, []string{"erin"}, ans)

	assert.Equal(t, map[int64]string{1: "tense"}, got.Moods)
	require.Len(t, got.Segments, 2)
	first, second := got.Segments[0], got.Segments[1]
	assert.Equal(t, int64(10), first.SegmentID)
	assert.Equal(t, []store.SpokenForm{{Start: 1, End: 6, Say: "Run quickly"}}, first.SpokenAs)
	assert.Equal(t, int64(11), second.SegmentID)
	// "said Ryoka. She had Lv. 5 and 20 HP. The door was open."
	assert.Equal(t, []store.Pause{{Offset: 36, Ms: longPauseMs}}, second.Pauses)
	assert.Equal(t, []store.SpokenForm{
		{Start: 0, End: 15, Say: "x x x"},
		{Start: 20, End: 25, Say: "level five"},
		{Start: 33, End: 35, Say: "hit points x"},
	}, second.SpokenAs)
	assert.Equal(t, []store.LexiconEntry{{Term: "Ryoka", Replacement: "ree-oh-kah", Note: "invented name"}},
		got.Suggestions)
}

func TestSpotOK(t *testing.T) {
	for spot, ok := range map[string]bool{
		"Lv.": true, "HP": true, "12/20": true, "St.": true, "lv.": true, "km": true,
		"door": false, "the old man": false, "approx.": true, "": false,
	} {
		assert.Equal(t, ok, spotOK([]rune(spot), "something"), spot)
	}
	assert.False(t, spotOK([]rune("HP"), "hp"), "the same")
	assert.False(t, spotOK([]rune("HP"), "123"), "no words")
}

func TestSplitParts(t *testing.T) {
	paras := []store.Paragraph{
		{WordCount: 100}, {WordCount: 100}, {Kind: store.ParagraphSceneBreak}, {WordCount: 100}, {WordCount: 500},
	}
	assert.Equal(t, []part{{0, 2}, {2, 4}, {4, 5}}, split(paras, 300))
	assert.Nil(t, split(nil, 300))
}
