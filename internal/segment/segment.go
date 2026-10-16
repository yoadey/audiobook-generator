// Package segment splits paragraphs into narration, quoted speech and
// thoughts, and chunks them into TTS-sized pieces. It is deterministic on
// purpose: the LLM later only labels the quotes found here, it never
// rewrites text.
package segment

import (
	"strings"
	"unicode"
)

// Style is the quotation mark convention of a book.
type Style int

// Quote styles.
const (
	CurlyDouble        Style = iota // “…” (most English books)
	StraightDouble                  // "…"
	CurlySingle                     // ‘…’ (British convention)
	StraightSingle                  // '…'
	Guillemets                      // «…»
	GuillemetsReversed              // »…«
	LowHigh                         // „…“
	Dash                            // — dialogue introduced by a dash
)

var styleNames = map[Style]string{
	CurlyDouble: "curly_double", StraightDouble: "straight_double", CurlySingle: "curly_single",
	StraightSingle: "straight_single", Guillemets: "guillemets", GuillemetsReversed: "guillemets_reversed",
	LowHigh: "low_high", Dash: "dash",
}

func (s Style) String() string {
	if n, ok := styleNames[s]; ok {
		return n
	}
	return styleNames[CurlyDouble]
}

// ParseStyle is the inverse of Style.String; unknown values map to CurlyDouble.
func ParseStyle(s string) Style {
	for st, n := range styleNames {
		if n == s {
			return st
		}
	}
	return CurlyDouble
}

// StyleCounter detects a book's quote style from sample text.
type StyleCounter struct {
	n map[Style]int
}

// Add counts the opening marks in one paragraph.
func (c *StyleCounter) Add(text string) {
	if c.n == nil {
		c.n = map[Style]int{}
	}
	runes := []rune(text)
	if startsWithDash(runes) {
		c.n[Dash]++
	}
	for i, r := range runes {
		switch r {
		case '“':
			if opensAfter(runes, i) { // „…“ closes with it
				c.n[CurlyDouble]++
			}
		case '"':
			if opensAfter(runes, i) {
				c.n[StraightDouble]++
			}
		case '‘':
			if opensAfter(runes, i) && !isElision(runes, i) {
				c.n[CurlySingle]++
			}
		case '\'':
			if openSingle(runes, i, '\'') && closesLater(runes, i, '\'') {
				c.n[StraightSingle]++
			}
		case '«':
			if opensAfter(runes, i) {
				c.n[Guillemets]++
			}
		case '»':
			if opensAfter(runes, i) {
				c.n[GuillemetsReversed]++
			}
		case '„':
			c.n[LowHigh]++
		}
	}
}

// Style returns the dominant style (CurlyDouble when there is no evidence).
// Dash dialogue needs clearly more dash paragraphs than quotation marks.
func (c *StyleCounter) Style() Style {
	best, most := CurlyDouble, c.n[CurlyDouble]
	for _, st := range []Style{StraightDouble, CurlySingle, StraightSingle, Guillemets, GuillemetsReversed, LowHigh} {
		if c.n[st] > most {
			best, most = st, c.n[st]
		}
	}
	if c.n[Dash] > 2*most && c.n[Dash] >= 3 {
		return Dash
	}
	return best
}

// Kind of a Part.
type Kind int

// Part kinds.
const (
	Narration Kind = iota
	Quote
	// Thought is a character's thought: a run in italics or, in books
	// whose speech uses double marks, in single ones.
	Thought
)

func (k Kind) String() string {
	switch k {
	case Quote:
		return "quote"
	case Thought:
		return "thought"
	default:
		return "narration"
	}
}

// Span is a run of characters [Start, End) of a text, in runes.
type Span struct {
	Start, End int
}

