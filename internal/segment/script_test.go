package segment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocate(t *testing.T) {
	text := "“Good morning,” said Anna. “Is it raining?” Tom shrugged… “No—not now.”"
	for _, c := range []struct {
		start, end string
		from       int
		want       string
		ok         bool
	}{
		{"Good morning,", "Good morning,", 0, "“Good morning,”", true},
		// Straight quotes, other case and spaces still match; the marks
		// around are taken along.
		{`"is it`, `raining?"`, 0, "“Is it raining?”", true},
		{"Good morning", "Good morning", 0, "“Good morning,”", true}, // the comma before the mark too
		{"No-not now.", "No-not now.", 0, "“No—not now.”", true},
		{"Tom   shrugged...", "Tom shrugged...", 0, "Tom shrugged…", true},
		{"*Is it", "raining?*", 0, "“Is it raining?”", true},
		{"Good morning,", "Good morning,", 5, "", false},  // before from
		{"Is it raining?", "Good morning,", 0, "", false}, // end before start
		{"", "x", 0, "", false},
		{"snowing", "snowing", 0, "", false},
	} {
		sp, ok := Locate(text, c.from, c.start, c.end)
		assert.Equal(t, c.ok, ok, "%q", c.start)
		if ok {
			assert.Equal(t, c.want, string([]rune(text)[sp.Start:sp.End]), "%q", c.start)
		}
	}
	// An apostrophe after a word is no closing mark.
	sp, ok := Locate("He said it was Tom’s idea.", 0, "it was Tom", "it was Tom")
	assert.True(t, ok)
	assert.Equal(t, "it was Tom", string([]rune("He said it was Tom’s idea.")[sp.Start:sp.End]))
}

func TestScript(t *testing.T) {
	text := "“Hi,” she said. I should go, she thought."
	hi, _ := Locate(text, 0, "Hi,", "Hi,")
	thought, _ := Locate(text, 0, "I should go,", "I should go,")
	parts := Script(text, []Span{{16, 28}}, []Voiced{{hi, Quote}, {thought, Thought}}, 1)
	assert.Equal(t, []Part{
		{Kind: Quote, Quote: 1, Text: "“Hi,”"},
		{Kind: Narration, Text: "she said."},
		{Kind: Thought, Quote: 2, Text: "I should go,", Emphasis: []Span{{0, 12}}},
		{Kind: Narration, Text: "she thought."},
	}, parts)

	// Wordless narration joins a neighbour; nothing is lost.
	text = "— “Wait!”"
	w, _ := Locate(text, 0, "Wait!", "Wait!")
	assert.Equal(t, []Part{{Kind: Quote, Quote: 1, Text: "— “Wait!”"}}, Script(text, nil, []Voiced{{w, Quote}}, 1))

	// Overlapping or empty spans are left out.
	parts = Script("abc def", nil, []Voiced{{Span{0, 3}, Quote}, {Span{2, 5}, Quote}, {Span{6, 6}, Quote}}, 1)
	assert.Equal(t, []Part{{Kind: Quote, Quote: 1, Text: "abc"}, {Kind: Narration, Text: "def"}}, parts)
}
