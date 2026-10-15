// Package jobs runs persistent background jobs stored in the database.
//
// Every long-running step (import, analysis, rendering) is a job, so
// restarts, rate limits and crashes only pause work: queued jobs survive in
// the database and jobs that were running when the process died are put
// back into the queue on start-up.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Handler executes one job type.
type Handler struct {
	// Queue the job type runs on; each queue has its own worker pool.
	Queue string
	// Run does the work. progress (0..1) may be called freely; it is
	// throttled before it reaches the database.
	Run func(ctx context.Context, j *store.Job, progress func(float64)) error
	// OnFail, when set, is called once a job has failed for good.
	OnFail func(ctx context.Context, j *store.Job, err error)
}

// PermanentError marks a failure that retrying cannot fix.
type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so the job fails without further attempts.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return PermanentError{err}
}

// Deferrer is implemented by errors that mean "not now, try again at
// DeferUntil" — e.g. every LLM provider is out of quota until midnight.
// The job goes back to the queue without counting the attempt.
type Deferrer interface {
	error
	DeferUntil() time.Time
}

// DeferError is a ready-made Deferrer.
type DeferError struct {
	Until  time.Time
	Reason string
}

func (e DeferError) Error() string { return e.Reason }

// DeferUntil implements Deferrer.
func (e DeferError) DeferUntil() time.Time { return e.Until }

// Defer returns an error that puts the job back into the queue until t.
func Defer(t time.Time, reason string) error { return DeferError{Until: t, Reason: reason} }

// Runner owns the worker pools.
type Runner struct {
	st  *store.Store
	log *slog.Logger

	mu          sync.Mutex
	handlers    map[string]Handler
	concurrency map[string]int
	wake        map[string]chan struct{}

	// PollInterval bounds how long a worker sleeps when its queue is empty
	// (enqueueing through the Runner wakes workers immediately).
	PollInterval time.Duration
	// Backoff returns the delay before retry number attempt (1-based).
	Backoff func(attempt int) time.Duration
}

// New returns a Runner without handlers.
func New(st *store.Store, log *slog.Logger) *Runner {
	return &Runner{
		st:           st,
		log:          log,
		handlers:     map[string]Handler{},
		concurrency:  map[string]int{},
		wake:         map[string]chan struct{}{},
		PollInterval: 5 * time.Second,
		Backoff: func(attempt int) time.Duration {
			return min(30*time.Second<<(attempt-1), 30*time.Minute)
		},
	}
}

// Handle registers the handler for a job type.
func (r *Runner) Handle(jobType string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[jobType] = h
	if _, ok := r.concurrency[h.Queue]; !ok {
		r.concurrency[h.Queue] = 1
	}
	if _, ok := r.wake[h.Queue]; !ok {
		r.wake[h.Queue] = make(chan struct{}, 1)
	}
}

// SetConcurrency sets the number of workers of a queue (default 1).
func (r *Runner) SetConcurrency(queue string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.concurrency[queue] = max(n, 1)
}

// Enqueue stores a job of a registered type and wakes its queue.
func (r *Runner) Enqueue(ctx context.Context, j *store.Job) error {
	r.mu.Lock()
	h, ok := r.handlers[j.Type]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown job type %q", j.Type)
	}
	j.Queue = h.Queue
	if err := r.st.EnqueueJob(ctx, j); err != nil {
		return err
	}
	r.notify(h.Queue)
	return nil
}

// Wake makes idle workers of a queue look for work now.
func (r *Runner) Wake(queue string) { r.notify(queue) }

func (r *Runner) notify(queue string) {
	r.mu.Lock()
	ch := r.wake[queue]
	r.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Run starts all workers and blocks until ctx is canceled and every
// worker has stopped.
func (r *Runner) Run(ctx context.Context) error {
	n, err := r.st.RequeueRunningJobs(ctx)
	if err != nil {
		return fmt.Errorf("requeue running jobs: %w", err)
	}
	if n > 0 {
		r.log.Info("requeued interrupted jobs", "count", n)
	}

	r.mu.Lock()
	queues := map[string]int{}
	for q, c := range r.concurrency {
		queues[q] = c
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	for q, c := range queues {
		for range c {
			wg.Go(func() { r.worker(ctx, q) })
		}
	}
	wg.Wait()
	return nil
}

func (r *Runner) worker(ctx context.Context, queue string) {
	r.mu.Lock()
	wake := r.wake[queue]
	r.mu.Unlock()
	for ctx.Err() == nil {
		j, err := r.st.ClaimJob(ctx, queue, time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error("claim job", "queue", queue, "err", err)
			}
			sleep(ctx, wake, r.PollInterval)
			continue
		}
		if j == nil {
			sleep(ctx, wake, r.PollInterval)
			continue
		}
		r.execute(ctx, j)
		// Other workers of this queue may be idle while more work waits.
		r.notify(queue)
	}
}

func sleep(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-t.C:
	}
}

func (r *Runner) execute(ctx context.Context, j *store.Job) {
	r.mu.Lock()
	h := r.handlers[j.Type]
	r.mu.Unlock()
	log := r.log.With("job", j.ID, "type", j.Type, "attempt", j.Attempts)
	log.Info("job started")
	start := time.Now()

	err := r.run(ctx, h, j)
	// Use a fresh context for bookkeeping: ctx may be canceled by now.
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	switch {
	case err == nil:
		if err := r.st.FinishJob(bg, j.ID); err != nil {
			log.Error("finish job", "err", err)
		}
		log.Info("job done", "duration", time.Since(start))
	case ctx.Err() != nil:
		// Shutdown: give the job back without counting the attempt.
		if err := r.st.ReleaseJob(bg, j.ID); err != nil {
			log.Error("release job", "err", err)
		}
		log.Info("job interrupted by shutdown")
	default:
		var deferred Deferrer
		if errors.As(err, &deferred) {
			at := deferred.DeferUntil().UTC()
			log.Info("job deferred", "reason", err, "until", at)
			if derr := r.st.DeferJob(bg, j.ID, err.Error(), at); derr != nil {
				log.Error("defer job", "err", derr)
			}
			return
		}
		var perm PermanentError
		if errors.As(err, &perm) || j.Attempts >= j.MaxAttempts {
			log.Error("job failed", "err", err)
			if ferr := r.st.FailJob(bg, j.ID, err.Error()); ferr != nil {
				log.Error("fail job", "err", ferr)
			}
			if h.OnFail != nil {
				h.OnFail(bg, j, err)
			}
			return
		}
		at := time.Now().UTC().Add(r.Backoff(j.Attempts))
		log.Warn("job failed, will retry", "err", err, "retry_at", at)
		if rerr := r.st.RetryJob(bg, j.ID, err.Error(), at); rerr != nil {
			log.Error("retry job", "err", rerr)
		}
	}
}

// run calls the handler, turning panics into errors.
func (r *Runner) run(ctx context.Context, h Handler, j *store.Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	var last time.Time
	progress := func(p float64) {
		if time.Since(last) < time.Second && p < 1 {
			return
		}
		last = time.Now()
		if err := r.st.SetJobProgress(ctx, j.ID, p); err != nil && ctx.Err() == nil {
			r.log.Warn("job progress", "job", j.ID, "err", err)
		}
	}
	return h.Run(ctx, j, progress)
}
