package analysis

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// batchResult is the answer schema of the character_analysis prompt.
type batchResult struct {
	Characters []struct {
		Ref         string   `json:"ref"`
		Name        string   `json:"name"`
		Aliases     []string `json:"aliases"`
		Gender      string   `json:"gender"`
		Age         string   `json:"age"`
		Role        string   `json:"role"`
		Description string   `json:"description"`
		SpeechStyle string   `json:"speech_style"`
		Importance  int      `json:"importance"`
		Speaks      bool     `json:"speaks"`
		Chapters    []int    `json:"chapters"`
	} `json:"characters"`
	Chapters []struct {
		N         int    `json:"n"`
		Narration string `json:"narration"`
		POV       string `json:"pov"`
	} `json:"chapters"`
}

// bible is the in-memory index of a book's characters used to match the
// names an answer reports.
type bible struct {
	chars  map[int64]*store.Character
	byNorm map[string][]int64 // name and alias norms → characters
	// words of each name/alias norm, for finding the characters a text
	// mentions.
	names map[int64][]string
}

func loadBible(ctx context.Context, st *store.Store, bookID int64) (*bible, error) {
	cs, err := st.BookCharacters(ctx, bookID)
	if err != nil {
		return nil, err
	}
	as, err := st.BookAliases(ctx, bookID)
	if err != nil {
		return nil, err
	}
	b := &bible{chars: map[int64]*store.Character{}, byNorm: map[string][]int64{}, names: map[int64][]string{}}
	for i := range cs {
		b.add(&cs[i])
	}
	for _, a := range as {
		b.addName(a.CharacterID, a.Norm)
	}
	return b, nil
}

func (b *bible) add(c *store.Character) {
	b.chars[c.ID] = c
	b.addName(c.ID, store.NormName(c.Name))
}

func (b *bible) addName(id int64, norm string) {
	if norm == "" || slices.Contains(b.byNorm[norm], id) {
		return
	}
	b.byNorm[norm] = append(b.byNorm[norm], id)
	b.names[id] = append(b.names[id], norm)
}

// best picks the most important of several matches.
func (b *bible) best(ids []int64) int64 {
	var best *store.Character
	for _, id := range ids {
		c := b.chars[id]
		if c == nil {
			continue
		}
		if best == nil || c.Importance > best.Importance ||
			(c.Importance == best.Importance && c.ChapterCount > best.ChapterCount) {
			best = c
		}
	}
	if best == nil {
		return 0
	}
	return best.ID
}

// match finds the known character a reported name refers to: by its full
// name first, then by its aliases if they all point to one character.
func (b *bible) match(name string, aliases []string) int64 {
	if ids := b.byNorm[store.NormName(name)]; len(ids) > 0 {
		return b.best(ids)
	}
	found := map[int64]bool{}
	for _, a := range aliases {
		for _, id := range b.byNorm[store.NormName(a)] {
			found[id] = true
		}
	}
	if len(found) == 1 {
		for id := range found {
			return id
		}
	}
	return 0
}

