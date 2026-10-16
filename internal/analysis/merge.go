package analysis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// titles are stripped when comparing names ("Captain Smith" ~ "Smith").
var titles = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "miss": true, "mister": true, "missus": true, "dr": true,
	"doctor": true, "sir": true, "dame": true, "lady": true, "lord": true, "madam": true, "madame": true,
	"captain": true, "capt": true, "king": true, "queen": true, "prince": true, "princess": true,
	"master": true, "mistress": true, "professor": true, "prof": true, "general": true,
	"commander": true, "lieutenant": true, "sergeant": true, "colonel": true, "major": true,
	"father": true, "brother": true, "sister": true, "aunt": true, "uncle": true, "grandma": true,
	"grandpa": true, "saint": true, "st": true, "young": true, "old": true, "little": true,
}

// generic words are not names: "Mom" or "Guard" must not merge characters.
var generic = map[string]bool{
	"mom": true, "mum": true, "mother": true, "dad": true, "father": true, "sir": true, "madam": true,
	"girl": true, "boy": true, "man": true, "woman": true, "guard": true, "captain": true, "king": true,
	"queen": true, "lord": true, "lady": true, "master": true, "child": true, "kid": true, "stranger": true,
	"voice": true, "someone": true, "everyone": true, "narrator": true, "doctor": true, "teacher": true,
	"soldier": true, "boss": true, "brother": true, "sister": true, "old man": true, "old woman": true,
	"young man": true, "young woman": true, "unknown": true, "ma": true, "pa": true, "mr": true,
	"ms": true, "dr": true, "me": true, "us": true, "he": true, "it": true, "oh": true, "ok": true,
}

// properName reports whether an alias looks like a personal name: it
// starts with an upper-case letter and is not a generic term.
func properName(alias, norm string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimLeft(alias, `"'“‘[(`))
	return unicode.IsUpper(r) && len(norm) >= 2 && !generic[norm] && !generic[stripTitles(norm)]
}

func stripTitles(norm string) string {
	ws := strings.Fields(norm)
	for len(ws) > 1 && titles[ws[0]] {
		ws = ws[1:]
	}
	return strings.Join(ws, " ")
}

// candidate is a pair of characters that may be the same person.
type candidate struct{ a, b int64 }

// candidates finds likely duplicates the per-batch matching missed:
//   - the same proper name apart from titles ("Captain Smith", "Smith");
//   - a single-word name that is the first or last word of another's full
//     name ("Erin", "Erin Solstice").
//
// Pairs known to be distinct are skipped; pairs with different known
// genders are returned in conflicting (they are not the same).
func candidates(chars []store.Character, aliases []store.CharacterAlias, distinct map[[2]int64]bool,
	limit int,
) (pairs, conflicting []candidate, more bool) {
	byID := map[int64]*store.Character{}
	names := map[int64][][2]string{} // alias, norm
	for i := range chars {
		c := &chars[i]
		byID[c.ID] = c
		names[c.ID] = append(names[c.ID], [2]string{c.Name, store.NormName(c.Name)})
	}
	for _, a := range aliases {
		names[a.CharacterID] = append(names[a.CharacterID], [2]string{a.Alias, a.Norm})
	}

	full := map[string][]int64{}   // title-stripped proper names
	single := map[string][]int64{} // single-word proper names
	partOf := map[string][]int64{} // first/last words of multi-word proper names
	for id, ns := range names {
		for _, n := range ns {
			if !properName(n[0], n[1]) {
				continue
			}
			key := stripTitles(n[1])
			full[key] = appendUnique(full[key], id)
			ws := strings.Fields(key)
			switch {
			case len(ws) == 1:
				single[key] = appendUnique(single[key], id)
			case len(ws) > 1:
				partOf[ws[0]] = appendUnique(partOf[ws[0]], id)
				partOf[ws[len(ws)-1]] = appendUnique(partOf[ws[len(ws)-1]], id)
			}
		}
	}

	seen := map[[2]int64]bool{}
	consider := func(a, b int64) {
		if a == b {
			return
		}
		key := [2]int64{min(a, b), max(a, b)}
		if seen[key] || distinct[key] {
			return
		}
		seen[key] = true
		ca, cb := byID[a], byID[b]
		if ca == nil || cb == nil {
			return
		}
		if known(ca.Gender) && known(cb.Gender) && ca.Gender != cb.Gender {
			conflicting = append(conflicting, candidate{key[0], key[1]})
			return
		}
		pairs = append(pairs, candidate{key[0], key[1]})
	}
	for _, ids := range full {
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				consider(ids[i], ids[j])
			}
		}
	}
	for word, singles := range single {
		for _, s := range singles {
			for _, other := range partOf[word] {
				consider(s, other)
			}
		}
	}

	// Most important pairs first; the rest waits for a later run.
	weight := func(p candidate) int {
		return byID[p.a].Importance + byID[p.b].Importance
	}
	sort.Slice(pairs, func(i, j int) bool {
		if wi, wj := weight(pairs[i]), weight(pairs[j]); wi != wj {
			return wi > wj
		}
		if pairs[i].a != pairs[j].a {
			return pairs[i].a < pairs[j].a
		}
		return pairs[i].b < pairs[j].b
	})
	if len(pairs) > limit {
		pairs, more = pairs[:limit], true
	}
	return pairs, conflicting, more
}

