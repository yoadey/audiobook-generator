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
		{1, "HP", "[hit] {points}"},     // Kokoro syntax is dropped
		{1, "the door was", "a gate"},   // prose: not reworded
		{1, "Lv. 5 and", "level"},       // overlaps the first
		{1, "Mars", "the red planet"},   // not in the text
		{3, "over", "done"},             // prose
		{1, "Run,”", "Run quickly"},     // words only: never reworded
		{1, "said Ryoka. She", "x x x"}, // words only, capitalized or not
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
	require.Len(t, got.Segments, 1)
	second := got.Segments[0]
	assert.Equal(t, int64(11), second.SegmentID)
	// "said Ryoka. She had Lv. 5 and 20 HP. The door was open."
	assert.Equal(t, []store.Pause{{Offset: 36, Ms: longPauseMs}}, second.Pauses)
	assert.Equal(t, []store.SpokenForm{
		{Start: 20, End: 25, Say: "level five"},
		{Start: 33, End: 35, Say: "hit points"},
	}, second.SpokenAs)
	assert.Equal(t, []store.LexiconEntry{{Term: "Ryoka", Replacement: "ree-oh-kah", Note: "invented name"}},
		got.Suggestions)
}

func TestSpotOK(t *testing.T) {
	for _, c := range []struct {
		spot, say string
		ok        bool
	}{
		{"Lv.", "level", true},
		{"lv. 5", "level five", true},
		{"HP", "hit points", true},
		{"XP", "experience points", true},
		{"NPC", "N P C", true},
		{"12/20", "twelve out of twenty", true},
		{"[Level 12 → 13]", "level twelve to thirteen", true},
		{"20 HP", "twenty hit points", true},
		{"St.", "Saint", true},
		{"No. 5", "number five", true},
		{"5 km", "five kilometers", true},
		{"approx.", "approximately", true},
		{"e.g.", "for example", true},
		{"Henry VIII", "Henry the Eighth", true},
		{"3:45 p.m.", "three forty-five p m", true},

		{"door", "gate", false},
		{"the old man", "the young man", false},
		{"said Ryoka. She", "x x x", false},
		{"Run,”", "Run quickly", false},
		{"STOP", "go", false},              // shouting, not an acronym
		{"No", "number", false},            // a word without its period
		{"km", "kilometers", false},        // a unit only after a number
		{"Lv. 5 and", "level five", false}, // a word dropped
		{"Lv. 5", "level five and then he died", false},
		{"Henry VIII", "King the Eighth", false},
		{"HP", "hp", false},
		{"HP", "123", false},
		{"", "something", false},
	} {
		assert.Equal(t, c.ok, spotOK([]rune(c.spot), c.say), c.spot+" → "+c.say)
	}
}

func TestSplitParts(t *testing.T) {
	paras := []store.Paragraph{
		{WordCount: 100}, {WordCount: 100}, {Kind: store.ParagraphSceneBreak}, {WordCount: 100}, {WordCount: 500},
	}
	assert.Equal(t, []part{{0, 2}, {2, 4}, {4, 5}}, split(paras, 300))
	assert.Nil(t, split(nil, 300))
}