// Part is a run of narration, one quotation (marks included) or one
// thought.
type Part struct {
	Kind Kind
	// Quote numbers the quotations and thoughts of a chapter from 1; a
	// quotation that continues over several paragraphs keeps its number.
	// 0 for narration.
	Quote int
	Text  string
	// Emphasis lists the italic runs of Text.
	Emphasis []Span
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

// Quotes returns how many quotations and thoughts have been seen so far.
func (s *Segmenter) Quotes() int { return s.quote }

// piece is a part as a range of the paragraph's runes.
type piece struct {
	kind      Kind
	from, to  int
	continued bool // a quotation going on from the previous paragraph
}

// Paragraph splits one paragraph; emph are its italic runs.
func (s *Segmenter) Paragraph(text string, emph ...Span) []Part {
	runes := []rune(text)
	var pieces []piece
	if s.style == Dash {
		pieces = s.dashes(runes)
	} else {
		pieces = s.quotes(runes)
	}
	pieces = s.thoughts(runes, pieces, emph)
	pieces = tidy(runes, pieces)

	parts := make([]Part, 0, len(pieces))
	for i, p := range pieces {
		q := 0
		switch {
		case p.kind == Narration:
		case p.continued && i == 0 && s.quote > 0:
			q = s.quote
		default:
			s.quote++
			q = s.quote
		}
		parts = append(parts, Part{Kind: p.kind, Quote: q, Text: string(runes[p.from:p.to]),
			Emphasis: within(emph, p.from, p.to)})
	}
	return parts
}

// quotes splits at the style's quotation marks.
func (s *Segmenter) quotes(runes []rune) []piece {
	var pieces []piece
	start, i := 0, 0
	inQuote, continued := false, false
	// Multi-paragraph quotation: the previous paragraph had no closing mark
	// and this one starts with an opening mark.
	if s.open {
		j := 0
		for j < len(runes) && unicode.IsSpace(runes[j]) {
			j++
		}
		if j < len(runes) && s.isOpen(runes, j) {
			inQuote, continued = true, true
			start, i = j, j+1
		}
	}
	s.open = false
	for ; i < len(runes); i++ {
		switch {
		case !inQuote && s.isOpen(runes, i):
			pieces = append(pieces, piece{kind: Narration, from: start, to: i})
			start, inQuote = i, true
		case inQuote && s.isClose(runes, i):
			pieces = append(pieces, piece{kind: Quote, from: start, to: i + 1, continued: continued})
			start, inQuote, continued = i+1, false, false
		}
	}
	if inQuote {
		pieces = append(pieces, piece{kind: Quote, from: start, to: len(runes), continued: continued})
		s.open = true
	} else {
		pieces = append(pieces, piece{kind: Narration, from: start, to: len(runes)})
	}
	return pieces
}

// dashes splits dash dialogue: a paragraph opening with a dash is speech
// up to the next dash, then narration up to the next, and so on
// ("—Come in —she said—. Sit down.").
func (s *Segmenter) dashes(runes []rune) []piece {
	s.open = false
	if !startsWithDash(runes) {
		return []piece{{kind: Narration, from: 0, to: len(runes)}}
	}
	var pieces []piece
	kind, start := Quote, 0
	for i := 1; i < len(runes); i++ {
		// A dash between words or digits (well–known, 1990–2000) is no turn.
		turn := unicode.IsSpace(runes[i-1]) || i+1 == len(runes) ||
			unicode.IsSpace(runes[i+1]) || unicode.IsPunct(runes[i+1])
		if !isDash(runes[i]) || !turn {
			continue
		}
		if kind == Quote {
			pieces = append(pieces, piece{kind: Quote, from: start, to: i})
			kind, start = Narration, i
		} else {
			// The punctuation after the closing dash ends the narration.
			for i+1 < len(runes) && unicode.IsPunct(runes[i+1]) && !strings.ContainsRune("¿¡", runes[i+1]) {
				i++
			}
			pieces = append(pieces, piece{kind: Narration, from: start, to: i + 1})
			kind, start = Quote, i+1
		}
	}
	return append(pieces, piece{kind: kind, from: start, to: len(runes)})
}

// thoughts splits thoughts out of the narration: italic runs of a few
// words, and runs in single marks when speech uses double ones.
func (s *Segmenter) thoughts(runes []rune, pieces []piece, emph []Span) []piece {
	var out []piece
	for _, p := range pieces {
		if p.kind != Narration {
			out = append(out, p)
			continue
		}
		var found []Span
		for _, e := range emph {
			e = Span{max(e.Start, p.from), min(e.End, p.to)}
			if e.End > e.Start && isThought(runes[e.Start:e.End]) {
				found = append(found, e)
			}
		}
		found = append(found, s.singleQuoted(runes, p.from, p.to)...)
		found = disjoint(found)
		at := p.from
		for _, f := range found {
			out = append(out, piece{kind: Narration, from: at, to: f.Start}, piece{kind: Thought, from: f.Start, to: f.End})
			at = f.End
		}
		out = append(out, piece{kind: Narration, from: at, to: p.to})
	}
	return out
}

// singleQuoted finds runs in single marks within [from, to) when speech
// uses double marks: thoughts or quoted phrases of more than one word.
func (s *Segmenter) singleQuoted(runes []rune, from, to int) []Span {
	var open, close rune
	switch s.style {
	case CurlyDouble, StraightDouble, LowHigh:
		open, close = '‘', '’'
	case Guillemets, GuillemetsReversed:
		open, close = '‹', '›'
	default:
		return nil
	}
	var out []Span
	for i := from; i < to; i++ {
		if runes[i] != open || !opensAfter(runes, i) || isElision(runes, i) {
			continue
		}
		for j := i + 1; j < to; j++ {
			if runes[j] == close && (j+1 == len(runes) || !unicode.IsLetter(runes[j+1])) {
				if words(runes[i:j+1]) >= 2 {
					out = append(out, Span{i, j + 1})
				}
				i = j
				break
			}
		}
	}
	return out
}

// isThought: an italic run is a thought when it is more than stress on a
// word or two: three words, or two that end a sentence.
func isThought(r []rune) bool {
	n := words(r)
	if n >= 3 {
		return true
	}
	last := []rune(strings.TrimRight(string(r), " ”’\"')"))
	return n == 2 && len(last) > 0 && strings.ContainsRune(".!?…", last[len(last)-1])
}

// disjoint sorts spans and drops those overlapping an earlier one.
func disjoint(spans []Span) []Span {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].Start < spans[j-1].Start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var out []Span
	for _, s := range spans {
		if len(out) == 0 || s.Start >= out[len(out)-1].End {
			out = append(out, s)
		}
	}
	return out
}

