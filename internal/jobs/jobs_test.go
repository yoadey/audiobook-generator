package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func start(t *testing.T, r *jobs.Runner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = r.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func waitStatus(t *testing.T, s *store.Store, id int64, want store.JobStatus) *store.Job {
	t.Helper()
	var j *store.Job
	require.Eventually(t, func() bool {
		var err error
		j, err = s.GetJob(context.Background(), id)
		require.NoError(t, err)
		return j.Status == want
	}, 10*time.Second, 10*time.Millisecond, "job %d never reached %s", id, want)
	return j
}

func newRunner(s *store.Store) *jobs.Runner {
	r := jobs.New(s, slog.New(slog.DiscardHandler))
	r.PollInterval = 20 * time.Millisecond
	r.Backoff = func(int) time.Duration { return 0 }
	return r
}

func TestRunsJobs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		r := newRunner(s)
		var ran atomic.Int32
		r.Handle("ok", jobs.Handler{Queue: "q", Run: func(_ context.Context, _ *store.Job, progress func(float64)) error {
			progress(0.5)
			ran.Add(1)
			return nil
		}})
		r.SetConcurrency("q", 3)
		start(t, r)

		var ids []int64
		for range 5 {
			j := &store.Job{Type: "ok"}
			require.NoError(t, r.Enqueue(context.Background(), j))
			ids = append(ids, j.ID)
		}
		for _, id := range ids {
			j := waitStatus(t, s, id, store.JobDone)
			assert.Equal(t, 1, j.Attempts)
			assert.InDelta(t, 1.0, j.Progress, 0.001)
		}
		assert.EqualValues(t, 5, ran.Load())
	})
}

func TestRetriesThenFails(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		r := newRunner(s)
		var failed atomic.Bool
		r.Handle("flaky", jobs.Handler{
			Queue: "q",
			Run: func(context.Context, *store.Job, func(float64)) error {
				return errors.New("boom")
			},
			OnFail: func(_ context.Context, _ *store.Job, err error) { failed.Store(err != nil) },
		})
		start(t, r)

		j := &store.Job{Type: "flaky", MaxAttempts: 3}
		require.NoError(t, r.Enqueue(context.Background(), j))
		got := waitStatus(t, s, j.ID, store.JobFailed)
		assert.Equal(t, 3, got.Attempts)
		assert.Equal(t, "boom", got.Error)
		assert.Eventually(t, failed.Load, time.Second, 10*time.Millisecond)
	})
}

func TestPermanentErrorSkipsRetries(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		r := newRunner(s)
		r.Handle("bad", jobs.Handler{Queue: "q", Run: func(context.Context, *store.Job, func(float64)) error {
			return jobs.Permanent(errors.New("not an EPUB"))
		}})
		start(t, r)

		j := &store.Job{Type: "bad", MaxAttempts: 5}
		require.NoError(t, r.Enqueue(context.Background(), j))
		got := waitStatus(t, s, j.ID, store.JobFailed)
		assert.Equal(t, 1, got.Attempts)
	})
}

func TestRequeuesInterruptedJobs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		// A job left "running" by a crashed process.
		j := &store.Job{Type: "ok", Queue: "q"}
		require.NoError(t, s.EnqueueJob(ctx, j))
		claimed, err := s.ClaimJob(ctx, "q", time.Now().UTC())
		require.NoError(t, err)
		require.Equal(t, j.ID, claimed.ID)

		// Nothing else is claimable while it runs.
		none, err := s.ClaimJob(ctx, "q", time.Now().UTC())
		require.NoError(t, err)
		assert.Nil(t, none)

		r := newRunner(s)
		r.Handle("ok", jobs.Handler{Queue: "q", Run: func(context.Context, *store.Job, func(float64)) error { return nil }})
		start(t, r)
		waitStatus(t, s, j.ID, store.JobDone)
	})
}

func TestUnknownJobType(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		r := newRunner(s)
		assert.Error(t, r.Enqueue(context.Background(), &store.Job{Type: "nope"}))
	})
}
