// Package prepare prepares a chapter for reading aloud.
//
// Like the speaker attribution, a chapter is prepared just in time, before
// it is rendered, when its book asks for it. The LLM reads the chapter and
// returns compact labels — it never rewrites the text: the mood of the
// narration per paragraph, dramatic pauses (anchored by the words before
// them), spoken forms for spots a TTS reads wrongly ("Lv. 5" → "level
// five"), and pronunciations of invented names as suggestions for the
// book's lexicon. Everything is checked against the text and applies to
// the TTS input only; the book text stays as it is. Long chapters go in
// parts, each stored with its answer, so a stopped job resumes with the
// next part and never spends quota twice.
package prepare

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType prepares one chapter; it runs on the LLM queue.
const (
	JobType = "prepare_chapter"
	Queue   = "llm"
)

// Defaults.
const (
	DefaultPartTokens = 10000
	outputTokens      = 4096
	tokensPerWord     = 1.35
	// maxKnown bounds the lexicon terms listed as known.
	maxKnown = 150
)

// Completer sends structured requests (llm.Pool).
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, out any) (*llm.Result, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Preparer runs chapter preparations.
type Preparer struct {
	Store *store.Store
	LLM   Completer
	Jobs  Enqueuer
	// PartTokens is the input size of one request.
	PartTokens int
	Log        *slog.Logger
}

var prompt = llm.MustLoadPrompt("prepare_chapter")

// PromptID is the prompt version preparations use.
func PromptID() string { return prompt.ID() }

func (p *Preparer) partTokens() int {
	if p.PartTokens > 0 {
		return p.PartTokens
	}
	return DefaultPartTokens
}

// ErrBusy is returned by a forced Start while a preparation of the chapter
// is queued or running.
var ErrBusy = errors.New("this chapter is being prepared")

// Active reports whether a chapter's preparation is queued or running.
func Active(c *store.Chapter) bool {
	return c.PrepStatus == store.PrepQueued || c.PrepStatus == store.PrepRunning
}

// Needed reports whether a chapter must be prepared before it is rendered:
// its book asks for it, and it was not prepared (or failed) yet.
func Needed(b *store.Book, c *store.Chapter) bool {
	return b.PrepareChapters && !c.Excluded && c.PrepStatus != store.PrepDone && c.PrepStatus != store.PrepFailed
}

// Start queues the preparation of a chapter. With force, what an earlier
// preparation found is discarded and the chapter is prepared again.
// priority orders the job on the LLM queue.
func (p *Preparer) Start(ctx context.Context, b *store.Book, c *store.Chapter, force bool, priority int) error {
	pending, err := p.Store.PendingChapterJob(ctx, JobType, c.ID)
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
		if err := p.Store.ResetPreparation(ctx, c.ID); err != nil {
			return err
		}
	} else if c.PrepStatus == store.PrepDone {
		return nil
	}
	// From scratch after a reset; a failed or interrupted one resumes.
	fresh, err := p.Store.TransitionPrep(ctx, c.ID, store.PrepQueued,
		map[string]any{"prep_error": "", "prep_part": 0}, store.PrepNone)
	if err != nil {
		return err
	}
	if !fresh {
		ok, err := p.Store.TransitionPrep(ctx, c.ID, store.PrepQueued, map[string]any{"prep_error": ""},
			store.PrepFailed, store.PrepQueued, store.PrepRunning)
		if err != nil || !ok {
			return err
		}
	}
	return p.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &b.ID, ChapterID: &c.ID, Priority: priority, MaxAttempts: 5,
	})
}

type partDoneKey struct{}

// Handler is the job handler for JobType.
func (p *Preparer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.ChapterID == nil {
				return jobs.Permanent(errors.New("preparation job without chapter"))
			}
			// Attempts count failures of the current part.
			ctx = context.WithValue(ctx, partDoneKey{}, func(ctx context.Context) {
				j.Attempts = 1
				if err := p.Store.ResetJobAttempts(ctx, j.ID); err != nil {
					p.Log.Warn("reset job attempts", "job", j.ID, "err", err)
				}
			})
			return p.Run(ctx, *j.ChapterID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.ChapterID == nil {
				return
			}
			// A newer start queued another job: that one decides.
			if q, qerr := p.Store.PendingChapterJob(ctx, JobType, *j.ChapterID); qerr == nil && q != nil && q.ID != j.ID {
				return
			}
			if _, uerr := p.Store.TransitionPrep(ctx, *j.ChapterID, store.PrepFailed,
				map[string]any{"prep_error": store.CleanText(err.Error())},
				store.PrepQueued, store.PrepRunning); uerr != nil {
				p.Log.Error("mark preparation failed", "chapter", *j.ChapterID, "err", uerr)
			}
		},
	}
}

