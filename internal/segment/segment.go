// Package segment splits paragraphs into narration, quoted speech and
// thoughts, and chunks them into TTS-sized pieces. It is deterministic on
// purpose: the LLM later only labels the quotes found here, it never
// rewrites text.
package segment

import (
	"strings"
	"unicode"
	"unicode/utf8"
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
	// Epigraph attributions ("—Mark Twain") also open with a dash.
	if c.n[Dash] > 2*most && c.n[Dash] >= 10 {
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
	num       int  // the number Script gives a voiced piece
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

// thoughts splits thoughts out of the narration: italic runs that read
// like a thought (see isThought), and runs in single marks when speech
// uses double ones.
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
			if e.End > e.Start && isThought(runes, e) {
				found = append(found, Span{e.Start, swallowPunct(runes, e.End, p.to)})
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

// maxThoughtWords: longer runs in italics (letters, flashbacks, whole
// italic paragraphs) stay narration.
const maxThoughtWords = 40

// thoughtTags name a thought: "*I hate this place*, she thought". The
// reflexive tags only with a reflexive pronoun ("she told herself").
var (
	thoughtTags = map[string]bool{
		"thought": true, "thinks": true, "think": true, "wondered": true, "wonders": true, "mused": true,
		"muses": true, "realized": true, "realised": true, "hoped": true, "wished": true, "reflected": true,
		"considered": true, "prayed": true,
	}
	reflexiveTags = map[string]bool{"told": true, "tells": true, "reminded": true, "reminds": true, "asked": true}
	reflexives    = map[string]bool{
		"himself": true, "herself": true, "themselves": true, "myself": true, "yourself": true, "ourselves": true,
		"itself": true,
	}
	tagSubjects = map[string]bool{"he": true, "she": true, "i": true, "they": true, "we": true, "you": true, "it": true}
)

// isThought: an italic run is a thought when it reads like one: it starts
// a sentence or clause (a capital, or any letter after a dash, a colon or
// a thought tag), has two to maxThoughtWords words, and ends a clause
// itself or is followed by a tag like "she thought". Stress on words ("she
// had *never in her life* seen") and titles ("*Queen Mary II*") are not.
func isThought(runes []rune, e Span) bool {
	r := runes[e.Start:e.End]
	n := words(r)
	if n < 2 || n > maxThoughtWords || !startsCapital(r) && !opensClause(runes, e.Start) {
		return false
	}
	return endsClause(r) || taggedAfter(runes, e.End)
}

// taggedAfter: the text after a run starts with a thought tag: "[,] she
// thought", "Erin wondered", "he told himself".
func taggedAfter(runes []rune, end int) bool {
	rest := []rune(strings.TrimLeft(string(runes[end:min(len(runes), end+60)]), ",;:—– ”’\"'»›"))
	w := tagWords(rest, 4)
	if len(w) < 2 {
		return false
	}
	// The subject: a pronoun or a name of one or two capitalized words.
	k := 1
	switch {
	case tagSubjects[strings.ToLower(w[0])]:
	case isName(w[0]) && len(w) > 2 && isName(w[1]) && !isTag(w[1], ""):
		k = 2
	case isName(w[0]):
	default:
		return false
	}
	next := ""
	if k+1 < len(w) {
		next = w[k+1]
	}
	return isTag(w[k], next)
}

// taggedBefore: a thought tag right before start ("she thought, *what
// now*"), so the thought may start in lower case.
func taggedBefore(runes []rune, start int) bool {
	before := strings.TrimRight(string(runes[max(0, start-40):start]), " ,:—–“‘\"'«‹")
	w := strings.Fields(before)
	if len(w) < 2 {
		return false
	}
	last := strings.ToLower(strings.TrimFunc(w[len(w)-1], func(c rune) bool { return !unicode.IsLetter(c) }))
	if reflexives[last] && len(w) >= 3 {
		return isTag(w[len(w)-2], last)
	}
	return isTag(w[len(w)-1], "")
}

// opensClause: the text before start ends with a dash or colon, or with a
// thought tag, where a thought may start in lower case.
func opensClause(runes []rune, start int) bool {
	t := []rune(strings.TrimRight(string(runes[max(0, start-40):start]), " “‘\"'«‹"))
	if len(t) > 0 && strings.ContainsRune("—–:", t[len(t)-1]) {
		return true
	}
	return taggedBefore(runes, start)
}

// isTag: w names a thought (next: the word after it, for "told herself").
func isTag(w, next string) bool {
	w = strings.ToLower(strings.TrimFunc(w, func(c rune) bool { return !unicode.IsLetter(c) }))
	next = strings.ToLower(strings.TrimFunc(next, func(c rune) bool { return !unicode.IsLetter(c) }))
	return thoughtTags[w] || reflexiveTags[w] && reflexives[next]
}

// isName: a capitalized word of letters (punctuation after it allowed).
func isName(w string) bool {
	r := []rune(strings.TrimRight(w, ",.;:!?"))
	if len(r) == 0 || !unicode.IsUpper(r[0]) {
		return false
	}
	for _, c := range r {
		if !unicode.IsLetter(c) && c != '-' && c != '\'' && c != '’' {
			return false
		}
	}
	return true
}

// tagWords: up to n words at the start of r, stopping after a word that
// ends with punctuation (the tag ends a clause there).
func tagWords(r []rune, n int) []string {
	var out []string
	for _, w := range strings.Fields(string(r)) {
		if len(out) == n {
			break
		}
		out = append(out, w)
		if last, _ := utf8.DecodeLastRuneInString(w); strings.ContainsRune(",.;:!?—–", last) {
			break
		}
	}
	return out
}

// startsCapital: the first letter (after marks) is a capital.
func startsCapital(r []rune) bool {
	for _, c := range r {
		if unicode.IsLetter(c) {
			return unicode.IsUpper(c)
		}
		if unicode.IsDigit(c) {
			return false
		}
	}
	return false
}

// endsClause: the last character before closing marks ends a sentence or
// clause.
func endsClause(r []rune) bool {
	t := []rune(strings.TrimRight(string(r), " ”’\"')»›"))
	return len(t) > 0 && strings.ContainsRune(".!?…,;:—–", t[len(t)-1])
}

// swallowPunct extends a thought over punctuation right after it ("*I
// hate this place*, she thought"), up to limit; an apostrophe before a
// letter ("’s") is not punctuation.
func swallowPunct(runes []rune, end, limit int) int {
	for end < limit && strings.ContainsRune(",.;:!?…—–)”’\"'»›", runes[end]) {
		if (runes[end] == '’' || runes[end] == '\'') && end+1 < len(runes) && unicode.IsLetter(runes[end+1]) {
			break
		}
		end++
	}
	return end
}

// singleQuoted finds thoughts in single marks within [from, to) when
// speech uses double marks: a capitalized run of several words that ends
// a clause before its closing mark ("‘I should go,’ he thought"). Scare
// quotes (‘big bad wolf’), names (‘Royal Oak’), elisions (‘90s, ‘fraid)
// and possessives (the boys’ band) are not.
func (s *Segmenter) singleQuoted(runes []rune, from, to int) []Span {
	var open, close rune
	switch s.style {
	case CurlyDouble, LowHigh:
		open, close = '‘', '’'
	case StraightDouble:
		open, close = '\'', '\''
	case Guillemets, GuillemetsReversed:
		open, close = '‹', '›'
	default:
		return nil
	}
	var out []Span
	for i := from; i < to; i++ {
		if runes[i] != open || !opensAfter(runes, i) || isElision(runes, i) || i+1 >= to ||
			!startsThought(runes, i) {
			continue
		}
		for j := i + 1; j < to; j++ {
			if runes[j] != close || (j+1 < len(runes) && unicode.IsLetter(runes[j+1])) {
				continue
			}
			// The first closing candidate decides; a possessive (boys’)
			// does not end a clause, so nothing is taken.
			if r := runes[i : j+1]; words(r) >= 2 && words(r) <= maxThoughtWords && endsClause(r) {
				out = append(out, Span{i, j + 1})
			}
			i = j
			break
		}
	}
	return out
}

// startsThought: the text after an opening mark at i starts a thought: a
// capital, or any letter after a dash, a colon or a thought tag.
func startsThought(runes []rune, i int) bool {
	c := runes[i+1]
	return unicode.IsUpper(c) || unicode.IsLetter(c) && opensClause(runes, i)
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