// relevant lists the known characters a text mentions (all words of a name
// or alias occur in it), most important first, at most limit.
func (b *bible) relevant(text string, limit int) []*store.Character {
	words := map[string]bool{}
	lower := strings.NewReplacer("’", "'", "‘", "'").Replace(strings.ToLower(text))
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-'
	}) {
		w = strings.Trim(w, "'-")
		words[w] = true
		// "Erin's" mentions Erin.
		if base, ok := strings.CutSuffix(w, "'s"); ok {
			words[base] = true
		}
	}
	var out []*store.Character
	for id, norms := range b.names {
		for _, n := range norms {
			all := true
			for _, w := range strings.Fields(n) {
				if !words[w] {
					all = false
					break
				}
			}
			if all {
				out = append(out, b.chars[id])
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Importance != out[j].Importance {
			return out[i].Importance > out[j].Importance
		}
		if out[i].ChapterCount != out[j].ChapterCount {
			return out[i].ChapterCount > out[j].ChapterCount
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func ref(id int64) string { return "c" + strconv.FormatInt(id, 10) }

func parseRef(s string) int64 {
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "c"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// knownLines formats characters for the prompt.
func knownLines(cs []*store.Character, aliases map[int64][]store.CharacterAlias) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		line := fmt.Sprintf("%s: %s", ref(c.ID), c.Name)
		var as []string
		for _, a := range aliases[c.ID] {
			as = append(as, a.Alias)
		}
		if len(as) > 8 {
			as = as[:8]
		}
		if len(as) > 0 {
			line += " (aka " + strings.Join(as, "; ") + ")"
		}
		if c.Gender != store.GenderUnknown {
			line += ", " + string(c.Gender)
		}
		out = append(out, line)
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func gender(s string) store.Gender {
	switch store.Gender(s) {
	case store.GenderFemale, store.GenderMale:
		return store.Gender(s)
	}
	return store.GenderUnknown
}

func ageGroup(s string) store.AgeGroup {
	switch store.AgeGroup(s) {
	case store.AgeChild, store.AgeTeen, store.AgeAdult, store.AgeElderly:
		return store.AgeGroup(s)
	}
	return store.AgeUnknown
}

func narration(s string) store.Narration {
	switch store.Narration(s) {
	case store.NarrationFirstPerson, store.NarrationThirdPerson, store.NarrationMixed:
		return store.Narration(s)
	}
	return store.NarrationUnknown
}

// apply merges a batch answer into the book's character bible. It runs
// inside the transaction that also completes the batch.
func apply(ctx context.Context, tx *store.Store, bookID int64, b *bible, parts []chapterPart, res *batchResult) error {
	chapterOf := func(n int) (chapterPart, bool) {
		if n < 1 || n > len(parts) {
			return chapterPart{}, false
		}
		return parts[n-1], true
	}
	touched := map[int64]bool{}
	for _, rc := range res.Characters {
		name := clip(rc.Name, 120)
		if store.NormName(name) == "" {
			continue
		}
		id := int64(0)
		if r := parseRef(rc.Ref); r != 0 && b.chars[r] != nil {
			id = r
		} else {
			id = b.match(name, rc.Aliases)
		}
		imp := min(max(rc.Importance, 1), 5)
		var c *store.Character
		if id == 0 {
			c = &store.Character{
				BookID: bookID, Name: name, Gender: gender(rc.Gender), AgeGroup: ageGroup(rc.Age),
				Role: clip(rc.Role, 120), Description: clip(rc.Description, 400),
				SpeechStyle: clip(rc.SpeechStyle, 200), Importance: imp, Speaks: rc.Speaks,
			}
			if err := tx.CreateCharacter(ctx, c); err != nil {
				return err
			}
			b.add(c)
		} else {
			c = b.chars[id]
			fields := map[string]any{}
			if imp > c.Importance {
				c.Importance, fields["Importance"] = imp, imp
			}
			if rc.Speaks && !c.Speaks {
				c.Speaks, fields["Speaks"] = true, true
			}
			if !c.UserEdited {
				fill := func(field string, cur *string, v string, n int) {
					if *cur == "" && strings.TrimSpace(v) != "" {
						*cur = clip(v, n)
						fields[field] = *cur
					}
				}
				fill("Role", &c.Role, rc.Role, 120)
				fill("Description", &c.Description, rc.Description, 400)
				fill("SpeechStyle", &c.SpeechStyle, rc.SpeechStyle, 200)
				if g := gender(rc.Gender); c.Gender == store.GenderUnknown && g != store.GenderUnknown {
					c.Gender, fields["Gender"] = g, g
				}
				if a := ageGroup(rc.Age); c.AgeGroup == store.AgeUnknown && a != store.AgeUnknown {
					c.AgeGroup, fields["AgeGroup"] = a, a
				}
			}
			if err := tx.UpdateCharacter(ctx, c.ID, fields); err != nil {
				return err
			}
		}
		aliases := append([]string{name}, rc.Aliases...)
		for i := range aliases {
			aliases[i] = clip(aliases[i], 120)
		}
		if _, err := tx.AddCharacterAliases(ctx, c, aliases...); err != nil {
			return err
		}
		for _, a := range aliases {
			b.addName(c.ID, store.NormName(a))
		}
		var chapterIDs []int64
		for _, n := range rc.Chapters {
			if p, ok := chapterOf(n); ok && !slices.Contains(chapterIDs, p.ChapterID) {
				chapterIDs = append(chapterIDs, p.ChapterID)
			}
		}
		if err := tx.LinkCharacterChapters(ctx, bookID, c.ID, chapterIDs...); err != nil {
			return err
		}
		touched[c.ID] = true
	}

	for _, rch := range res.Chapters {
		p, ok := chapterOf(rch.N)
		// For a chapter split over several batches, the part with its
		// beginning decides.
		if !ok || p.FromPara != 0 {
			continue
		}
		var pov *int64
		if name := strings.TrimSpace(rch.POV); name != "" {
			if id := b.match(name, nil); id != 0 {
				pov = &id
			}
		}
		if err := tx.SetChapterNarration(ctx, p.ChapterID, narration(rch.Narration), pov); err != nil {
			return err
		}
	}

	ids := make([]int64, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	if err := tx.RefreshCharacterStats(ctx, ids...); err != nil {
		return err
	}
	// Keep the in-memory counts close enough for ranking.
	for _, id := range ids {
		if c, err := tx.GetCharacter(ctx, id); err == nil {
			b.chars[id] = c
		}
	}
	return nil
}
