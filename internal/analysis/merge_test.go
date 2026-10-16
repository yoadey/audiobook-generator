package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func TestCandidates(t *testing.T) {
	chars := []store.Character{
		{ID: 1, Name: "Erin Solstice", Gender: store.GenderFemale, Importance: 5},
		{ID: 2, Name: "Erin", Gender: store.GenderFemale, Importance: 2},
		{ID: 3, Name: "Mr. Bennet", Gender: store.GenderMale},
		{ID: 4, Name: "Mrs. Bennet", Gender: store.GenderFemale},
		{ID: 5, Name: "Guard"},
		{ID: 6, Name: "Captain Guard"},
		{ID: 7, Name: "Klbkch", Gender: store.GenderMale},
		{ID: 8, Name: "Captain Zel Shivertail", Gender: store.GenderMale},
		{ID: 9, Name: "Zel"},
		{ID: 10, Name: "Lyonette du Marquin", Gender: store.GenderFemale},
	}
	aliases := []store.CharacterAlias{
		{CharacterID: 7, Alias: "Klb", Norm: "klb"},
		{CharacterID: 10, Alias: "Lyonette", Norm: "lyonette"},
		{CharacterID: 2, Alias: "the girl", Norm: "girl"},
		{CharacterID: 5, Alias: "the girl", Norm: "girl"},
	}
	distinct := map[[2]int64]bool{{8, 9}: true}

	pairs, conflicting, more := candidates(chars, aliases, distinct, 100)
	assert.False(t, more)
	assert.ElementsMatch(t, []candidate{{1, 2}}, pairs,
		"first name vs full name; generic words, aliases of different people and known distinct pairs are not candidates")
	assert.ElementsMatch(t, []candidate{{3, 4}}, conflicting, "Mr. and Mrs. Bennet differ by gender")

	pairs, _, _ = candidates(chars, aliases, nil, 100)
	assert.Contains(t, pairs, candidate{8, 9})
	pairs, _, more = candidates(chars, aliases, nil, 1)
	assert.True(t, more)
	assert.Equal(t, []candidate{{1, 2}}, pairs, "most important first when limited")
}

func TestNameHelpers(t *testing.T) {
	assert.Equal(t, "smith", stripTitles("captain smith"))
	assert.Equal(t, "zel shivertail", stripTitles("captain zel shivertail"))
	assert.Equal(t, "captain", stripTitles("captain"))
	assert.True(t, properName("Erin", "erin"))
	assert.True(t, properName("[Innkeeper] Erin", "innkeeper erin"))
	assert.False(t, properName("the girl", "girl"))
	assert.False(t, properName("Mom", "mom"))
	assert.True(t, properName("Al", "al"))
	assert.False(t, properName("Ma", "ma"))
}

func TestRelatedNeedsANameNotADescription(t *testing.T) {
	b := newBible()
	b.add(&store.Character{ID: 1, Name: "Erin Solstice"})
	b.addName(1, "the girl")
	b.addName(1, "erin")
	assert.True(t, b.related(1, "Erin Solstice", nil))
	assert.True(t, b.related(1, "Innkeeper", []string{"Erin"}))
	assert.True(t, b.related(1, "Erin", nil))
	assert.False(t, b.related(1, "the girl", nil), "a shared description proves nothing")
	assert.False(t, b.related(1, "Lyonette", []string{"the girl"}))
}

func TestRelevantCharacters(t *testing.T) {
	b := newBible()
	b.add(&store.Character{ID: 1, Name: "Erin Solstice", Importance: 5})
	b.add(&store.Character{ID: 2, Name: "Ryoka Griffin", Importance: 4})
	b.add(&store.Character{ID: 3, Name: "Pisces", Importance: 3})
	b.addName(2, "runner")
	got := b.relevant("“Hello,” Erin said to the Runner. Solstice was far away.", 10)
	var ids []int64
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []int64{1, 2}, ids, "all words of a name or alias must occur; most important first")

	b.add(&store.Character{ID: 4, Name: "O’Brien", Importance: 1})
	got = b.relevant("Pisces' hat. O'Brien and Pisces's friend.", 10)
	ids = nil
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []int64{3, 4}, ids, "possessives and curly apostrophes match")
	assert.Len(t, b.relevant("Erin Solstice met Ryoka Griffin", 1), 1)

	assert.EqualValues(t, 3, b.match("pisces", nil))
	b.addName(2, "ryoka")
	b.add(&store.Character{ID: 5, Name: "Ryoka's sister"})
	assert.EqualValues(t, 2, b.match("Miss Griffin", []string{"Ryoka"}), "matched by a name-like alias")
	assert.Zero(t, b.match("Somebody", []string{"the Runner"}), "descriptions do not identify")
	assert.True(t, identifying("Erin"))
	assert.False(t, identifying("The Innkeeper"))
	assert.False(t, identifying("Mom"))
	b.add(&store.Character{ID: 6, Name: "Lady of the Lake"})
	assert.False(t, b.related(6, "Head of Security", nil), "filler words do not relate names")
	assert.True(t, b.related(6, "Lady of the Lake", nil))
	b.add(&store.Character{ID: 7, Name: "Elizabeth Bennet"})
	assert.False(t, b.related(7, "Jane Bennet", nil), "a shared surname is not enough")
	assert.True(t, b.related(7, "Elizabeth", nil))
	assert.True(t, b.related(7, "Miss Elizabeth Bennet", nil))
	b.add(&store.Character{ID: 8, Name: "Relc"})
	b.addName(8, "old guard")
	assert.Zero(t, b.match("the old guard", nil), "a description as name does not match an alias")
	assert.EqualValues(t, 8, b.match("Relc", nil))
	b.add(&store.Character{ID: 9, Name: "Li"})
	assert.True(t, b.related(9, "Li Wei", nil), "two-letter given names")
	assert.Zero(t, b.match("Unknown", nil))
	assert.EqualValues(t, 12, parseRef(" c12 "))
	assert.Zero(t, parseRef("nope"))
}
