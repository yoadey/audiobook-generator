// Package analysis builds a book's character bible with an LLM.
//
// The narrated chapters are packed into batches of roughly BatchTokens
// tokens and sent in reading order, each with the known characters the
// batch text mentions, so the model reuses names instead of inventing
// duplicates. Answers are merged into the bible immediately (one
// transaction per batch), so the job can stop at any point: quota
// exhaustion defers it, a restart resumes with the next batch. After the
// last batch, likely duplicates ("Erin" / "Erin Solstice") are checked by
// the LLM in groups and merged or marked distinct.
package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType analyzes a book; it runs on the LLM queue (one at a time, the
// providers' quotas are shared).
const (
	JobType = "analyze_book"
	Queue   = "llm"
)

// Defaults.
const (
	DefaultBatchTokens = 30000
	// Output budget per batch: generous for crowded chapters, still
	// within what common free models allow.
	batchOutputTokens = 8192
	// Known characters sent with a batch at most.
	maxKnown = 250
	// OverheadTokens estimates the prompt overhead per batch
	// (instructions, known characters).
	OverheadTokens = 2500
)

// Completer sends structured requests (llm.Pool).
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, out any) (*llm.Result, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Analyzer runs character analyses.
type Analyzer struct {
	Store *store.Store
	LLM   Completer
	Jobs  Enqueuer
	// BatchTokens is the target input size of a batch.
	BatchTokens int
	// MergePairs bounds the duplicate checks per run, MergeGroup the
	// pairs per request (defaults 1500 and 20).
	MergePairs, MergeGroup int
	Log                    *slog.Logger
}

var analysisPrompt = llm.MustLoadPrompt("character_analysis")

// PromptID is the prompt version new batches are analysed with.
func PromptID() string { return analysisPrompt.ID() }

func (a *Analyzer) batchTokens() int {
	if a.BatchTokens > 0 {
		return a.BatchTokens
	}
	return DefaultBatchTokens
}

func (a *Analyzer) maxMergePairs() int {
	if a.MergePairs > 0 {
		return a.MergePairs
	}
	return 1500
}

func (a *Analyzer) mergeGroupSize() int {
	if a.MergeGroup > 0 {
		return a.MergeGroup
	}
	return 20
}

// ErrBusy is returned for changes while an analysis is queued or running.
var ErrBusy = errors.New("the character analysis of this book is in progress")

// Active reports whether an analysis is queued or running by its status.
func Active(b *store.Book) bool {
	switch b.AnalysisStatus {
	case store.AnalysisQueued, store.AnalysisRunning, store.AnalysisMerging:
		return true
	}
	return false
}

// Busy reports whether a book's characters and chapters must not change:
// the analysis is active, or its job is still running (a paused analysis
// finishes its current batch first).
func (a *Analyzer) Busy(ctx context.Context, b *store.Book) (bool, error) {
	if Active(b) {
		return true, nil
	}
	j, err := a.Store.PendingBookJob(ctx, JobType, b.ID)
	if err != nil {
		return false, err
	}
	return j != nil && j.Status == store.JobRunning, nil
}

// Unfinished reports whether a started analysis still has batches to do:
// chapters must not be merged or split then, the pending batches refer to
// them.
func (a *Analyzer) Unfinished(ctx context.Context, bookID int64) (bool, error) {
	st, err := a.Store.AnalysisStats(ctx, bookID)
	if err != nil {
		return false, err
	}
	return st.Done < st.Total, nil
}

// Start queues the analysis of a book, or resumes it. With restart, all
// previous results (including manual corrections) are discarded first.
func (a *Analyzer) Start(ctx context.Context, b *store.Book, restart bool) error {
	if b.Status != store.BookImported && b.Status != store.BookReady {
		return fmt.Errorf("%w: the book is %s", store.ErrInvalid, b.Status)
	}
	pending, err := a.Store.PendingBookJob(ctx, JobType, b.ID)
	if err != nil {
		return err
	}
	if pending != nil {
		if restart {
			return ErrBusy
		}
		// A job is still there (e.g. finishing its batch after a pause):
		// let it carry on instead of stopping.
		if !Active(b) {
			return a.Store.UpdateBook(ctx, b.ID, map[string]any{
				"AnalysisStatus": store.AnalysisQueued, "AnalysisError": "",
			})
		}
		return nil
	}
	if restart {
		if err := a.Store.ResetAnalysis(ctx, b.ID); err != nil {
			return err
		}
	}
	if err := a.Store.UpdateBook(ctx, b.ID, map[string]any{
		"AnalysisStatus": store.AnalysisQueued, "AnalysisError": "", "AnalysisFinishedAt": nil,
	}); err != nil {
		return err
	}
	return a.Jobs.Enqueue(ctx, &store.Job{Type: JobType, BookID: &b.ID, MaxAttempts: 5})
}

// Pause stops a queued analysis now and a running one after its current
// batch. Results so far are kept; Start resumes.
func (a *Analyzer) Pause(ctx context.Context, b *store.Book) error {
	pending, err := a.Store.PendingBookJob(ctx, JobType, b.ID)
	if err != nil {
		return err
	}
	if pending != nil && pending.Status == store.JobQueued {
		if err := a.Store.CancelJob(ctx, pending.ID); err != nil && !errors.Is(err, store.ErrInvalid) {
			return err
		}
	}
	if !Active(b) {
		return nil
	}
	return a.Store.UpdateBook(ctx, b.ID, map[string]any{"AnalysisStatus": store.AnalysisPaused})
}

// Estimate is the expected size of an analysis.
type Estimate struct {
	Batches int
	Tokens  int64
}

// EstimateBook predicts batches and input tokens from word counts.
func (a *Analyzer) EstimateBook(ctx context.Context, bookID int64) (Estimate, error) {
	chapters, _, err := a.Store.ListChapters(ctx, bookID, store.ChapterFilter{}, 0, 1<<30)
	if err != nil {
		return Estimate{}, err
	}
	var tokens int64
	for _, c := range chapters {
		if !c.Excluded {
			tokens += int64(wordTokens(c.WordCount))
		}
	}
	if tokens == 0 {
		return Estimate{}, nil
	}
	batches := int(math.Ceil(float64(tokens) / float64(a.batchTokens())))
	return Estimate{Batches: batches, Tokens: tokens + int64(batches*OverheadTokens)}, nil
}

// Handler is the job handler for JobType.
func (a *Analyzer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.BookID == nil {
				return jobs.Permanent(errors.New("analysis job without book"))
			}
			return a.Run(ctx, *j.BookID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.BookID == nil {
				return
			}
			if uerr := a.Store.UpdateBook(ctx, *j.BookID, map[string]any{
				"AnalysisStatus": store.AnalysisFailed, "AnalysisError": store.CleanText(err.Error()),
			}); uerr != nil {
				a.Log.Error("mark analysis failed", "book", *j.BookID, "err", uerr)
			}
		},
	}
}

