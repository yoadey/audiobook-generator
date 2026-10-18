package prepare

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// Moods of narration the prompt offers.
var Moods = []string{"calm", "tense", "action", "somber", "warm", "light", "mysterious"}

// Pause lengths.
const (
	shortPauseMs = 500
	longPauseMs  = 1000
)

// Limits of an answer.
const (
	maxSpotRunes     = 40
	maxSuggestions   = 10
	maxNoteRunes     = 200
	maxRespellRunes  = 60
	minAnchorWords   = 2
	pausesPerParas   = 5 // at most one pause per this many paragraphs (plus a few)
	extraPausesAllow = 3
)

type answer struct {
	Moods []struct {
		P    int    `json:"p"`
		Mood string `json:"mood"`
	} `json:"moods"`
	Pauses []struct {
		P      int    `json:"p"`
		After  string `json:"after"`
		Length string `json:"length"`
	} `json:"pauses"`
	Spoken []struct {
		P    int    `json:"p"`
		Text string `json:"text"`
		Say  string `json:"say"`
	} `json:"spoken"`
	Pronunciations []struct {
		Term string `json:"term"`
		Say  string `json:"say"`
		Note string `json:"note"`
	} `json:"pronunciations"`
	Flows []int `json:"flows"`
}

// placed is a segment with where its text starts in its paragraph (runes).
type placed struct {
	seg   *store.Segment
	start int
	runes []rune
}

// layout finds each segment's text in its paragraph, in order (nil when a
// segment cannot be found: that paragraph gets no pauses or spoken forms).
func layout(p *store.Paragraph, segs []*store.Segment) []placed {
	text := []rune(p.Text)
	var out []placed
	at := 0
	for _, s := range segs {
		r := []rune(s.Text)
		i := indexRunes(text, r, at)
		if i < 0 {
			return nil
		}
		out = append(out, placed{s, i, r})
		at = i + len(r)
	}
	return out
}

