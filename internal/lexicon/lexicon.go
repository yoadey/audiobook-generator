// Package lexicon applies a book's pronunciation entries to text before it
// is spoken: a term is replaced by other text ("Ryoka" → "Ree-oh-ka"), or
// annotated with phonemes in Kokoro's notation ("[Ryoka](/ɹiˈoʊkə/)").
// Terms match whole words, ignoring case; longer terms win.
package lexicon

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Lexicon is a compiled set of entries.
type Lexicon struct {
	re      *regexp.Regexp
	entries map[string]store.LexiconEntry // by lower-case term
}

// Compile prepares entries for Apply (nil when there are none).
func Compile(entries []store.LexiconEntry) *Lexicon {
	if len(entries) == 0 {
		return nil
	}
	l := &Lexicon{entries: map[string]store.LexiconEntry{}}
	var terms []string
	for _, e := range entries {
		t := strings.ToLower(strings.TrimSpace(e.Term))
		if t == "" {
			continue
		}
		if _, ok := l.entries[t]; !ok {
			terms = append(terms, t)
		}
		l.entries[t] = e
	}
	if len(terms) == 0 {
		return nil
	}
	slices.SortFunc(terms, func(a, b string) int { return utf8.RuneCountInString(b) - utf8.RuneCountInString(a) })
	quoted := make([]string, len(terms))
	for i, t := range terms {
		// Spaces in a term match any run of whitespace.
		quoted[i] = strings.Join(strings.Fields(regexp.QuoteMeta(t)), `\s+`)
	}
	l.re = regexp.MustCompile(`(?i)` + strings.Join(quoted, "|"))
	return l
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Apply returns text with every whole-word occurrence of a term replaced.
func (l *Lexicon) Apply(text string) string {
	if l == nil || text == "" {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range l.re.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if start > 0 && isWord(before) || end < len(text) && isWord(after) {
			continue // part of a longer word
		}
		match := text[start:end]
		e := l.entries[strings.ToLower(strings.Join(strings.Fields(match), " "))]
		b.WriteString(text[last:start])
		if e.IsPhonemes {
			b.WriteString("[" + match + "](/" + e.Replacement + "/)")
		} else {
			b.WriteString(e.Replacement)
		}
		last = end
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}
