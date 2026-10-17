package segment

import (
	"strings"
	"unicode"
)

// MaxChunk is the default chunk size in characters. Kokoro handles up to
// ~510 phonemes per call; 400 characters stay safely below that.
const MaxChunk = 400

// Abbreviations whose trailing period does not end a sentence.
var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "st": true, "jr": true, "sr": true,
	"prof": true, "capt": true, "gen": true, "col": true, "lt": true, "sgt": true,
	"rev": true, "hon": true, "vs": true, "etc": true, "no": true, "mt": true,
	"e.g": true, "i.e": true, "cf": true, "fig": true, "vol": true, "ch": true,
}

// Sentences splits text at sentence boundaries, keeping all characters.
func Sentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for i := 0; i < len(runes); i++ {
		if !strings.ContainsRune(".!?…", runes[i]) {
			continue
		}
		// Swallow repeated terminators and closing quotes/brackets.
		j := i + 1
		for j < len(runes) && strings.ContainsRune(".!?…”’\"')]", runes[j]) {
			j++
		}
		if j >= len(runes) || !unicode.IsSpace(runes[j]) {
			continue
		}
		k := j
		for k < len(runes) && unicode.IsSpace(runes[k]) {
			k++
		}
		if k < len(runes) && !startsSentence(runes[k]) {
			continue
		}
		if runes[i] == '.' && isAbbreviation(runes, i) {
			continue
		}
		out = append(out, strings.TrimSpace(string(runes[start:j])))
		start = k
		i = k - 1
	}
	if rest := strings.TrimSpace(string(runes[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}

func startsSentence(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune("“‘\"'([—–", r)
}

// isAbbreviation looks at the word before the period at i.
func isAbbreviation(runes []rune, i int) bool {
	j := i
	for j > 0 && (unicode.IsLetter(runes[j-1]) || runes[j-1] == '.') {
		j--
	}
	word := string(runes[j:i])
	if len([]rune(word)) == 1 && unicode.IsUpper([]rune(word)[0]) {
		return true // initial: "J. R. R. Tolkien"
	}
	return abbreviations[strings.ToLower(word)]
}

// Chunk splits text into pieces of at most max characters, preferring
// sentence boundaries, then clause boundaries, then spaces.
func Chunk(text string, max int) []string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= max {
		if text == "" {
			return nil
		}
		return []string{text}
	}
	var pieces []string
	for _, s := range Sentences(text) {
		if len([]rune(s)) <= max {
			pieces = append(pieces, s)
			continue
		}
		pieces = append(pieces, splitLong(s, max)...)
	}
	return pack(pieces, max)
}

// pack joins consecutive pieces while they fit.
func pack(pieces []string, max int) []string {
	var out []string
	var cur string
	for _, p := range pieces {
		switch {
		case cur == "":
			cur = p
		case len([]rune(cur))+1+len([]rune(p)) <= max:
			cur += " " + p
		default:
			out = append(out, cur)
			cur = p
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// splitLong breaks an overlong sentence at clause punctuation, else spaces.
func splitLong(s string, max int) []string {
	var clauses []string
	start := 0
	runes := []rune(s)
	for i, r := range runes {
		if strings.ContainsRune(",;:—–", r) && i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
			clauses = append(clauses, strings.TrimSpace(string(runes[start:i+1])))
			start = i + 1
		}
	}
	clauses = append(clauses, strings.TrimSpace(string(runes[start:])))

	var out []string
	for _, c := range clauses {
		if len([]rune(c)) <= max {
			out = append(out, c)
			continue
		}
		out = append(out, pack(strings.Fields(c), max)...)
	}
	return pack(out, max)
}

// ChunkPart chunks a part like Chunk; the pieces keep the part's kind,
// number and the italic runs that fall into them.
func ChunkPart(p Part, max int) []Part {
	chunks := Chunk(p.Text, max)
	out := make([]Part, 0, len(chunks))
	text := []rune(p.Text)
	at := 0
	for _, c := range chunks {
		cr := []rune(c)
		// Chunks are the part's text in order, joined at single spaces.
		start := indexRunes(text, cr, at)
		piece := Part{Kind: p.Kind, Quote: p.Quote, Text: c}
		if start >= 0 {
			piece.Emphasis = within(p.Emphasis, start, start+len(cr))
			at = start + len(cr)
		}
		out = append(out, piece)
	}
	return out
}

// indexRunes finds sub in s from position from (-1 if absent).
func indexRunes(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if string(s[i:i+len(sub)]) == string(sub) {
			return i
		}
	}
	return -1
}
