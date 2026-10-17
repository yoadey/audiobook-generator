package render

import (
	"strings"
	"unicode"
)

// timedWord is a word the TTS spoke, at ms in the chapter's audio.
type timedWord struct {
	text string
	ms   int64
}

// token is a word of a text: where it starts (rune offset) and its
// normalized form.
type token struct {
	at   int
	norm string
}

// tokens splits text into words (letters and digits, apostrophes within
// them) for matching: lower case, apostrophes dropped ("Don’t" = "dont").
func tokens(text string) []token {
	r := []rune(text)
	var out []token
	for i := 0; i < len(r); {
		if !unicode.IsLetter(r[i]) && !unicode.IsDigit(r[i]) {
			i++
			continue
		}
		start := i
		var b strings.Builder
		for i < len(r) && (unicode.IsLetter(r[i]) || unicode.IsDigit(r[i]) ||
			(r[i] == '\'' || r[i] == '’') && i+1 < len(r) && unicode.IsLetter(r[i+1])) {
			if r[i] != '\'' && r[i] != '’' {
				b.WriteRune(unicode.ToLower(r[i]))
			}
			i++
		}
		out = append(out, token{start, b.String()})
	}
	return out
}

// lookahead is how many words of the text a spoken word may skip to find
// its match (words read differently: numbers, pronunciations).
const lookahead = 6

// alignWords maps the words the TTS spoke onto a segment's text: a spoken
// word that matches one of the next words of the text gives that word's
// start as [rune offset, ms]. Text words without a match (a number read
// out, a changed pronunciation) get none; the reader keeps the previous.
func alignWords(text string, spoken []timedWord) [][2]int64 {
	words := tokens(text)
	var out [][2]int64
	next := 0
	last := int64(-1)
	for _, w := range spoken {
		for _, t := range tokens(w.text) {
			for k := next; k < len(words) && k < next+lookahead; k++ {
				if words[k].norm != t.norm {
					continue
				}
				ms := max(w.ms, last)
				out = append(out, [2]int64{int64(words[k].at), ms})
				last, next = ms, k+1
				break
			}
		}
	}
	return out
}
