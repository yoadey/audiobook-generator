package render_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// queue records jobs in the store like the real runner, without running them.
type queue struct {
	s    *store.Store
	mu   sync.Mutex
	jobs []store.Job
}

func (q *queue) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = render.Queue
	if err := q.s.EnqueueJob(ctx, j); err != nil {
		return err
	}
	q.mu.Lock()
	q.jobs = append(q.jobs, *j)
	q.mu.Unlock()
	return nil
}

func TestEnsureAhead(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		_, b, cs := setup(t, s) // Copyright (excluded), Chapter 1, Chapter 2, Chapter 3
		q := &queue{s: s}
		sched := &render.Scheduler{Store: s, Jobs: q, DefaultVoice: "af_heart"}

		b.Lookahead = 2
		n, err := sched.EnsureAhead(ctx, b)
		require.NoError(t, err)
		assert.Equal(t, 2, n, "excluded front matter is skipped")
		require.Len(t, q.jobs, 2)
		assert.Equal(t, cs[1].ID, *q.jobs[0].ChapterID)
		assert.Equal(t, cs[2].ID, *q.jobs[1].ChapterID)
		assert.Greater(t, q.jobs[0].Priority, q.jobs[1].Priority, "nearer chapters first")

		// Pending renders are not queued twice.
		n, err = sched.EnsureAhead(ctx, b)
		require.NoError(t, err)
		assert.Zero(t, n)

		// Moving on queues the next chapter.
		require.NoError(t, s.SaveProgress(ctx, &store.ListenProgress{BookID: b.ID, ChapterID: cs[2].ID, PositionMs: 5000}))
		n, err = sched.EnsureAhead(ctx, b)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, cs[3].ID, *q.jobs[2].ChapterID)

		p, err := s.GetProgress(ctx, b.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 5000, p.PositionMs)
	})
}

func TestRenderChapterSkipsCurrentAudio(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		_, b, cs := setup(t, s)
		q := &queue{s: s}
		sched := &render.Scheduler{Store: s, Jobs: q, DefaultVoice: "af_heart"}
		ch := cs[1]
		require.NoError(t, s.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{Path: "x.m4a", Voice: "af_heart@1.00"}))
		got, _ := s.GetChapter(ctx, ch.ID)
		assert.False(t, sched.Stale(b, got))

		ok, err := sched.RenderChapter(ctx, b, got, false)
		require.NoError(t, err)
		assert.False(t, ok, "audio is current")
		ok, err = sched.RenderChapter(ctx, b, got, true)
		require.NoError(t, err)
		assert.True(t, ok, "forced")

		// A new narrator makes existing audio stale.
		b.NarratorVoice = "bm_george"
		assert.True(t, sched.Stale(b, got))
	})
}