func known(g store.Gender) bool { return g == store.GenderFemale || g == store.GenderMale }

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

// mergeResult is the answer schema of the merge_characters prompt.
type mergeResult struct {
	Decisions []struct {
		Pair string `json:"pair"`
		Same bool   `json:"same"`
	} `json:"decisions"`
}

// profile is a compact description of a character for a merge request.
func profile(c *store.Character, aliases []store.CharacterAlias, chapters string) string {
	var as []string
	for _, a := range aliases {
		as = append(as, a.Alias)
	}
	if len(as) > 6 {
		as = as[:6]
	}
	parts := []string{c.Name}
	if len(as) > 0 {
		parts = append(parts, "aka "+strings.Join(as, ", "))
	}
	parts = append(parts, string(c.Gender)+" "+string(c.AgeGroup))
	if c.Role != "" {
		parts = append(parts, c.Role)
	}
	if c.Description != "" {
		parts = append(parts, c.Description)
	}
	parts = append(parts, chapters)
	return strings.Join(parts, " | ")
}

// mergeDuplicates asks the LLM about candidate pairs in groups and merges
// or marks them distinct; both decisions are stored, so a resumed run only
// asks about pairs left over. More candidates than MergePairs are handled
// in further rounds.
func (a *Analyzer) mergeDuplicates(ctx context.Context, bookID int64, progress func(float64)) error {
	for {
		asked, more, err := a.mergeRound(ctx, bookID, progress)
		if err != nil || !more || !asked {
			return err
		}
	}
}

