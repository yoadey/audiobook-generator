package casting

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/tts"
)

// Part is one base voice of a recipe.
type Part struct {
	Voice  string
	Weight float64
}

// Recipe is a weighted blend of base voices; weights sum to 1.
type Recipe []Part

// MaxParts bounds the base voices of a recipe, maxRecipeLen its text.
const (
	MaxParts     = 4
	maxRecipeLen = 200
)

var partRe = regexp.MustCompile(`^([a-z]{2}_[a-z0-9]+)(?:\(([0-9]+(?:\.[0-9]+)?)\))?$`)

// ErrRecipe describes a malformed recipe.
var ErrRecipe = errors.New(`a voice must look like "af_heart", a mix like "bf_emma(0.7)+af_bella(0.3)" ` +
	`or a voice of another engine like "chatterbox:Emily.wav"`)

// ParseRecipe reads Kokoro's mix syntax ("af_heart",
// "bf_emma(2)+af_bella(1)"); weights are normalized, repeated voices
// added up. A voice of another engine ("chatterbox:Emily.wav") stands
// alone: engines' voices do not mix.
func ParseRecipe(s string) (Recipe, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxRecipeLen {
		return nil, ErrRecipe
	}
	if strings.Contains(s, ":") {
		if _, _, ok := tts.SplitVoice(s); !ok {
			return nil, ErrRecipe
		}
		return Recipe{{Voice: s, Weight: 1}}, nil
	}
	var r Recipe
	total := 0.0
	for _, raw := range strings.Split(s, "+") {
		m := partRe.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			return nil, ErrRecipe
		}
		w := 1.0
		if m[2] != "" {
			var err error
			if w, err = strconv.ParseFloat(m[2], 64); err != nil || w <= 0 || w > 100 {
				return nil, ErrRecipe
			}
		}
		total += w
		if i := slices.IndexFunc(r, func(p Part) bool { return p.Voice == m[1] }); i >= 0 {
			r[i].Weight += w
		} else {
			r = append(r, Part{Voice: m[1], Weight: w})
		}
	}
	if len(r) > MaxParts {
		return nil, fmt.Errorf("%w (at most %d voices)", ErrRecipe, MaxParts)
	}
	for i := range r {
		r[i].Weight /= total
	}
	return r.normalized(), nil
}

// normalized sorts parts by weight (heaviest first) and rounds weights to
// hundredths that still sum to 1.
func (r Recipe) normalized() Recipe {
	out := slices.Clone(r)
	slices.SortStableFunc(out, byWeight)
	sum := 0.0
	for i := range out {
		out[i].Weight = math.Round(out[i].Weight*100) / 100
		sum += out[i].Weight
	}
	out = slices.DeleteFunc(out, func(p Part) bool { return p.Weight <= 0 })
	if len(out) > 0 {
		out[0].Weight = math.Round((out[0].Weight+1-sum)*100) / 100
		// The correction may reorder near-equal parts.
		slices.SortStableFunc(out, byWeight)
	}
	return out
}

func byWeight(a, b Part) int {
	if a.Weight != b.Weight {
		if a.Weight > b.Weight {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Voice, b.Voice)
}

// String formats the recipe for Kokoro ("af_heart",
// "bf_emma(0.7)+af_bella(0.3)").
func (r Recipe) String() string {
	if len(r) == 1 {
		return r[0].Voice
	}
	parts := make([]string, len(r))
	for i, p := range r {
		parts[i] = p.Voice + "(" + strconv.FormatFloat(p.Weight, 'f', -1, 64) + ")"
	}
	return strings.Join(parts, "+")
}

// Main returns the heaviest base voice.
func (r Recipe) Main() string {
	if len(r) == 0 {
		return ""
	}
	return r[0].Voice
}

func (r Recipe) weight(voice string) float64 {
	for _, p := range r {
		if p.Voice == voice {
			return p.Weight
		}
	}
	return 0
}

// maxSpeedShare caps what a speed difference adds to Distance: a voice
// spoken faster is still recognisably the same voice.
const maxSpeedShare = 0.15

// Distance tells how differently two voices sound, roughly: the share of
// blend weight they do not have in common (0 same blend, 1 nothing in
// common) plus their difference in speed (at most maxSpeedShare).
func Distance(a Recipe, aSpeed float64, b Recipe, bSpeed float64) float64 {
	shared := 0.0
	for _, p := range a {
		shared += min(p.Weight, b.weight(p.Voice))
	}
	return 1 - shared + min(math.Abs(aSpeed-bSpeed), maxSpeedShare)
}
