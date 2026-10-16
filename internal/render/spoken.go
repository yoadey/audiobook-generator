package render

import (
	"regexp"
	"strings"
	"unicode"
)

// Text cleanup before synthesis. The book text stays as it is (the reader
// shows it); only what Kokoro is given changes, where its reading would
// otherwise trip over typography.
var (
	// Footnote and reference markers: [1], [12a].
	refMarkRe = regexp.MustCompile(`\[\d+[a-z]?\]`)
	// Brackets of game texts and system messages ([Skill: Fireball]).
	bracketRe = regexp.MustCompile(`[\[\]{}<>]`)
	// Decoration that is not read: *, _, ~, |, and # not before a number.
	decorRe  = regexp.MustCompile(`[*_~|]+|#(?:[^0-9]|$)`)
	numberRe = regexp.MustCompile(`#(\d)`)
	dashesRe = regexp.MustCompile(`-{2,}`)
	dotsRe   = regexp.MustCompile(`\.{3,}`)
	spaceRe  = regexp.MustCompile(`\s{2,}`)
	// "Chapter XII", "Book IV": Roman numbers after a part's name.
	romanRe = regexp.MustCompile(`\b(?i:(chapter|book|part|volume|act|scene|canto|section|interlude))\s+([IVXLCDM]+)\b`)
	// Words in capitals: two or more in a row, or one before "!", are
	// shouting, which Kokoro would spell out letter by letter.
	pronounIRe = regexp.MustCompile(`\bi\b`)
	capsRunRe  = regexp.MustCompile(`\b[A-Z][A-Z'’]*(?:[\s,]+[A-Z][A-Z'’]*)+\b|\b[A-Z][A-Z'’]+\b(?:!)`)
)

// spoken prepares a segment's text for synthesis.
func spoken(s string) string {
	s = refMarkRe.ReplaceAllString(s, "")
	s = bracketRe.ReplaceAllString(s, " ")
	s = numberRe.ReplaceAllString(s, "number $1")
	s = decorRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "#") {
			return " " + m[1:]
		}
		return " "
	})
	s = strings.ReplaceAll(s, "&", " and ")
	s = dashesRe.ReplaceAllString(s, "—")
	s = dotsRe.ReplaceAllString(s, "…")
	s = romanRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := romanRe.FindStringSubmatch(m)
		// A lone letter other than I, V, X is more likely a name ("Part D").
		if n := roman(sub[2]); n > 0 && (len(sub[2]) > 1 || sub[2] == "I" || sub[2] == "V" || sub[2] == "X") {
			return sub[1] + " " + itoa(n)
		}
		return m
	})
	s = capsRunRe.ReplaceAllStringFunc(s, shout)
	s = spaceRe.ReplaceAllString(s, " ")
	// Spaces left before punctuation by removed markers.
	s = strings.NewReplacer(" ,", ",", " .", ".", " !", "!", " ?", "?", " ;", ";", " :", ":").Replace(s)
	return strings.TrimSpace(s)
}

// shout writes capitals as a sentence ("STOP RIGHT THERE" → "Stop right
// there"), keeping "I" and short acronyms of a single word alone.
func shout(m string) string {
	long := 0
	for _, w := range strings.FieldsFunc(m, func(r rune) bool { return r == ' ' || r == ',' || r == '!' }) {
		if len([]rune(w)) >= 2 {
			long++
		}
	}
	single := !strings.ContainsAny(strings.TrimSpace(m), " ,")
	switch {
	case single && len([]rune(strings.TrimRight(m, "!"))) <= 2:
		return m // "OK!", "NO!" are read fine
	case !single && long < 2:
		return m // "I A" and the like: no shouting
	}
	lower := []rune(strings.ToLower(m))
	lower[0] = unicode.ToUpper(lower[0])
	out := string(lower)
	return pronounIRe.ReplaceAllString(out, "I")
}

// roman converts a Roman number (0 if malformed or implausible).
func roman(s string) int {
	vals := map[rune]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total, prev := 0, 0
	r := []rune(strings.ToUpper(s))
	for i := len(r) - 1; i >= 0; i-- {
		v := vals[r[i]]
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	if total <= 0 || total > 3999 || toRoman(total) != strings.ToUpper(s) {
		return 0
	}
	return total
}

func toRoman(n int) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var b strings.Builder
	for i, v := range vals {
		for n >= v {
			b.WriteString(syms[i])
			n -= v
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