// mergeRound handles up to MergePairs candidates; asked reports whether it
// decided anything, more whether candidates were left over.
func (a *Analyzer) mergeRound(ctx context.Context, bookID int64, progress func(float64)) (asked, more bool, err error) {
	chars, err := a.Store.BookCharacters(ctx, bookID)
	if err != nil {
		return false, false, err
	}
	aliases, err := a.Store.BookAliases(ctx, bookID)
	if err != nil {
		return false, false, err
	}
	distinct, err := a.Store.DistinctPairs(ctx, bookID)
	if err != nil {
		return false, false, err
	}
	pairs, conflicting, more := candidates(chars, aliases, distinct, a.maxMergePairs())
	for _, p := range conflicting {
		if err := a.Store.MarkDistinct(ctx, bookID, p.a, p.b); err != nil {
			return false, false, err
		}
		distinct[[2]int64{p.a, p.b}] = true
	}
	if len(pairs) == 0 {
		return false, more, nil
	}
	byID := map[int64]*store.Character{}
	for i := range chars {
		byID[chars[i].ID] = &chars[i]
	}
	aliasesOf := map[int64][]store.CharacterAlias{}
	for _, al := range aliases {
		aliasesOf[al.CharacterID] = append(aliasesOf[al.CharacterID], al)
	}
	// survivor follows merges made during this run.
	merged := map[int64]int64{}
	survivor := func(id int64) int64 {
		for {
			next, ok := merged[id]
			if !ok {
				return id
			}
			id = next
		}
	}

	var ids []int64
	for _, p := range pairs {
		ids = append(ids, p.a, p.b)
	}
	last, err := a.Store.LastChapterIdx(ctx, ids...)
	if err != nil {
		return false, false, err
	}
	isDistinct := func(x, y int64) bool { return distinct[[2]int64{min(x, y), max(x, y)}] }
	group := a.mergeGroupSize()
	for start := 0; start < len(pairs); start += group {
		if paused, err := a.paused(ctx, bookID, store.AnalysisMerging); err != nil || paused {
			return false, false, err
		}
		progress(float64(start) / float64(len(pairs)))
		var ask []candidate
		var lines []pairLine
		for _, p := range pairs[start:min(start+group, len(pairs))] {
			pa, pb := survivor(p.a), survivor(p.b)
			if pa == pb || isDistinct(pa, pb) {
				continue
			}
			ask = append(ask, candidate{pa, pb})
			lines = append(lines, pairLine{
				ID: fmt.Sprintf("p%d", len(ask)),
				A:  profile(byID[pa], aliasesOf[pa], chapterSpan(byID[pa], last[pa])),
				B:  profile(byID[pb], aliasesOf[pb], chapterSpan(byID[pb], last[pb])),
			})
		}
		if len(ask) == 0 {
			continue
		}
		req, err := mergePrompt.Render(map[string]any{"Pairs": lines})
		if err != nil {
			return false, false, err
		}
		req.MaxTokens = 2048
		var out mergeResult
		_, err = a.LLM.CompleteJSON(ctx, req, &out)
		var (
			rejected *llm.RejectedError
			tooLarge *llm.TooLargeError
			invalid  *llm.InvalidOutputError
		)
		if errors.As(err, &rejected) || errors.As(err, &tooLarge) || errors.As(err, &invalid) {
			// No provider takes this group, or the model cannot answer it:
			// leave its pairs undecided rather than failing the whole
			// analysis.
			a.Log.Warn("duplicate check request refused, skipping its pairs", "book", bookID, "err", err)
			continue
		}
		if err != nil {
			return false, false, err
		}
		if done, ok := ctx.Value(batchDoneKey{}).(func(context.Context)); ok {
			done(ctx) // progress: reset the job's attempts
		}
		for _, d := range out.Decisions {
			var n int
			if _, err := fmt.Sscanf(d.Pair, "p%d", &n); err != nil || n < 1 || n > len(ask) {
				continue
			}
			p := ask[n-1]
			pa, pb := survivor(p.a), survivor(p.b)
			if pa == pb || isDistinct(pa, pb) {
				continue
			}
			if !d.Same {
				if err := a.Store.MarkDistinct(ctx, bookID, pa, pb); err != nil {
					return false, false, err
				}
				distinct[[2]int64{min(pa, pb), max(pa, pb)}] = true
				asked = true
				continue
			}
			// A character corrected by hand survives (two of them are left
			// to the user); otherwise the better-known one.
			keep, drop := pa, pb
			ck, cd := byID[keep], byID[drop]
			switch {
			case ck.UserEdited && cd.UserEdited:
				// Both curated by hand: the user decides; do not ask again.
				if err := a.Store.MarkDistinct(ctx, bookID, pa, pb); err != nil {
					return false, false, err
				}
				distinct[[2]int64{min(pa, pb), max(pa, pb)}] = true
				asked = true
				continue
			case cd.UserEdited:
				keep, drop = drop, keep
			case ck.UserEdited:
			case cd.ChapterCount > ck.ChapterCount ||
				(cd.ChapterCount == ck.ChapterCount && len(strings.Fields(cd.Name)) > len(strings.Fields(ck.Name))):
				keep, drop = drop, keep
			}
			c, err := a.Store.MergeCharacters(ctx, keep, drop)
			if err != nil {
				return false, false, err
			}
			byID[keep] = c
			aliasesOf[keep] = append(aliasesOf[keep], aliasesOf[drop]...)
			merged[drop] = keep
			asked = true
			// "drop is not X" now holds for keep (as in the store).
			for k := range distinct {
				if k[0] == drop || k[1] == drop {
					other := k[0] + k[1] - drop
					if other != keep {
						distinct[[2]int64{min(keep, other), max(keep, other)}] = true
					}
				}
			}
			a.Log.Info("merged duplicate characters", "book", bookID, "kept", c.Name, "merged", byID[drop].Name)
		}
	}
	return asked, more, nil
}

type pairLine struct{ ID, A, B string }

// chapterSpan describes where a character appears ("chapters 3–40, 12 in
// total").
func chapterSpan(c *store.Character, last int) string {
	if c.ChapterCount == 0 {
		return "no chapters"
	}
	last = max(last, c.FirstChapterIdx)
	return fmt.Sprintf("chapters %d–%d, %d in total", c.FirstChapterIdx+1, last+1, c.ChapterCount)
}

var mergePrompt = llm.MustLoadPrompt("merge_characters")
