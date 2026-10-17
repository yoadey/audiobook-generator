// Package lexicon applies a book's pronunciation entries to text before it
// is spoken: a term is replaced by other text ("Ryoka" → "Ree-oh-ka"), or
// annotated with phonemes in Kokoro's notation ("[Ryoka](/ɹiˈoʊkə/)").
// Terms match whole words, ignoring case; longer terms win.
package lexicon

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Lexicon is a compiled set of entries.
type Lexicon struct {
	any   *regexp.Regexp   // finds where some term may start
	terms []*regexp.Regexp // each term anchored at the start, longest first
	entry []store.LexiconEntry
}

// Compile prepares entries for Apply (nil when there are none).
func Compile(entries []store.LexiconEntry) *Lexicon {
	seen := map[string]bool{}
	var es []store.LexiconEntry
	for _, e := range entries {
		key := strings.ToLower(strings.Join(strings.Fields(e.Term), " "))
		// Suggestions apply once accepted.
		if key == "" || seen[key] || e.Status != store.LexiconActive {
			continue
		}
		seen[key] = true
		es = append(es, e)
	}
	if len(es) == 0 {
		return nil
	}
	slices.SortStableFunc(es, func(a, b store.LexiconEntry) int {
		return utf8.RuneCountInString(b.Term) - utf8.RuneCountInString(a.Term)
	})
	l := &Lexicon{entry: es}
	quoted := make([]string, len(es))
	for i, e := range es {
		// Spaces in a term match any run of whitespace.
		quoted[i] = strings.Join(strings.Fields(regexp.QuoteMeta(e.Term)), `\s+`)
		l.terms = append(l.terms, regexp.MustCompile(`^(?i:`+quoted[i]+`)`))
	}
	l.any = regexp.MustCompile(`(?i)` + strings.Join(quoted, "|"))
	return l
}

// isWord reports whether r continues a word (combining marks included, so
// a decomposed "á" is not cut off its letter).
func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc)
}

// Apply returns text with every whole-word occurrence of a term replaced;
// where a longer term is no whole word ("Ryoka Griffins"), a shorter one
// may still be ("Ryoka").
func (l *Lexicon) Apply(text string) string {
	if l == nil || text == "" {
		return text
	}
	var b strings.Builder
	last, pos := 0, 0
	for pos < len(text) {
		m := l.any.FindStringIndex(text[pos:])
		if m == nil {
			break
		}
		start := pos + m[0]
		k, end := l.match(text, start)
		if k < 0 {
			_, size := utf8.DecodeRuneInString(text[start:])
			pos = start + max(size, 1)
			continue
		}
		e := l.entry[k]
		b.WriteString(text[last:start])
		if e.IsPhonemes {
			b.WriteString("[" + text[start:end] + "](/" + e.Replacement + "/)")
		} else {
			b.WriteString(e.Replacement)
		}
		last, pos = end, end
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// Matches reports whether any term occurs in text as a whole word.
func (l *Lexicon) Matches(text string) bool {
	if l == nil {
		return false
	}
	for pos := 0; pos < len(text); {
		m := l.any.FindStringIndex(text[pos:])
		if m == nil {
			return false
		}
		start := pos + m[0]
		if k, _ := l.match(text, start); k >= 0 {
			return true
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		pos = start + max(size, 1)
	}
	return false
}

// match returns the longest term that is a whole word at start, and its
// end (-1 when none is).
func (l *Lexicon) match(text string, start int) (int, int) {
	if before, _ := utf8.DecodeLastRuneInString(text[:start]); start > 0 && isWord(before) {
		return -1, 0
	}
	for k, re := range l.terms {
		m := re.FindStringIndex(text[start:])
		if m == nil || m[1] == 0 {
			continue
		}
		end := start + m[1]
		if after, _ := utf8.DecodeRuneInString(text[end:]); end < len(text) && isWord(after) {
			continue // part of a longer word
		}
		return k, end
	}
	return -1, 0
}

// Normalize collapses the whitespace of a term and trims a replacement.
func Normalize(term, replacement string) (string, string) {
	return strings.Join(strings.Fields(term), " "), strings.TrimSpace(replacement)
}

// Validate checks a normalized entry. Phonemes go into Kokoro's
// "[word](/phonemes/)" notation, so they must not contain its brackets,
// and a text replacement must not start one.
func Validate(term, replacement string, phonemes bool) error {
	switch {
	case term == "" || utf8.RuneCountInString(term) > 100:
		return errors.New("term must have 1 to 100 characters")
	case strings.IndexFunc(term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0:
		return errors.New("term must contain letters or digits")
	case replacement == "" || utf8.RuneCountInString(replacement) > 200:
		return errors.New("replacement must have 1 to 200 characters")
	case phonemes && strings.ContainsAny(replacement, "[]()/"):
		return errors.New("phonemes must not contain [ ] ( ) or /")
	case !phonemes && strings.ContainsAny(replacement, "[]"):
		return errors.New("replacement must not contain [ or ]")
	}
	return nil
}