// tidy trims pieces of spaces and joins narration without words (only
// punctuation, a dash) to its neighbour, so no character of the text is
// lost.
func tidy(runes []rune, pieces []piece) []piece {
	var out []piece
	pending, pendingTo := -1, -1 // wordless text waiting for a following piece
	for _, p := range pieces {
		for p.from < p.to && unicode.IsSpace(runes[p.from]) {
			p.from++
		}
		for p.to > p.from && unicode.IsSpace(runes[p.to-1]) {
			p.to--
		}
		if p.from == p.to {
			continue
		}
		if p.kind == Narration && !hasWord(string(runes[p.from:p.to])) {
			if len(out) > 0 {
				out[len(out)-1].to = p.to
			} else {
				if pending < 0 {
					pending = p.from
				}
				pendingTo = p.to
			}
			continue
		}
		if pending >= 0 {
			p.from, pending = pending, -1
		}
		out = append(out, p)
	}
	if pending >= 0 {
		// Only punctuation: kept as narration (not spoken).
		out = append(out, piece{kind: Narration, from: pending, to: pendingTo})
	}
	return out
}

// within returns the parts of spans inside [from, to), relative to from.
func within(spans []Span, from, to int) []Span {
	var out []Span
	for _, s := range spans {
		a, b := max(s.Start, from), min(s.End, to)
		if b > a {
			out = append(out, Span{a - from, b - from})
		}
	}
	return out
}

func (s *Segmenter) isOpen(runes []rune, i int) bool {
	switch s.style {
	case StraightDouble:
		return runes[i] == '"' && opensAfter(runes, i)
	case CurlySingle:
		return runes[i] == '‘' && opensAfter(runes, i) && !isElision(runes, i)
	case StraightSingle:
		return openSingle(runes, i, '\'')
	case Guillemets:
		return runes[i] == '«'
	case GuillemetsReversed:
		return runes[i] == '»'
	case LowHigh:
		return runes[i] == '„'
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
	case StraightSingle:
		return runes[i] == '\'' && (i+1 == len(runes) || !unicode.IsLetter(runes[i+1]))
	case Guillemets:
		return runes[i] == '»'
	case GuillemetsReversed:
		return runes[i] == '«'
	case LowHigh:
		return runes[i] == '“'
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
	return unicode.IsSpace(p) || strings.ContainsRune("([{—–-/“\"‘„«»", p)
}

// openSingle: a straight single mark opens a quotation before a word, not
// as an elision ('em, 'tis).
func openSingle(runes []rune, i int, mark rune) bool {
	return runes[i] == mark && opensAfter(runes, i) && i+1 < len(runes) &&
		!unicode.IsSpace(runes[i+1]) && !isElision(runes, i)
}

// closesLater: a closing single mark follows in the same text.
func closesLater(runes []rune, i int, mark rune) bool {
	for j := i + 1; j < len(runes); j++ {
		if runes[j] == mark && !unicode.IsSpace(runes[j-1]) && (j+1 == len(runes) || !unicode.IsLetter(runes[j+1])) {
			return true
		}
	}
	return false
}

func isDash(r rune) bool { return r == '—' || r == '―' || r == '–' }

// startsWithDash: dash dialogue opens a paragraph with a dash and a word.
func startsWithDash(runes []rune) bool {
	i := 0
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i >= len(runes) || !isDash(runes[i]) {
		return false
	}
	i++
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	return i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || strings.ContainsRune("¿¡", runes[i]))
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

// words counts the words of r.
func words(r []rune) int {
	n := 0
	for _, w := range strings.Fields(string(r)) {
		if hasWord(w) {
			n++
		}
	}
	return n
}
