package lexicon

import (
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
	var none *Lexicon
	assert.Equal(t, "x", none.Apply("x"))
	assert.Nil(t, Compile(nil))
}
