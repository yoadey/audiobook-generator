package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// part is a run of paragraphs of one chapter (ToPara exclusive, 0 = to
// the end of the chapter).
type part struct {
	ChapterID int64 `json:"chapterId"`
	FromPara  int   `json:"fromPara,omitempty"`
	ToPara    int   `json:"toPara,omitempty"`
}

// tokensPerWord estimates input tokens of English prose.
const tokensPerWord = 1.35

func wordTokens(words int) int { return int(float64(words)*tokensPerWord) + 1 }

// planned is a batch before it is stored.
type planned struct {
	parts   []part
	tokens  int
	chapter int // index of the first chapter
	para    int // first paragraph
}

// plan packs the narrated chapters of a book into batches of about
// budget tokens: whole chapters where possible, oversized chapters split
// at paragraph boundaries.
func plan(ctx context.Context, st *store.Store, bookID int64, budget int) ([]store.AnalysisBatch, error) {
	chapters, _, err := st.ListChapters(ctx, bookID, store.ChapterFilter{}, 0, 1<<30)
	if err != nil {
		return nil, err
	}
	var out []planned
	var cur *planned
	flush := func() {
		if cur != nil && len(cur.parts) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, c := range chapters {
		if c.Excluded {
			continue
		}
		t := wordTokens(c.WordCount)
		if t <= budget {
			if cur != nil && cur.tokens+t > budget {
				flush()
			}
			if cur == nil {
				cur = &planned{chapter: c.Idx}
			}
			cur.parts = append(cur.parts, part{ChapterID: c.ID})
			cur.tokens += t
			continue
		}
		// Too big for one batch: whole batches of its paragraphs.
		flush()
		paras, _, err := st.ChapterText(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		from, tokens := 0, 0
		for i, p := range paras {
			pt := wordTokens(p.WordCount)
			if tokens > 0 && tokens+pt > budget {
				out = append(out, planned{parts: []part{{ChapterID: c.ID, FromPara: from, ToPara: i}},
					tokens: tokens, chapter: c.Idx, para: from})
				from, tokens = i, 0
			}
			tokens += pt
		}
		out = append(out, planned{parts: []part{{ChapterID: c.ID, FromPara: from}},
			tokens: tokens, chapter: c.Idx, para: from})
	}
	flush()

	batches := make([]store.AnalysisBatch, 0, len(out))
	for _, p := range out {
		b, err := toBatch(bookID, p)
		if err != nil {
			return nil, err
		}
		batches = append(batches, b)
	}
	return batches, nil
}

func toBatch(bookID int64, p planned) (store.AnalysisBatch, error) {
	raw, err := json.Marshal(p.parts)
	if err != nil {
		return store.AnalysisBatch{}, err
	}
	return store.AnalysisBatch{
		BookID: bookID, FirstChapterIdx: p.chapter, FirstParaIdx: p.para,
		Parts: string(raw), Tokens: p.tokens, Status: store.BatchPending,
	}, nil
}

func parseParts(b *store.AnalysisBatch) ([]part, error) {
	var ps []part
	if err := json.Unmarshal([]byte(b.Parts), &ps); err != nil {
		return nil, fmt.Errorf("batch %d: parts: %w", b.ID, err)
	}
	return ps, nil
}

// chapterPart is a part with its loaded text.
type chapterPart struct {
	part
	chapter    store.Chapter
	paragraphs []store.Paragraph
	whole      bool // the part is the whole chapter
}

// load reads the parts' text; chapters deleted meanwhile are skipped.
func load(ctx context.Context, st *store.Store, ps []part) ([]chapterPart, error) {
	var out []chapterPart
	for _, p := range ps {
		c, err := st.GetChapter(ctx, p.ChapterID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		paras, _, err := st.ChapterText(ctx, p.ChapterID)
		if err != nil {
			return nil, err
		}
		to := p.ToPara
		if to <= 0 || to > len(paras) {
			to = len(paras)
		}
		from := min(max(p.FromPara, 0), to)
		out = append(out, chapterPart{
			part: p, chapter: *c, paragraphs: paras[from:to],
			whole: from == 0 && to == len(paras),
		})
	}
	return out, nil
}

// render formats the parts as numbered chapters for the prompt.
func render(parts []chapterPart) string {
	var b strings.Builder
	for i, p := range parts {
		title := p.chapter.Title
		if !p.whole {
			title += fmt.Sprintf(" (part: paragraphs %d–%d)", p.FromPara+1, p.FromPara+len(p.paragraphs))
		}
		fmt.Fprintf(&b, "=== [%d] %s ===\n\n", i+1, title)
		for _, para := range p.paragraphs {
			switch para.Kind {
			case store.ParagraphSceneBreak:
				b.WriteString("* * *\n\n")
			default:
				b.WriteString(para.Text)
				b.WriteString("\n\n")
			}
		}
	}
	return b.String()
}

// halves splits a batch in two (by parts, or by the paragraphs of a
// single part); ok is false when it cannot be split any further.
func halves(bookID int64, ps []part, loaded []chapterPart) (first, second store.AnalysisBatch, ok bool, err error) {
	var a, b planned
	switch {
	case len(loaded) >= 2:
		mid := len(loaded) / 2
		a = plannedOf(loaded[:mid])
		b = plannedOf(loaded[mid:])
	case len(loaded) == 1 && len(loaded[0].paragraphs) >= 2:
		p := loaded[0]
		mid := p.FromPara + len(p.paragraphs)/2
		end := p.FromPara + len(p.paragraphs)
		left := chapterPart{part: part{ChapterID: p.ChapterID, FromPara: p.FromPara, ToPara: mid},
			chapter: p.chapter, paragraphs: p.paragraphs[:mid-p.FromPara]}
		right := chapterPart{part: part{ChapterID: p.ChapterID, FromPara: mid, ToPara: end},
			chapter: p.chapter, paragraphs: p.paragraphs[mid-p.FromPara:]}
		a, b = plannedOf([]chapterPart{left}), plannedOf([]chapterPart{right})
	default:
		return first, second, false, nil
	}
	if first, err = toBatch(bookID, a); err != nil {
		return
	}
	second, err = toBatch(bookID, b)
	return first, second, true, err
}

func plannedOf(parts []chapterPart) planned {
	p := planned{chapter: parts[0].chapter.Idx, para: parts[0].FromPara}
	for _, cp := range parts {
		words := 0
		for _, para := range cp.paragraphs {
			words += para.WordCount
		}
		p.parts = append(p.parts, cp.part)
		p.tokens += wordTokens(words)
	}
	return p
}
