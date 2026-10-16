// Package segment splits paragraphs into narration and quoted speech and
// chunks them into TTS-sized pieces. It is deterministic on purpose: the LLM
// later only labels the quotes found here, it never rewrites text.
package segment

import (
	"strings"
	"unicode"
)

// Style is the quotation mark convention of a book.
type Style int

// Quote styles.
const (
	CurlyDouble    Style = iota // “…” (most English books)
	StraightDouble              // "…"
	CurlySingle                 // ‘…’ (British convention)
)

func (s Style) String() string {
	switch s {
	case StraightDouble:
		return "straight_double"
	case CurlySingle:
		return "curly_single"
	default:
		return "curly_double"
	}
}

// ParseStyle is the inverse of Style.String; unknown values map to CurlyDouble.
func ParseStyle(s string) Style {
	switch s {
	case "straight_double":
		return StraightDouble
	case "curly_single":
		return CurlySingle
	default:
		return CurlyDouble
	}
}

// StyleCounter detects a book's quote style from sample text.
type StyleCounter struct {
	curlyDouble, straightDouble, curlySingle int
}

// Add counts the opening marks in text.
func (c *StyleCounter) Add(text string) {
	runes := []rune(text)
	for i, r := range runes {
		switch r {
		case '“':
			c.curlyDouble++
		case '"':
			if opensAfter(runes, i) {
				c.straightDouble++
			}
		case '‘':
			if opensAfter(runes, i) && !isElision(runes, i) {
				c.curlySingle++
			}
		}
	}
}

// Style returns the dominant style (CurlyDouble when there is no evidence).
func (c *StyleCounter) Style() Style {
	switch {
	case c.curlySingle > c.curlyDouble && c.curlySingle > c.straightDouble:
		return CurlySingle
	case c.straightDouble > c.curlyDouble:
		return StraightDouble
	default:
		return CurlyDouble
	}
}

// Kind of a Part.
type Kind int

// Part kinds.
const (
	Narration Kind = iota
	Quote
)

func (k Kind) String() string {
	if k == Quote {
		return "quote"
	}
	return "narration"
}

// Part is a run of narration or one quotation (marks included).
type Part struct {
	Kind Kind
	// Quote numbers the quotations of a chapter from 1; a quotation that
	// continues over several paragraphs keeps its number. 0 for narration.
	Quote int
	Text  string
}

// Segmenter splits the paragraphs of one chapter, in order. It carries
// state across paragraphs for multi-paragraph quotations.
type Segmenter struct {
	style Style
	open  bool // previous paragraph ended inside a quotation
	quote int  // last quotation number
}

// New returns a Segmenter for one chapter.
func New(style Style) *Segmenter { return &Segmenter{style: style} }

// Break ends any open quotation, e.g. at a scene break.
func (s *Segmenter) Break() { s.open = false }

// Quotes returns how many quotations have been seen so far.
func (s *Segmenter) Quotes() int { return s.quote }

// Paragraph splits one paragraph.
func (s *Segmenter) Paragraph(text string) []Part {
	runes := []rune(text)
	var (
		parts   []Part
		cur     strings.Builder
		inQuote bool
		i       int
	)
	flush := func(kind Kind) {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t == "" || (kind == Narration && !hasWord(t)) {
			return
		}
		q := 0
		if kind == Quote {
			q = s.quote
		}
		parts = append(parts, Part{Kind: kind, Quote: q, Text: t})
	}

	// Multi-paragraph quotation: the previous paragraph had no closing mark
	// and this one starts with an opening mark.
	if s.open {
		j := 0
		for j < len(runes) && unicode.IsSpace(runes[j]) {
			j++
		}
		if j < len(runes) && s.isOpen(runes, j) {
			inQuote = true
			cur.WriteRune(runes[j])
			i = j + 1
		}
	}
	s.open = false

	for ; i < len(runes); i++ {
		r := runes[i]
		switch {
		case !inQuote && s.isOpen(runes, i):
			flush(Narration)
			s.quote++
			inQuote = true
			cur.WriteRune(r)
		case inQuote && s.isClose(runes, i):
			cur.WriteRune(r)
			flush(Quote)
			inQuote = false
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote {
		flush(Quote)
		s.open = true
	} else {
		flush(Narration)
	}
	return parts
}

func (s *Segmenter) isOpen(runes []rune, i int) bool {
	switch s.style {
	case StraightDouble:
		return runes[i] == '"' && opensAfter(runes, i)
	case CurlySingle:
		return runes[i] == '‘' && opensAfter(runes, i) && !isElision(runes, i)
	default:
		return runes[i] == '“'
	}
}

func (s *Segmenter) isClose(runes []rune, i int) bool {
	switch s.style {
	case StraightDouble:
		return runes[i] == '"'
	case CurlySingle:
		// ’ is also the apostrophe: only a mark not followed by a letter
		// ("don’t" vs. “’Tis late,’ she said”) closes.
		return runes[i] == '’' && (i+1 == len(runes) || !unicode.IsLetter(runes[i+1]))
	default:
		return runes[i] == '”'
	}
}

// opensAfter: an opening mark is at the start or after space/brackets/dashes.
func opensAfter(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	p := runes[i-1]
	return unicode.IsSpace(p) || strings.ContainsRune("([{—–-/“\"‘", p)
}

// Common elisions written with a leading ‘ (‘em, ‘tis) are not quotes.
var elisions = []string{"em", "tis", "twas", "til", "till", "cause", "round", "n", "ere", "neath", "bout", "e"}

func isElision(runes []rune, i int) bool {
	j := i + 1
	for j < len(runes) && unicode.IsLetter(runes[j]) {
		j++
	}
	word := strings.ToLower(string(runes[i+1 : j]))
	for _, e := range elisions {
		if word == e {
			return true
		}
	}
	return false
}

func hasWord(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}
