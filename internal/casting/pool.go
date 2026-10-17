package casting

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// AgeSpeed is the speaking speed that suits an age group.
func AgeSpeed(a store.AgeGroup) float64 {
	switch a {
	case store.AgeChild:
		return 1.08
	case store.AgeTeen:
		return 1.04
	case store.AgeElderly:
		return 0.92
	}
	return 1
}

// ageHints are timbre words of the catalogue that suit an age group.
var ageHints = map[store.AgeGroup][]string{
	store.AgeChild:   {"young", "youthful", "light", "airy"},
	store.AgeTeen:    {"young", "youthful", "light", "playful"},
	store.AgeElderly: {"old", "mature", "deep", "gravelly"},
}

func suits(v BaseVoice, age store.AgeGroup) bool {
	for _, h := range ageHints[age] {
		if strings.Contains(v.Traits, h) {
			return true
		}
	}
	return false
}

// genderFits reports whether a base voice may voice a character of gender
// g (any voice for an unknown gender).
func genderFits(v BaseVoice, g store.Gender) bool {
	return g != store.GenderFemale && g != store.GenderMale || v.Gender == g
}

// narratorShare is the narrator weight above which a base voice is kept
// for the narrator.
const narratorShare = 0.3

// PoolVoice picks a minor character's voice: a castable base voice of its
// gender and, where possible, suited to its age, that is not a main part
// of the narrator's voice. key (the character's ID) picks among them by
// hash, so a character always gets the same voice as long as the
// available voices and the narrator stay the same. nil when no voice fits.
func PoolVoice(voices []BaseVoice, narrator Recipe, g store.Gender, age store.AgeGroup, key string) (Recipe, float64) {
	var fit, suited []BaseVoice
	for _, v := range Castable(voices) {
		if !genderFits(v, g) || narrator.weight(v.ID) >= narratorShare {
			continue
		}
		fit = append(fit, v)
		if suits(v, age) {
			suited = append(suited, v)
		}
	}
	if len(suited) > 0 {
		fit = suited
	}
	if len(fit) == 0 {
		return nil, 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	v := fit[h.Sum64()%uint64(len(fit))]
	if !mixable(fit) {
		return Recipe{{Voice: v.ID, Weight: 1}}, 1 // other engines keep their pace
	}
	return Recipe{{Voice: v.ID, Weight: 1}}, AgeSpeed(age)
}

// Source tells where a character's voice comes from.
type Source string

// Voice sources.
const (
	SourceOwn      Source = "own"      // the character's recipe (cast or chosen)
	SourcePool     Source = "pool"     // a pool voice for its gender and age
	SourceNarrator Source = "narrator" // nothing else fits
)

// Voice is how a character is voiced.
type Voice struct {
	Recipe string
	Speed  float64
	Source Source
}

// Effective returns the voice a character speaks with. narrator is the
// book's effective narrator recipe and speed.
func Effective(c *store.Character, voices []BaseVoice, narrator string, narratorSpeed float64) Voice {
	if c.VoiceRecipe != "" {
		speed := c.VoiceSpeed
		if speed <= 0 {
			speed = 1
		}
		return Voice{Recipe: c.VoiceRecipe, Speed: speed, Source: SourceOwn}
	}
	nr, _ := ParseRecipe(narrator)
	if r, speed := PoolVoice(voices, nr, c.Gender, c.AgeGroup, strconv.FormatInt(c.ID, 10)); r != nil {
		return Voice{Recipe: r.String(), Speed: speed, Source: SourcePool}
	}
	return Voice{Recipe: narrator, Speed: narratorSpeed, Source: SourceNarrator}
}
