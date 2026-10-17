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
	// "Chapter XII", "Book IV:": Roman numbers after a capitalized part's
	// name, at the end or before punctuation or a capitalized word (not
	// "the best part I ever played").
	romanRe = regexp.MustCompile(`\b(Chapter|CHAPTER|Book|BOOK|Part|PART|Volume|VOLUME|Act|ACT|Scene|SCENE|Canto|CANTO|` +
		`Section|SECTION|Interlude|INTERLUDE)[\s\x{00A0}]+([IVXLCDM]+)\b`)
	// What may follow a lone I, V or X to be a number: the end, punctuation
	// or a capitalized word (not "the best part I ever played").
	afterLoneRe = regexp.MustCompile(`^($|[:.,;!?)\-–—]|[\s\x{00A0}]+[^\p{Ll}\s'’])`)
	// Kokoro's pronunciation syntax from the lexicon: [word](/phonemes/).
	phonemeRe = regexp.MustCompile(`\[[^\[\]]*\]\(/[^)]*/\)`)
	// Quotation marks are not spoken (apostrophes stay).
	quoteMarks = strings.NewReplacer("“", "", "”", "", "„", "", "‟", "", "«", "", "»", "", "‹", "", "›", "", "\"", "", "‘", "")
	// Common short words: in capitals they are shouting, not acronyms.
	shortWords = map[string]bool{}
	// Words in capitals: two or more in a row, or one before "!", are
	// shouting, which Kokoro would spell out letter by letter.
	capsWordRe = regexp.MustCompile(`[A-Z][A-Z'’]*`)
	capsRunRe  = regexp.MustCompile(`\b[A-Z][A-Z'’]*(?:[\s,]+[A-Z][A-Z'’]*)+\b!?|\b[A-Z][A-Z'’]+\b!`)
)

// spoken prepares a segment's text for synthesis; Kokoro pronunciations
// from the lexicon are left as they are.
func spoken(s string) string {
	var b strings.Builder
	at := 0
	for _, m := range phonemeRe.FindAllStringIndex(s, -1) {
		b.WriteString(cleanup(s[at:m[0]], at > 0))
		b.WriteString(s[m[0]:m[1]]) // no spaces: "[Ryoka](/…/)’s"
		at = m[1]
	}
	b.WriteString(cleanup(s[at:], at > 0))
	return tidySpaces(b.String())
}

// cleanup rewrites the plain text between pronunciations; afterWord: it
// follows one (an apostrophe at its start is part of a word).
func cleanup(s string, afterWord bool) string {
	s = quoteMarks.Replace(s)
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
	s = romans(s)
	s = singleMarks(s, afterWord)
	return capsRunRe.ReplaceAllStringFunc(s, shout)
}

// romans writes Roman part numbers as numbers ("Chapter XII" → "Chapter
// 12"). A lone I, V or X only before punctuation or a capitalized word; a
// lone other letter is more likely a name ("Part D").
func romans(s string) string {
	var b strings.Builder
	at := 0
	for _, m := range romanRe.FindAllStringSubmatchIndex(s, -1) {
		name, num := s[m[2]:m[3]], s[m[4]:m[5]]
		n := roman(num)
		lone := len(num) == 1
		if n == 0 || lone && num != "I" && num != "V" && num != "X" || lone && !afterLoneRe.MatchString(s[m[1]:]) {
			continue
		}
		b.WriteString(s[at:m[0]])
		b.WriteString(name + " " + itoa(n))
		at = m[1]
	}
	b.WriteString(s[at:])
	return b.String()
}

// singleMarks drops single quotation marks; an apostrophe between letters
// (don’t, rock’n’roll) stays.
func singleMarks(s string, afterWord bool) string {
	r := []rune(s)
	out := r[:0:0]
	for i, c := range r {
		if c == '’' || c == '\'' {
			before := i > 0 && unicode.IsLetter(r[i-1]) || i == 0 && afterWord
			if before && i+1 < len(r) && unicode.IsLetter(r[i+1]) {
				out = append(out, c)
			}
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func tidySpaces(s string) string {
	s = spaceRe.ReplaceAllString(s, " ")
	// Spaces left before punctuation by removed markers.
	s = strings.NewReplacer(" ,", ",", " .", ".", " !", "!", " ?", "?", " ;", ";", " :", ":").Replace(s)
	return strings.TrimSpace(s)
}

func init() {
	for _, w := range strings.Fields("A I AM AN AS AT BE BY DO GO HE IF IN IS IT ME MY NO OF OH OK ON OR SO TO UP US WE " +
		"ALL AND ANY ARE BAD BIG BUT CAN DID END FOR GET GOD GOT HAS HER HEY HIM HIS HOW ITS LET MAN NOT NOW OFF " +
		"ONE OUR OUT RUN SAY SEE SHE THE TOO TWO WAS WAY WHO WHY YES YET YOU") {
		shortWords[w] = true
	}
}

// shout writes shouted capitals as a sentence ("STOP RIGHT THERE" →
// "Stop right there", "THE CIA DID IT!" → "The CIA did it!"). A run is
// shouting when it ends in "!" or has two words that are no acronyms
// (four letters or more, or common short words); acronyms (FBI, BBC TV)
// keep their capitals.
func shout(m string) string {
	words := strings.FieldsFunc(m, func(r rune) bool { return r == ' ' || r == ',' || r == '!' || r == '?' })
	speakable := func(w string) bool {
		return len([]rune(w)) >= 4 || shortWords[strings.Trim(w, "'’")] || strings.ContainsAny(w, "'’")
	}
	n := 0
	for _, w := range words {
		if speakable(w) {
			n++
		}
	}
	excl := strings.HasSuffix(m, "!")
	switch {
	case len(words) == 1 && (!excl || len([]rune(words[0])) < 4):
		return m
	case len(words) > 1 && !excl && n < 2:
		return m
	}
	first := true
	return capsWordRe.ReplaceAllStringFunc(m, func(w string) string {
		if !speakable(w) && len(words) > 1 {
			first = false
			return w // an acronym in a shout
		}
		out := strings.ToLower(w)
		if first || out == "i" || strings.HasPrefix(out, "i'") || strings.HasPrefix(out, "i’") {
			out = strings.ToUpper(out[:1]) + out[1:]
		}
		first = false
		return out
	})
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