func indexRunes(hay, needle []rune, from int) int {
	if len(needle) == 0 {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// fold makes text comparable: lower case, plain quotation marks and
// apostrophes, one rune per rune (offsets stay valid).
func fold(r []rune) []rune {
	out := make([]rune, len(r))
	for i, c := range r {
		switch c {
		case '’', '‘', '‚', '‛', '`', '´':
			c = '\''
		case '“', '”', '„', '‟', '«', '»':
			c = '"'
		default:
			c = unicode.ToLower(c)
		}
		out[i] = c
	}
	return out
}

// interpret checks an answer against paragraphs [from, to) and keeps what
// fits the text: moods of paragraphs that exist, pauses whose words are
// found where a sentence or clause ends, spoken forms of spots found
// verbatim within one segment (not ordinary prose), and pronunciations of
// terms that occur in the text and are not known yet.
func interpret(paras []store.Paragraph, segs []store.Segment, from, to int, known []string, ans *answer,
) store.Preparation {
	out := store.Preparation{Moods: map[int64]string{}}
	byPara := map[int64][]*store.Segment{}
	for i := range segs {
		byPara[segs[i].ParagraphID] = append(byPara[segs[i].ParagraphID], &segs[i])
	}
	para := func(n int) *store.Paragraph {
		i := n - 1
		if i < from || i >= to || paras[i].Kind == store.ParagraphSceneBreak {
			return nil
		}
		return &paras[i]
	}
	for _, m := range ans.Moods {
		mood := strings.ToLower(strings.TrimSpace(m.Mood))
		if p := para(m.P); p != nil && p.Kind == store.ParagraphText && slices.Contains(Moods, mood) {
			out.Moods[p.ID] = mood
		}
	}
	// A paragraph flows on from a text paragraph right before it (also
	// the last one of the part before).
	for _, n := range ans.Flows {
		if p := para(n); p != nil && p.Kind == store.ParagraphText && n >= 2 &&
			paras[n-2].Kind == store.ParagraphText && !slices.Contains(out.Flows, p.ID) {
			out.Flows = append(out.Flows, p.ID)
		}
	}

	prep := map[int64]*store.SegmentPrep{}
	segPrep := func(id int64) *store.SegmentPrep {
		if prep[id] == nil {
			prep[id] = &store.SegmentPrep{SegmentID: id}
		}
		return prep[id]
	}
	paused := map[int64]bool{}
	budget := (to-from)/pausesPerParas + extraPausesAllow
	for _, pa := range ans.Pauses {
		p := para(pa.P)
		if p == nil || paused[p.ID] || budget == 0 {
			continue
		}
		ms := shortPauseMs
		if strings.EqualFold(strings.TrimSpace(pa.Length), "long") {
			ms = longPauseMs
		}
		pl := layout(p, byPara[p.ID])
		end, ok := anchorEnd([]rune(p.Text), pa.After)
		if pl == nil || !ok {
			continue
		}
		for _, x := range pl {
			if end > x.start && end <= x.start+len(x.runes) {
				sp := segPrep(x.seg.ID)
				sp.Pauses = append(sp.Pauses, store.Pause{Offset: end - x.start, Ms: ms})
				paused[p.ID] = true
				budget--
				break
			}
		}
	}

	for _, sa := range ans.Spoken {
		p := para(sa.P)
		spot := []rune(strings.TrimSpace(sa.Text))
		say := cleanSay(sa.Say)
		if p == nil || !spotOK(spot, say) {
			continue
		}
		pl := layout(p, byPara[p.ID])
		for _, x := range pl {
			for at := 0; ; {
				i := indexRunes(x.runes, spot, at)
				if i < 0 {
					break
				}
				at = i + len(spot)
				if !bounded(x.runes, i, at) {
					continue
				}
				sp := segPrep(x.seg.ID)
				sp.SpokenAs = append(sp.SpokenAs, store.SpokenForm{Start: i, End: at, Say: say})
			}
		}
	}
	for _, sp := range prep {
		sp.SpokenAs = disjoint(sp.SpokenAs)
		slices.SortFunc(sp.Pauses, func(a, b store.Pause) int { return a.Offset - b.Offset })
		out.Segments = append(out.Segments, *sp)
	}
	slices.SortFunc(out.Segments, func(a, b store.SegmentPrep) int { return int(a.SegmentID - b.SegmentID) })

	text := strings.Builder{}
	for i := from; i < to; i++ {
		text.WriteString(paras[i].Text)
		text.WriteString("\n")
	}
	seen := map[string]bool{}
	for _, k := range known {
		seen[strings.ToLower(k)] = true
	}
	for _, pr := range ans.Pronunciations {
		term, say := lexicon.Normalize(pr.Term, strings.ToLower(pr.Say))
		if len(out.Suggestions) == maxSuggestions || seen[strings.ToLower(term)] ||
			lexicon.Validate(term, say, false) != nil || !respelling(say) || strings.EqualFold(term, say) ||
			!occurs(text.String(), term) {
			continue
		}
		seen[strings.ToLower(term)] = true
		out.Suggestions = append(out.Suggestions, store.LexiconEntry{
			Term: term, Replacement: say, Note: truncate(store.CleanText(strings.TrimSpace(pr.Note)), maxNoteRunes),
		})
	}
	return out
}

// anchorEnd finds the words before a pause in a paragraph and returns
// where the pause goes: after them and their closing marks, which must end
// a sentence or clause.
func anchorEnd(text []rune, after string) (int, bool) {
	a := []rune(strings.Join(strings.Fields(after), " "))
	if len(strings.Fields(after)) < minAnchorWords {
		return 0, false
	}
	i := indexRunes(fold(text), fold(a), 0)
	if i < 0 {
		return 0, false
	}
	end := i + len(a)
	for end < len(text) && strings.ContainsRune("”’\"')»›", text[end]) {
		end++
	}
	t := strings.TrimRight(string(text[i:end]), "”’\"')»› ")
	last, _ := utf8.DecodeLastRuneInString(t)
	return end, strings.ContainsRune(".!?…,;:—–", last)
}

// cleanSay makes a spoken form plain words: no Kokoro syntax, one line.
func cleanSay(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune("[](){}<>/\\*_#|~", r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(store.CleanText(s)), " ")
}

// Abbreviations a spot may hold: read as words, never words themselves
// (with or without their period), and those that are only abbreviations
// with their period ("No. 5", not "no").
var (
	abbrevs = set("lv", "lvl", "approx", "etc", "vs", "mr", "mrs", "ms", "dr", "prof", "jr", "sr", "mt", "ft",
		"pg", "pp", "hrs", "qty", "str", "dex", "agi", "vit", "wis", "cha", "int", "def", "atk", "hp", "mp", "sp", "xp")
	dottedAbbrevs = set("no", "nos", "st", "vol", "ch", "pt", "pts", "hr", "min", "sec", "ca", "cf", "capt", "sgt",
		"lt", "gov", "gen", "ave", "rd")
	// Units, as notation only in a spot with a number ("5 km").
	units      = set("km", "kg", "cm", "mm", "ml", "mph", "kph", "lb", "lbs", "oz", "yd", "yds", "mi", "m", "g", "l", "s")
	initialsRe = regexp.MustCompile(`^(?:\pL\.){2,}$`) // e.g., U.S., a.m.
	romanRe    = regexp.MustCompile(`^[IVXLCDM]{2,}$`)
)

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// spotOK: a spot to say differently is short notation a voice misreads
// (numbers, symbols, abbreviations, acronyms, Roman numerals), and its
// spoken form keeps every ordinary word of the spot, in order. The LLM
// only spells notation out; it can never change, add or drop words.
func spotOK(spot []rune, say string) bool {
	n := utf8.RuneCountInString(say)
	switch {
	case len(spot) == 0 || len(spot) > maxSpotRunes || n == 0 || n > 4*len(spot)+40:
		return false
	case strings.IndexFunc(say, unicode.IsLetter) < 0 || strings.EqualFold(say, string(spot)):
		return false
	}
	said := words(say)
	number := strings.IndexFunc(string(spot), unicode.IsDigit) >= 0
	var kept []string
	budget := 0 // words the notation may be said with
	for _, t := range strings.Fields(string(spot)) {
		core := strings.TrimFunc(t, edgeMark)
		bare := strings.TrimSuffix(core, ".")
		lower := strings.ToLower(bare)
		switch digits := countFunc(core, unicode.IsDigit); {
		case core == "":
		case digits > 0 || strings.IndexFunc(core, symbol) >= 0:
			budget += 2*digits + 2*countFunc(core, symbol) + 1
		case abbrevs[lower] || (dottedAbbrevs[lower] && bare != core) || initialsRe.MatchString(core):
			budget += 2
		case number && units[lower]:
			budget += 3
		case romanRe.MatchString(bare):
			budget += 3 // "the eighth"
		case acronym(bare, said):
			budget += utf8.RuneCountInString(bare)
		default:
			kept = append(kept, words(core)...)
		}
	}
	return budget > 0 && len(said) <= len(kept)+budget && subsequence(kept, said)
}

func countFunc(s string, f func(rune) bool) int {
	n := 0
	for _, r := range s {
		if f(r) {
			n++
		}
	}
	return n
}

// edgeMark: punctuation around a word in a spot.
func edgeMark(r rune) bool {
	return strings.ContainsRune(`"'“”‘’„«»‹›()[]{},;:!?…—–`, r)
}

// symbol: a rune that is notation inside a word (→, /, %, +, °, ×, #, @, &).
func symbol(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(".-'’", r)
}

// acronym: two to six capitals said as words starting with its letters
// ("HP" → "hit points") or spelled out ("h p").
func acronym(core string, said []string) bool {
	letters := []rune(strings.ToLower(core))
	if len(letters) < 2 || len(letters) > 6 || strings.ToUpper(core) != core ||
		strings.IndexFunc(core, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
		return false
	}
	for i := 0; i+len(letters) <= len(said); i++ {
		ok := true
		for j, l := range letters {
			w := said[i+j]
			if !strings.HasPrefix(w, string(l)) && (l != 'x' || !strings.HasPrefix(w, "ex")) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// words: the lower-case words of a text, apostrophes plain.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(s, "’", "'")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
}

// subsequence: every word of want is in have, in order.
func subsequence(want, have []string) bool {
	i := 0
	for _, w := range have {
		if i < len(want) && w == want[i] {
			i++
		}
	}
	return i == len(want)
}

// bounded: a spot starting or ending with a letter or digit is a whole
// word there ("HP" not in "HPotion").
func bounded(r []rune, i, j int) bool {
	word := func(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) }
	if word(r[i]) && i > 0 && word(r[i-1]) {
		return false
	}
	return !word(r[j-1]) || j == len(r) || !word(r[j])
}

// disjoint sorts spoken forms and drops those overlapping an earlier one.
func disjoint(fs []store.SpokenForm) []store.SpokenForm {
	slices.SortStableFunc(fs, func(a, b store.SpokenForm) int { return a.Start - b.Start })
	var out []store.SpokenForm
	for _, f := range fs {
		if len(out) == 0 || f.Start >= out[len(out)-1].End {
			out = append(out, f)
		}
	}
	return out
}

var respellRe = regexp.MustCompile(`^[a-z]+(?:[- '][a-z]+)*$`)

// respelling: lower-case letters with hyphens, spaces or apostrophes.
func respelling(s string) bool {
	return utf8.RuneCountInString(s) <= maxRespellRunes && respellRe.MatchString(s)
}

// occurs: the term is in the text as a whole word, ignoring case.
func occurs(text, term string) bool {
	re, err := regexp.Compile(`(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(term) + `($|[^\p{L}\p{N}])`)
	return err == nil && re.MatchString(text)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