// Run prepares the remaining parts of a chapter.
func (p *Preparer) Run(ctx context.Context, chapterID int64, progress func(float64)) error {
	ch, err := p.Store.GetChapter(ctx, chapterID)
	if err != nil {
		return jobs.Permanent(err)
	}
	b, err := p.Store.GetBook(ctx, ch.BookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if ch.Excluded {
		_, err := p.Store.TransitionPrep(ctx, chapterID, store.PrepNone, nil, store.PrepQueued, store.PrepRunning)
		return err // not narrated: nothing to prepare
	}
	if ok, err := p.Store.TransitionPrep(ctx, chapterID, store.PrepRunning, nil,
		store.PrepQueued, store.PrepRunning); err != nil || !ok {
		return err // reset or done meanwhile
	}
	paras, segs, err := p.Store.ChapterText(ctx, chapterID)
	if err != nil {
		return err
	}
	done := ch.PrepPart
	var parts []part
	for _, pt := range split(paras, p.partTokens()) {
		if pt.to <= done {
			continue
		}
		pt.from = max(pt.from, done)
		parts = append(parts, pt)
	}
	for i, pt := range parts {
		progress(float64(i) / float64(len(parts)))
		known, err := p.known(ctx, b.ID)
		if err != nil {
			return err
		}
		prep, err := p.prepare(ctx, b, ch, known, paras, segs, pt.from, pt.to)
		if err != nil {
			return err
		}
		err = p.Store.ApplyPreparation(ctx, chapterID, prep, done, pt.to)
		if errors.Is(err, store.ErrStalePrep) {
			p.Log.Info("preparation reset meanwhile, stopping", "chapter", chapterID)
			return nil
		}
		if err != nil {
			return err
		}
		done = pt.to
		if f, ok := ctx.Value(partDoneKey{}).(func(context.Context)); ok {
			f(ctx)
		}
	}
	progress(1)
	_, err = p.Store.MarkPrepared(ctx, chapterID, prompt.ID())
	return err
}

// known lists the book's lexicon terms (in any state: suggested or
// dismissed ones are not suggested again).
func (p *Preparer) known(ctx context.Context, bookID int64) ([]string, error) {
	es, err := p.Store.ListLexicon(ctx, bookID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, min(len(es), maxKnown))
	for _, e := range es {
		if len(out) == maxKnown {
			break
		}
		out = append(out, e.Term)
	}
	return out, nil
}

// part is a range of paragraphs [from, to) sent in one request.
type part struct{ from, to int }

// split cuts the chapter into parts of about budget tokens, preferring
// scene breaks once a part is half full.
func split(paras []store.Paragraph, budget int) []part {
	var out []part
	start, tokens := 0, 0
	for i, p := range paras {
		t := int(float64(p.WordCount)*tokensPerWord) + 4
		if tokens > 0 && (tokens+t > budget || p.Kind == store.ParagraphSceneBreak && tokens > budget/2) {
			out = append(out, part{start, i})
			start, tokens = i, 0
		}
		tokens += t
	}
	if start < len(paras) {
		out = append(out, part{start, len(paras)})
	}
	return out
}

// numbered renders paragraphs [from, to) as "[pN] text" (N: the index in
// the chapter, 1-based); scene breaks are left out.
func numbered(paras []store.Paragraph, from, to int) (string, int) {
	var b strings.Builder
	n := 0
	for i := from; i < to; i++ {
		if paras[i].Kind == store.ParagraphSceneBreak || strings.TrimSpace(paras[i].Text) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "[p%d] %s\n\n", i+1, paras[i].Text)
	}
	return strings.TrimSpace(b.String()), n
}

// prepare asks about paragraphs [from, to), splitting the range when the
// request or answer is too large; a passage a content filter still
// refuses on its own is left as it is.
func (p *Preparer) prepare(ctx context.Context, b *store.Book, ch *store.Chapter, known []string,
	paras []store.Paragraph, segs []store.Segment, from, to int,
) (store.Preparation, error) {
	text, n := numbered(paras, from, to)
	if n == 0 {
		return store.Preparation{}, nil
	}
	req, err := prompt.Render(map[string]any{
		"Book": b.Title, "Title": ch.Title, "Known": strings.Join(known, ", "), "Count": n, "Text": text,
	})
	if err != nil {
		return store.Preparation{}, jobs.Permanent(err)
	}
	req.MaxTokens = outputTokens
	var ans answer
	_, err = p.LLM.CompleteJSON(ctx, req, &ans)
	var (
		tooLarge *llm.TooLargeError
		invalid  *llm.InvalidOutputError
		rejected *llm.RejectedError
	)
	blocked := (errors.As(err, &invalid) && invalid.Blocked) || (errors.As(err, &rejected) && rejected.Blocked())
	if blocked || errors.As(err, &tooLarge) || (errors.As(err, &invalid) && invalid.Truncated) ||
		(errors.As(err, &rejected) && rejected.TooLong()) {
		if to-from < 2 {
			p.Log.Warn("passage cannot be prepared, it is read as it is", "chapter", ch.ID, "paragraph", from, "err", err)
			return store.Preparation{}, nil
		}
		mid := from + (to-from)/2
		first, err := p.prepare(ctx, b, ch, known, paras, segs, from, mid)
		if err != nil {
			return store.Preparation{}, err
		}
		second, err := p.prepare(ctx, b, ch, known, paras, segs, mid, to)
		if err != nil {
			return store.Preparation{}, err
		}
		return merge(first, second), nil
	}
	if errors.As(err, &invalid) {
		p.Log.Warn("preparation answer unusable, the passage is read as it is", "chapter", ch.ID, "err", err)
		return store.Preparation{}, nil
	}
	if errors.As(err, &rejected) {
		return store.Preparation{}, jobs.Permanent(err)
	}
	if err != nil {
		return store.Preparation{}, err
	}
	return interpret(paras, segs, from, to, known, &ans), nil
}

func merge(a, b store.Preparation) store.Preparation {
	if a.Moods == nil {
		a.Moods = map[int64]string{}
	}
	for k, v := range b.Moods {
		a.Moods[k] = v
	}
	a.Segments = append(a.Segments, b.Segments...)
	seen := map[string]bool{}
	for _, e := range a.Suggestions {
		seen[strings.ToLower(e.Term)] = true
	}
	for _, e := range b.Suggestions {
		if !seen[strings.ToLower(e.Term)] {
			a.Suggestions = append(a.Suggestions, e)
		}
	}
	return a
}

// pollEvery is how often a render waiting for its preparation looks again.
const pollEvery = 30 * time.Second

// WaitError defers a render until its chapter is prepared.
func WaitError() error {
	return jobs.Defer(time.Now().Add(pollEvery), "waiting for the preparation of this chapter")
}
