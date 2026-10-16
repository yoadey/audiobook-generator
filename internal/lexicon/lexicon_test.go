package lexicon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func TestApply(t *testing.T) {
	l := Compile([]store.LexiconEntry{
		{Term: "Ryoka", Replacement: "Ree-oh-ka"},
		{Term: "Erin Solstice", Replacement: "ˈɛɹɪn ˈsɑlstɪs", IsPhonemes: true},
		{Term: "Érin", Replacement: "Air-in"},
	})
	assert.Equal(t, "Ree-oh-ka ran. Ree-oh-ka's shoes!", l.Apply("Ryoka ran. RYOKA's shoes!"))
	assert.Equal(t, "Ryokas and ARyoka stay", l.Apply("Ryokas and ARyoka stay"), "whole words only")
	assert.Equal(t, "Hi [Erin  Solstice](/ˈɛɹɪn ˈsɑlstɪs/).", l.Apply("Hi Erin  Solstice."))
	assert.Equal(t, "Air-in", l.Apply("Érin"))
	assert.Equal(t, "nothing here", l.Apply("nothing here"))
	assert.True(t, l.Matches("say ryoka"))
	assert.True(t, Compile([]store.LexiconEntry{{Term: "Ryoka", Replacement: "Ryoka"}}).Matches("Ryoka"), "identity replacement")
	assert.False(t, l.Matches("Ryokas"))
	var none *Lexicon
	assert.Equal(t, "x", none.Apply("x"))
	assert.Nil(t, Compile(nil))
}

func TestApplyEdgeCases(t *testing.T) {
	l := Compile([]store.LexiconEntry{
		{Term: "Ryoka", Replacement: "R"},
		{Term: "Ryoka Griffin", Replacement: "RG"},
		{Term: "Asa", Replacement: "A"},
	})
	// A longer term that is no whole word leaves the shorter one.
	assert.Equal(t, "R Griffins", l.Apply("Ryoka Griffins"))
	assert.Equal(t, "RG.", l.Apply("Ryoka Griffin."))
	// Case folding the regexp knows (long s) never deletes text.
	assert.Equal(t, "A went.", l.Apply("Aſa went."))
	// A combining accent belongs to the word.
	assert.Equal(t, "Ryoka\u0301 ran", l.Apply("Ryoka\u0301 ran"))
	// Adjacent occurrences.
	assert.Equal(t, "R R", l.Apply("Ryoka ryoka"))
}

func TestValidate(t *testing.T) {
	assert.NoError(t, Validate("Ryoka", "Ree-oh-ka", false))
	assert.Error(t, Validate("", "x", false))
	assert.Error(t, Validate("!!", "x", false))
	assert.Error(t, Validate("a", "[x]", false))
	assert.Error(t, Validate("a", "x/y", true))
	assert.Error(t, Validate(strings.Repeat("a", 101), "x", false))
}
