package casting

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Quote is something a character says in the book.
type Quote struct {
	Text    string
	Chapter store.Chapter
}

// Quote lengths that make a useful preview.
const (
	minQuote = 12
	maxQuote = 400
	// quoteChapters bounds the chapters searched.
	quoteChapters = 40
)

// speechVerbs mark who says a quote ("…," said Erin / Erin asked).
const speechVerbs = `said|says|asked|asks|replied|replies|answered|shouted|yelled|called|cried|whispered|` +
	`muttered|murmured|snapped|growled|added|exclaimed|laughed|sighed|told|insisted|continued|demanded|` +
	`began|went on|admitted|agreed|explained|suggested|warned|announced|declared|protested`

// Quotes finds up to limit quotes the character speaks: quotations in
// paragraphs whose narration says that one of its names said them. Until
// speaker attribution exists this is a heuristic; it may pick a wrong
// quote now and then, which is fine for a preview.
func Quotes(ctx context.Context, st *store.Store, c *store.Character, aliases []string, limit int) ([]Quote, error) {
	re := attributionRe(c.Name, aliases)
	if re == nil {
		return nil, nil
	}
	chapters, err := st.CharacterChapters(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	var out []Quote
	seen := map[string]bool{}
	for i, ch := range chapters {
		if i >= quoteChapters || len(out) >= limit {
			break
		}
		if ch.Excluded {
			continue
		}
		_, segs, err := st.ChapterText(ctx, ch.ID)
		if err != nil {
			return nil, err
		}
		for _, q := range attributed(segs, re) {
			if len(out) >= limit {
				break
			}
			if !seen[q] {
				seen[q] = true
				out = append(out, Quote{Text: q, Chapter: ch})
			}
		}
	}
	return out, nil
}

// attributed returns the longest quotation of each paragraph whose
// narration matches re.
func attributed(segs []store.Segment, re *regexp.Regexp) []string {
	var out []string
	for start := 0; start < len(segs); {
		end := start
		for end < len(segs) && segs[end].ParagraphID == segs[start].ParagraphID {
			end++
		}
		para := segs[start:end]
		start = end
		var narration strings.Builder
		quotes := map[int]*strings.Builder{}
		var order []int
		for _, s := range para {
			if s.Kind == store.SegmentNarration {
				narration.WriteString(s.Text)
				narration.WriteString(" ")
				continue
			}
			b, ok := quotes[s.QuoteRef]
			if !ok {
				b = &strings.Builder{}
				quotes[s.QuoteRef] = b
				order = append(order, s.QuoteRef)
			}
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(s.Text)
		}
		if len(order) == 0 || !re.MatchString(narration.String()) {
			continue
		}
		best := ""
		for _, ref := range order {
			q := strings.TrimSpace(quotes[ref].String())
			if n := utf8.RuneCountInString(q); n >= minQuote && n <= maxQuote && len(q) > len(best) {
				best = q
			}
		}
		if best != "" {
			out = append(out, best)
		}
	}
	return out
}

// attributionRe matches a speech verb next to one of the character's
// names: its full name, its first name, and name-like aliases.
func attributionRe(name string, aliases []string) *regexp.Regexp {
	var names []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		r, _ := utf8.DecodeRuneInString(n)
		if utf8.RuneCountInString(n) >= 2 && unicode.IsUpper(r) && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	add(name)
	if ws := strings.Fields(name); len(ws) > 1 {
		add(ws[0])
	}
	for _, a := range aliases {
		add(a)
	}
	if len(names) == 0 {
		return nil
	}
	// Longest first, so "Erin Solstice" wins over "Erin".
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	who := `(?:` + strings.Join(quoted, "|") + `)`
	verb := `(?i:` + speechVerbs + `)`
	// Names may start or end with non-ASCII letters, which \b does not know.
	const before, after = `(?:^|[^\p{L}\p{N}])`, `(?:$|[^\p{L}\p{N}])`
	return regexp.MustCompile(`(?:` + before + who + `\s+(?:\w+ly\s+)?` + verb + `\b)|(?:\b` + verb + `\s+` + who + after + `)`)
}
