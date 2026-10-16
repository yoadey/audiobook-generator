// Package attribution decides who speaks each quotation of a chapter.
//
// A chapter is attributed just in time, before it is rendered: its text is
// sent to the LLM with every quotation marked ([q12]“…”), together with
// the characters that appear in the chapter and the last speakers of the
// previous one. The answer only labels quotations — the text itself is
// never rewritten. Speakers the model names but the bible lacks ("new:")
// become minor characters. Long chapters are sent in parts (preferably
// split at scene breaks, with a few paragraphs of context); each part is
// stored as soon as it is answered, so a stopped job resumes with the
// next part and never spends quota twice.
package attribution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType attributes one chapter; it runs on the LLM queue.
const (
	JobType = "attribute_chapter"
	Queue   = "llm"
)

// Defaults.
const (
	DefaultPartTokens = 12000
	// UnsureBelow is the confidence under which a speaker is flagged for
	// review.
	UnsureBelow = 0.6
	// contextParas precede a part (after the first) as context.
	contextParas = 3
	// maxCharacters bounds the characters listed per request.
	maxCharacters = 120
	outputTokens  = 8192
	tokensPerWord = 1.35
)

// Emotions the prompt offers.
var emotions = map[string]bool{
	"neutral": true, "happy": true, "sad": true, "angry": true, "afraid": true, "surprised": true,
	"whispering": true, "shouting": true, "sarcastic": true,
}

// Completer sends structured requests (llm.Pool).
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, out any) (*llm.Result, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Attributor runs speaker attributions.
type Attributor struct {
	Store *store.Store
	LLM   Completer
	Jobs  Enqueuer
	// PartTokens is the input size of one request.
	PartTokens int
	Log        *slog.Logger
}

var prompt = llm.MustLoadPrompt("attribute_chapter")

// PromptID is the prompt version attributions use.
func PromptID() string { return prompt.ID() }

func (a *Attributor) partTokens() int {
	if a.PartTokens > 0 {
		return a.PartTokens
	}
	return DefaultPartTokens
}

// Errors of Start.
var (
	ErrBusy     = errors.New("the speakers of this chapter are being attributed")
	ErrNotReady = fmt.Errorf("%w: the character analysis has not finished", store.ErrInvalid)
)

// Active reports whether a chapter's attribution is queued or running.
func Active(c *store.Chapter) bool {
	return c.AttributionStatus == store.AttributionQueued || c.AttributionStatus == store.AttributionRunning
}

// Needed reports whether a chapter must be attributed before it is
// rendered with character voices: the book has a finished analysis, the
// chapter has quotations, and it was not attributed (or failed) yet.
func Needed(b *store.Book, c *store.Chapter) bool {
	return b.AnalysisStatus == store.AnalysisDone && c.QuoteCount > 0 && !c.Excluded &&
		c.AttributionStatus != store.AttributionDone && c.AttributionStatus != store.AttributionFailed
}

// Start queues the attribution of a chapter. With force, the automatic
// speakers are discarded and the chapter is attributed again (speakers the
// user chose stay). priority orders the job on the LLM queue.
func (a *Attributor) Start(ctx context.Context, b *store.Book, c *store.Chapter, force bool, priority int) error {
	if b.AnalysisStatus != store.AnalysisDone {
		return ErrNotReady
	}
	pending, err := a.Store.PendingChapterJob(ctx, JobType, c.ID)
	if err != nil {
		return err
	}
	if pending != nil {
		if force {
			return ErrBusy
		}
		return nil
	}
	if force {
		if err := a.Store.ResetAttribution(ctx, c.ID); err != nil {
			return err
		}
	} else if c.AttributionStatus == store.AttributionDone {
		return nil
	}
	if ok, err := a.Store.TransitionAttribution(ctx, c.ID, store.AttributionQueued,
		map[string]any{"attribution_error": ""},
		store.AttributionNone, store.AttributionFailed, store.AttributionQueued, store.AttributionRunning); err != nil {
		return err
	} else if !ok {
		return nil
	}
	return a.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &b.ID, ChapterID: &c.ID, Priority: priority, MaxAttempts: 5,
	})
}

