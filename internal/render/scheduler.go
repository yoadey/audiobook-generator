package render

import (
	"context"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Scheduler decides which chapters to render: on request, and "just in
// time" by keeping the next chapters after the listening position ready.
type Scheduler struct {
	Store        *store.Store
	Jobs         Enqueuer
	DefaultVoice string

	// mu makes "no render pending → enqueue one" atomic (one app
	// instance; requests and progress saves arrive concurrently).
	mu sync.Mutex
}

// RetryFailedAfter is how long the lookahead leaves a chapter whose render
// failed for good alone before it tries again (a manual render retries at
// once). Without it every progress save would queue the failing chapter.
const RetryFailedAfter = 30 * time.Minute

// Job priorities: explicit requests before lookahead.
const (
	priorityManual    = 200
	priorityLookahead = 100
)

// current reports whether a chapter's audio matches the book's narrator
// and the chapter's speakers and their voices — narrated audio of a chapter
// that can now get character voices is not.
func current(b *store.Book, c *store.Chapter, voiceKey string) bool {
	return c.AudioStatus == store.AudioReady && c.AudioVoice == voiceKey && c.AudioVoiceRev == c.VoiceRev &&
		!attribution.Needed(b, c)
}

// Pending reports whether a chapter waits for or is being rendered.
func Pending(c *store.Chapter) bool {
	return c.AudioStatus == store.AudioQueued || c.AudioStatus == store.AudioRendering
}

// Stale reports whether ready audio was rendered with other settings.
func (s *Scheduler) Stale(b *store.Book, c *store.Chapter) bool {
	if c.AudioStatus != store.AudioReady {
		return false
	}
	return !current(b, c, VoiceKey(NarratorVoice(b, s.DefaultVoice)))
}

// RenderChapter queues a chapter unless a render is already pending.
// With force, current audio is rendered again.
func (s *Scheduler) RenderChapter(ctx context.Context, b *store.Book, c *store.Chapter, force bool) (bool, error) {
	return s.enqueue(ctx, b, c, priorityManual, force)
}

func (s *Scheduler) enqueue(ctx context.Context, b *store.Book, c *store.Chapter, priority int, force bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && current(b, c, VoiceKey(NarratorVoice(b, s.DefaultVoice))) {
		return false, nil
	}
	pending, err := s.Store.PendingRenderJob(ctx, JobType, c.ID)
	if err != nil {
		return false, err
	}
	if pending != nil {
		return false, nil
	}
	if err := s.Store.SetAudioStatus(ctx, store.AudioQueued, "", c.ID); err != nil {
		return false, err
	}
	if err := s.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &b.ID, ChapterID: &c.ID, Priority: priority, MaxAttempts: 3,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// RenderAhead queues up to count narrated chapters from index from on.
func (s *Scheduler) RenderAhead(ctx context.Context, b *store.Book, from, count int) (int, error) {
	cs, err := s.Store.ChaptersAhead(ctx, b.ID, from, count)
	if err != nil {
		return 0, err
	}
	queued := 0
	for k := range cs {
		c := &cs[k]
		if c.AudioStatus == store.AudioFailed && time.Since(c.UpdatedAt) < RetryFailedAfter {
			continue
		}
		ok, err := s.enqueue(ctx, b, c, priorityLookahead-k, false)
		if err != nil {
			return queued, err
		}
		if ok {
			queued++
		}
	}
	return queued, nil
}

// EnsureAhead keeps the book's lookahead window after the listening
// position rendered (the current chapter included).
func (s *Scheduler) EnsureAhead(ctx context.Context, b *store.Book) (int, error) {
	if b.Lookahead <= 0 {
		return 0, nil
	}
	from := 0
	p, err := s.Store.GetProgress(ctx, b.ID)
	if err != nil {
		return 0, err
	}
	if p != nil {
		c, err := s.Store.GetChapter(ctx, p.ChapterID)
		if err == nil && c.BookID == b.ID {
			from = c.Idx
		}
	}
	return s.RenderAhead(ctx, b, from, b.Lookahead)
}
