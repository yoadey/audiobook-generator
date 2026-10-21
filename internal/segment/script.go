package segment

import (
	"strings"
	"unicode"
)

// Voiced is a span of a paragraph that is not narration: speech, a
// thought or another voice, as the speaker attribution found it.
type Voiced struct {
	Span
	Kind Kind // Quote or Thought
}

// Script splits a paragraph into narration and the given voiced spans
// (sorted, disjoint, in runes); emph are its italic runs. The voiced
// parts are numbered from first in the order of spans (Part.Quote), so a
// caller finds each span's part by its number; a span with nothing but
// spaces has none. No character of the text is lost: narration without a
// word joins its neighbour, as in Paragraph.
func Script(text string, emph []Span, spans []Voiced, first int) []Part {
	runes := []rune(text)
	var pieces []piece
	at := 0
	for i, v := range spans {
		if v.Start < at || v.End > len(runes) || v.End <= v.Start {
			continue
		}
		pieces = append(pieces, piece{kind: Narration, from: at, to: v.Start},
			piece{kind: v.Kind, from: v.Start, to: v.End, num: first + i})
		at = v.End
	}
	pieces = append(pieces, piece{kind: Narration, from: at, to: len(runes)})
	var parts []Part
	for _, p := range tidy(runes, pieces) {
		parts = append(parts, Part{Kind: p.kind, Quote: p.num, Text: string(runes[p.from:p.to]),
			Emphasis: within(emph, p.from, p.to)})
	}
	return parts
}

// Locate finds the span of a paragraph that begins with the words start
// and ends with the words end (as the LLM copied them; one may hold the
// whole span), searching from rune offset from. Quotation marks, dashes,
// ellipses, spaces, letter case and asterisks (italics in the prompt) need
// not match exactly. The span grows over the quotation marks right around
// it (and punctuation before a closing one), so speech keeps its marks.
func Locate(text string, from int, start, end string) (Span, bool) {
	runes := []rune(text)
	norm, at := fold(runes)
	ns, _ := fold([]rune(strings.ReplaceAll(start, "*", "")))
	ne, _ := fold([]rune(strings.ReplaceAll(end, "*", "")))
	ns, ne = trimSpace(ns), trimSpace(ne)
	if len(ns) == 0 {
		return Span{}, false
	}
	if len(ne) == 0 {
		ne = ns
	}
	nfrom := 0
	for nfrom < len(at) && at[nfrom] < from {
		nfrom++
	}
	i := indexRunes(norm, ns, nfrom)
	if i < 0 {
		return Span{}, false
	}
	j := indexRunes(norm, ne, i)
	if j < 0 {
		return Span{}, false
	}
	// The last occurrence of end that still starts in the same voiced
	// span is not knowable here; the first one after start is taken.
	s, e := at[i], at[j+len(ne)-1]+1
	return grow(runes, Span{s, e}), true
}

// fold maps text to a simpler form for matching, with the rune offset in
// the original of each folded rune.
func fold(runes []rune) ([]rune, []int) {
	out := make([]rune, 0, len(runes))
	at := make([]int, 0, len(runes))
	space := false
	for i, r := range runes {
		switch {
		case unicode.IsSpace(r):
			if !space && len(out) > 0 {
				out, at = append(out, ' '), append(at, i)
			}
			space = true
			continue
		case strings.ContainsRune("“”„‟«»‹›\"", r):
			r = '"'
		case strings.ContainsRune("‘’‚‛′'", r):
			r = '\''
		case strings.ContainsRune("—–―‒-", r):
			r = '-'
		case r == '…':
			out, at = append(out, '.', '.'), append(at, i, i)
			r = '.'
		default:
			r = unicode.ToLower(r)
		}
		space = false
		out, at = append(out, r), append(at, i)
	}
	return out, at
}

func trimSpace(r []rune) []rune {
	for len(r) > 0 && r[0] == ' ' {
		r = r[1:]
	}
	for len(r) > 0 && r[len(r)-1] == ' ' {
		r = r[:len(r)-1]
	}
	return r
}

const (
	openMarks  = "“‘\"'«»‹›„‚"
	closeMarks = "”’\"'»«›‹“"
)

// grow extends a span over the quotation marks around it.
func grow(runes []rune, s Span) Span {
	for s.Start > 0 && strings.ContainsRune(openMarks, runes[s.Start-1]) {
		s.Start--
	}
	for {
		j := s.End
		for j < len(runes) && strings.ContainsRune(",.!?…;:—–-", runes[j]) {
			j++
		}
		if j < len(runes) && strings.ContainsRune(closeMarks, runes[j]) &&
			(j+1 == len(runes) || !unicode.IsLetter(runes[j+1])) {
			s.End = j + 1
			continue
		}
		return s
	}
}