// Run analyses the remaining batches of a book, then merges duplicates.
func (a *Analyzer) Run(ctx context.Context, bookID int64, progress func(float64)) error {
	b, err := a.Store.GetBook(ctx, bookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if b.AnalysisStatus == store.AnalysisPaused || b.AnalysisStatus == store.AnalysisNone {
		return nil // paused (or reset) before it started
	}
	fields := map[string]any{"AnalysisStatus": store.AnalysisRunning, "AnalysisError": ""}
	if b.AnalysisStartedAt == nil {
		fields["AnalysisStartedAt"] = time.Now().UTC()
	}
	if err := a.Store.UpdateBook(ctx, bookID, fields); err != nil {
		return err
	}

	stats, err := a.Store.AnalysisStats(ctx, bookID)
	if err != nil {
		return err
	}
	if stats.Total == 0 {
		batches, err := plan(ctx, a.Store, bookID, a.batchTokens())
		if err != nil {
			return err
		}
		if len(batches) == 0 {
			return jobs.Permanent(errors.New("the book has no narrated chapters to analyse"))
		}
		if err := a.Store.CreateAnalysisBatches(ctx, batches); err != nil {
			return err
		}
		a.Log.Info("analysis planned", "book", bookID, "batches", len(batches))
	}

	for {
		if paused, err := a.paused(ctx, bookID); err != nil || paused {
			return err
		}
		next, err := a.Store.NextAnalysisBatch(ctx, bookID)
		if err != nil {
			return err
		}
		if next == nil {
			break
		}
		if stats, err := a.Store.AnalysisStats(ctx, bookID); err == nil && stats.Total > 0 {
			progress(0.95 * float64(stats.Done) / float64(stats.Total))
		}
		if err := a.analyzeBatch(ctx, bookID, next); err != nil {
			return err
		}
	}

	if paused, err := a.paused(ctx, bookID); err != nil || paused {
		return err
	}
	if err := a.Store.UpdateBook(ctx, bookID, map[string]any{"AnalysisStatus": store.AnalysisMerging}); err != nil {
		return err
	}
	if err := a.mergeDuplicates(ctx, bookID, func(p float64) { progress(0.95 + 0.05*p) }); err != nil {
		return err
	}
	if paused, err := a.paused(ctx, bookID); err != nil || paused {
		return err
	}
	if err := a.Store.RefreshBookCharacterStats(ctx, bookID); err != nil {
		return err
	}
	now := time.Now().UTC()
	a.Log.Info("analysis done", "book", bookID)
	return a.Store.UpdateBook(ctx, bookID, map[string]any{
		"AnalysisStatus": store.AnalysisDone, "AnalysisFinishedAt": now,
	})
}

// paused reports whether the user paused (or reset) the analysis; a
// "queued" status (resumed while this job was still running) becomes
// "running" again.
func (a *Analyzer) paused(ctx context.Context, bookID int64) (bool, error) {
	b, err := a.Store.GetBook(ctx, bookID)
	if err != nil {
		return false, err
	}
	switch b.AnalysisStatus {
	case store.AnalysisPaused, store.AnalysisNone:
		return true, nil
	case store.AnalysisQueued:
		return false, a.Store.UpdateBook(ctx, bookID, map[string]any{"AnalysisStatus": store.AnalysisRunning})
	}
	return false, nil
}

// analyzeBatch sends one batch and merges the answer. A batch the model
// cannot answer completely, or that no provider accepts, is split in two.
func (a *Analyzer) analyzeBatch(ctx context.Context, bookID int64, batch *store.AnalysisBatch) error {
	ps, err := parseParts(batch)
	if err != nil {
		return jobs.Permanent(err)
	}
	parts, err := load(ctx, a.Store, ps)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		// Its chapters were deleted: nothing to do.
		batch.Prompt = analysisPrompt.ID()
		return a.Store.CompleteAnalysisBatch(ctx, batch)
	}
	bib, err := loadBible(ctx, a.Store, bookID)
	if err != nil {
		return err
	}
	text := render(parts)
	known := bib.relevant(text, maxKnown)
	ids := make([]int64, len(known))
	for i, c := range known {
		ids[i] = c.ID
	}
	aliases, err := a.Store.CharacterAliases(ctx, ids...)
	if err != nil {
		return err
	}
	req, err := analysisPrompt.Render(map[string]any{
		"Known": knownLines(known, aliases), "ChapterCount": len(parts), "Text": text,
	})
	if err != nil {
		return jobs.Permanent(err)
	}
	req.MaxTokens = batchOutputTokens
	sent := make(map[int64]bool, len(known))
	for _, c := range known {
		sent[c.ID] = true
	}

	var res batchResult
	out, err := a.LLM.CompleteJSON(ctx, req, &res)
	var (
		tooLarge *llm.TooLargeError
		invalid  *llm.InvalidOutputError
		rejected *llm.RejectedError
	)
	if errors.As(err, &tooLarge) || (errors.As(err, &invalid) && invalid.Truncated) ||
		(errors.As(err, &rejected) && rejected.TooLong()) {
		first, second, ok, herr := halves(bookID, ps, parts)
		if herr != nil {
			return herr
		}
		if !ok {
			return jobs.Permanent(fmt.Errorf("batch at chapter %d cannot be split further: %w", batch.FirstChapterIdx+1, err))
		}
		a.Log.Info("splitting analysis batch", "book", bookID, "batch", batch.ID, "reason", err)
		return a.Store.ReplaceAnalysisBatch(ctx, batch.ID, []store.AnalysisBatch{first, second})
	}
	if err != nil {
		return err
	}

	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	batch.Prompt, batch.Provider, batch.Model = analysisPrompt.ID(), out.Provider, out.Model
	batch.InputTokens, batch.OutputTokens = out.Usage.InputTokens, out.Usage.OutputTokens
	batch.Result = string(raw)
	return a.Store.Tx(ctx, func(tx *store.Store) error {
		if err := apply(ctx, tx, bookID, bib, sent, parts, &res); err != nil {
			return err
		}
		return tx.CompleteAnalysisBatch(ctx, batch)
	})
}