type partDoneKey struct{}

// Handler is the job handler for JobType.
func (a *Attributor) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.ChapterID == nil {
				return jobs.Permanent(errors.New("attribution job without chapter"))
			}
			// Attempts count failures of the current part.
			ctx = context.WithValue(ctx, partDoneKey{}, func(ctx context.Context) {
				j.Attempts = 1
				if err := a.Store.ResetJobAttempts(ctx, j.ID); err != nil {
					a.Log.Warn("reset job attempts", "job", j.ID, "err", err)
				}
			})
			return a.Run(ctx, *j.ChapterID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.ChapterID == nil {
				return
			}
			if _, uerr := a.Store.TransitionAttribution(ctx, *j.ChapterID, store.AttributionFailed,
				map[string]any{"attribution_error": store.CleanText(err.Error())},
				store.AttributionQueued, store.AttributionRunning); uerr != nil {
				a.Log.Error("mark attribution failed", "chapter", *j.ChapterID, "err", uerr)
			}
		},
	}
}

// Run attributes the remaining parts of a chapter.
func (a *Attributor) Run(ctx context.Context, chapterID int64, progress func(float64)) error {
	ch, err := a.Store.GetChapter(ctx, chapterID)
	if err != nil {
		return jobs.Permanent(err)
	}
	b, err := a.Store.GetBook(ctx, ch.BookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if b.AnalysisStatus != store.AnalysisDone {
		return jobs.Permanent(errors.New("the character analysis has not finished"))
	}
	if ok, err := a.Store.TransitionAttribution(ctx, chapterID, store.AttributionRunning, nil,
		store.AttributionQueued, store.AttributionRunning); err != nil || !ok {
		return err // reset or done meanwhile
	}
	paras, segs, err := a.Store.ChapterText(ctx, chapterID)
	if err != nil {
		return err
	}
	parts := split(paras, segs, a.partTokens())
	ctxData, err := a.loadContext(ctx, b, ch)
	if err != nil {
		return err
	}
	for i := ch.AttributionPart; i < len(parts); i++ {
		progress(float64(i) / float64(len(parts)))
		p := parts[i]
		res, err := a.attribute(ctx, ch, ctxData, paras, segs, p.from, p.to, p.from-min(p.from, contextParas))
		if err != nil {
			return err
		}
		if err := a.Store.ApplyAttribution(ctx, chapterID, res, i+1); err != nil {
			return err
		}
		ctxData.remember(res)
		if done, ok := ctx.Value(partDoneKey{}).(func(context.Context)); ok {
			done(ctx)
		}
	}
	progress(1)
	_, err = a.Store.MarkAttributed(ctx, chapterID, prompt.ID())
	return err
}

// part is a range of paragraphs [from, to) sent in one request.
type part struct{ from, to int }

func paraTokens(p store.Paragraph) int { return int(float64(p.WordCount)*tokensPerWord) + 4 }

// starts maps each quotation to the paragraph it begins in (a quotation
// may run over several paragraphs; it belongs to its first).
func starts(segs []store.Segment) map[int]int64 {
	out := map[int]int64{}
	for _, s := range segs {
		if _, ok := out[s.QuoteRef]; s.QuoteRef > 0 && !ok {
			out[s.QuoteRef] = s.ParagraphID
		}
	}
	return out
}

// split cuts the chapter into parts of about budget tokens that contain
// quotations, preferring scene breaks once a part is half full.
func split(paras []store.Paragraph, segs []store.Segment, budget int) []part {
	quoted := map[int64]bool{}
	for _, p := range starts(segs) {
		quoted[p] = true
	}
	var out []part
	start, tokens, hasQuote := 0, 0, false
	flush := func(end int) {
		if hasQuote {
			out = append(out, part{start, end})
		}
		start, tokens, hasQuote = end, 0, false
	}
	for i, p := range paras {
		t := paraTokens(p)
		if tokens > 0 && (tokens+t > budget || p.Kind == store.ParagraphSceneBreak && tokens > budget/2) {
			flush(i)
		}
		tokens += t
		hasQuote = hasQuote || quoted[p.ID]
	}
	flush(len(paras))
	return out
}

// chapterContext is what every request of a chapter shares.
type chapterContext struct {
	chars    []store.Character
	aliases  map[int64][]store.CharacterAlias
	listed   map[int64]bool
	byNorm   map[string][]int64
	previous []store.Speaker
	names    map[int64]string
}

func (a *Attributor) loadContext(ctx context.Context, b *store.Book, ch *store.Chapter) (*chapterContext, error) {
	all, err := a.Store.BookCharacters(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	aliases, err := a.Store.BookAliases(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	appear, err := a.Store.ChapterCharacters(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	previous, err := a.Store.LastSpeakers(ctx, b.ID, ch.Idx, 5)
	if err != nil {
		return nil, err
	}
	c := &chapterContext{aliases: map[int64][]store.CharacterAlias{}, listed: map[int64]bool{},
		byNorm: map[string][]int64{}, previous: previous, names: map[int64]string{}}
	byID := map[int64]*store.Character{}
	for i := range all {
		byID[all[i].ID] = &all[i]
		c.names[all[i].ID] = all[i].Name
		c.addNorm(all[i].ID, all[i].Name)
	}
	for _, al := range aliases {
		c.aliases[al.CharacterID] = append(c.aliases[al.CharacterID], al)
		c.byNorm[al.Norm] = appendUnique(c.byNorm[al.Norm], al.CharacterID)
	}
	// The characters of the chapter, the point-of-view character and the
	// previous speakers (the scene may continue).
	add := func(id int64) {
		if ch := byID[id]; ch != nil && !c.listed[id] && len(c.chars) < maxCharacters {
			c.listed[id] = true
			c.chars = append(c.chars, *ch)
		}
	}
	if ch.POVCharacterID != nil {
		add(*ch.POVCharacterID)
	}
	for _, s := range previous {
		add(s.CharacterID)
	}
	for _, x := range appear {
		add(x.ID)
	}
	return c, nil
}

func (c *chapterContext) addNorm(id int64, name string) {
	if n := store.NormName(name); n != "" {
		c.byNorm[n] = appendUnique(c.byNorm[n], id)
	}
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

// remember makes the last speakers of a part the context of the next.
func (c *chapterContext) remember(res []store.Attribution) {
	var prev []store.Speaker
	seen := map[int64]bool{}
	for i := len(res) - 1; i >= 0 && len(prev) < 5; i-- {
		if id := res[i].CharacterID; id != nil && !seen[*id] {
			seen[*id] = true
			prev = append(prev, store.Speaker{CharacterID: *id, Name: c.names[*id]})
		}
	}
	if len(prev) > 0 {
		c.previous = prev
	}
}

func (c *chapterContext) lines() []string {
	out := make([]string, 0, len(c.chars))
	for _, ch := range c.chars {
		var as []string
		for _, al := range c.aliases[ch.ID] {
			as = append(as, al.Alias)
		}
		out = append(out, fmt.Sprintf("c%d | %s | %s | %s", ch.ID, ch.Name, strings.Join(as, ", "), ch.Gender))
	}
	return out
}

// marked renders paragraphs [from, to) with every quotation marked by its
// number; context paragraphs [ctxFrom, from) are rendered plain.
func marked(paras []store.Paragraph, segs []store.Segment, ctxFrom, from, to int) (context, text string, refs []int) {
	byPara := map[int64][]store.Segment{}
	for _, s := range segs {
		byPara[s.ParagraphID] = append(byPara[s.ParagraphID], s)
	}
	var cb, tb strings.Builder
	seen := map[int]bool{}
	first := starts(segs)
	for i := ctxFrom; i < to; i++ {
		p := paras[i]
		b := &tb
		if i < from {
			b = &cb
		}
		if p.Kind == store.ParagraphSceneBreak {
			b.WriteString("* * *\n\n")
			continue
		}
		pieces := byPara[p.ID]
		if len(pieces) == 0 {
			b.WriteString(p.Text)
			b.WriteString("\n\n")
			continue
		}
		for k, s := range pieces {
			if k > 0 {
				b.WriteString(" ")
			}
			if i >= from && s.QuoteRef > 0 && !seen[s.QuoteRef] && first[s.QuoteRef] == p.ID {
				seen[s.QuoteRef] = true
				refs = append(refs, s.QuoteRef)
				fmt.Fprintf(b, "[q%d]", s.QuoteRef)
			}
			b.WriteString(s.Text)
		}
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(cb.String()), strings.TrimSpace(tb.String()), refs
}

type answer struct {
	Quotes []struct {
		Q          int     `json:"q"`
		Speaker    string  `json:"speaker"`
		Gender     string  `json:"gender"`
		Emotion    string  `json:"emotion"`
		Confidence float64 `json:"confidence"`
	} `json:"quotes"`
}

// attribute asks for the speakers of paragraphs [from, to), splitting the
// range when the request or answer is too large; a passage a content
// filter still refuses on its own is left unattributed.
func (a *Attributor) attribute(ctx context.Context, ch *store.Chapter, c *chapterContext,
	paras []store.Paragraph, segs []store.Segment, from, to, ctxFrom int,
) ([]store.Attribution, error) {
	contextText, text, refs := marked(paras, segs, ctxFrom, from, to)
	if len(refs) == 0 {
		return nil, nil
	}
	prev := make([]string, 0, len(c.previous))
	for _, s := range c.previous {
		prev = append(prev, fmt.Sprintf("%s (c%d)", s.Name, s.CharacterID))
	}
	req, err := prompt.Render(map[string]any{
		"Characters": c.lines(), "Previous": strings.Join(prev, ", "), "Title": ch.Title,
		"Context": contextText, "Count": len(refs), "Text": text,
	})
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	req.MaxTokens = outputTokens
	var ans answer
	_, err = a.LLM.CompleteJSON(ctx, req, &ans)
	var (
		tooLarge *llm.TooLargeError
		invalid  *llm.InvalidOutputError
		rejected *llm.RejectedError
	)
	blocked := (errors.As(err, &invalid) && invalid.Blocked) || (errors.As(err, &rejected) && rejected.Blocked())
	if blocked || errors.As(err, &tooLarge) || (errors.As(err, &invalid) && invalid.Truncated) ||
		(errors.As(err, &rejected) && rejected.TooLong()) {
		if to-from < 2 {
			a.Log.Warn("passage cannot be attributed, its quotes stay with the narrator",
				"chapter", ch.ID, "paragraph", from, "err", err)
			return unknown(refs), nil
		}
		mid := from + (to-from)/2
		first, err := a.attribute(ctx, ch, c, paras, segs, from, mid, ctxFrom)
		if err != nil {
			return nil, err
		}
		second, err := a.attribute(ctx, ch, c, paras, segs, mid, to, mid-min(mid-from, contextParas))
		if err != nil {
			return nil, err
		}
		return append(first, second...), nil
	}
	if errors.As(err, &invalid) {
		a.Log.Warn("attribution answer unusable, its quotes stay with the narrator", "chapter", ch.ID, "err", err)
		return unknown(refs), nil
	}
	if errors.As(err, &rejected) {
		return nil, jobs.Permanent(err)
	}
	if err != nil {
		return nil, err
	}
	return a.apply(ctx, ch, c, refs, &ans)
}

func unknown(refs []int) []store.Attribution {
	out := make([]store.Attribution, len(refs))
	for i, r := range refs {
		out[i] = store.Attribution{QuoteRef: r}
	}
	return out
}

// apply validates an answer: every sent quotation gets a result (unknown
// with confidence 0 when missing or invalid); "new:" speakers are matched
// by name or created as minor characters.
func (a *Attributor) apply(ctx context.Context, ch *store.Chapter, c *chapterContext, refs []int,
	ans *answer,
) ([]store.Attribution, error) {
	sent := map[int]bool{}
	for _, r := range refs {
		sent[r] = true
	}
	got := map[int]store.Attribution{}
	for _, q := range ans.Quotes {
		if !sent[q.Q] {
			continue
		}
		at := store.Attribution{QuoteRef: q.Q, Emotion: "neutral", Confidence: clamp(q.Confidence)}
		if e := strings.ToLower(strings.TrimSpace(q.Emotion)); emotions[e] {
			at.Emotion = e
		}
		speaker := strings.TrimSpace(q.Speaker)
		switch {
		case strings.EqualFold(speaker, "unknown") || speaker == "":
		case strings.HasPrefix(strings.ToLower(speaker), "new:"):
			id, err := a.newSpeaker(ctx, ch, c, strings.TrimSpace(speaker[4:]), q.Gender)
			if err != nil {
				return nil, err
			}
			at.CharacterID = id
		default:
			id, err := strconv.ParseInt(strings.TrimPrefix(speaker, "c"), 10, 64)
			if err == nil && c.listed[id] {
				at.CharacterID = &id
			} else {
				at.Confidence = 0 // an id that was not offered
			}
		}
		if at.CharacterID == nil {
			at.Confidence = min(at.Confidence, UnsureBelow/2)
		}
		got[q.Q] = at
	}
	out := make([]store.Attribution, 0, len(refs))
	for _, r := range refs {
		at, ok := got[r]
		if !ok {
			at = store.Attribution{QuoteRef: r}
		}
		out = append(out, at)
	}
	return out, nil
}

func clamp(f float64) float64 {
	if math.IsNaN(f) {
		return 0
	}
	return min(max(f, 0), 1)
}

// newSpeaker finds a named speaker in the bible or adds it as a minor
// character of this chapter.
func (a *Attributor) newSpeaker(ctx context.Context, ch *store.Chapter, c *chapterContext, name, gender string,
) (*int64, error) {
	name = strings.TrimSpace(name)
	norm := store.NormName(name)
	if norm == "" || len([]rune(name)) > 120 {
		return nil, nil
	}
	if ids := c.byNorm[norm]; len(ids) == 1 {
		id := ids[0]
		c.listed[id] = true
		return &id, nil
	} else if len(ids) > 1 {
		return nil, nil // ambiguous: leave it to the narrator
	}
	g := store.Gender(strings.ToLower(gender))
	if g != store.GenderFemale && g != store.GenderMale {
		g = store.GenderUnknown
	}
	nc := &store.Character{BookID: ch.BookID, Name: name, Gender: g, AgeGroup: store.AgeUnknown,
		Importance: 1, Speaks: true, Tier: store.TierMinor}
	if err := a.Store.CreateCharacter(ctx, nc); err != nil {
		return nil, err
	}
	if err := a.Store.LinkCharacterChapters(ctx, ch.BookID, nc.ID, ch.ID); err != nil {
		return nil, err
	}
	if err := a.Store.RefreshCharacterStats(ctx, nc.ID); err != nil {
		return nil, err
	}
	a.Log.Info("new character from attribution", "chapter", ch.ID, "name", name)
	c.listed[nc.ID] = true
	c.names[nc.ID] = name
	c.addNorm(nc.ID, name)
	return &nc.ID, nil
}

// pollEvery is how often a render waiting for its attribution looks again.
const pollEvery = 30 * time.Second

// WaitError defers a render until its chapter is attributed.
func WaitError() error {
	return jobs.Defer(time.Now().Add(pollEvery), "waiting for the speaker attribution of this chapter")
}
