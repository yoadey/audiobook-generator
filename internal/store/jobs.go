package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// EnqueueJob inserts a queued job. NotBefore defaults to now.
func (s *Store) EnqueueJob(ctx context.Context, j *Job) error {
	j.Status = JobQueued
	if j.NotBefore.IsZero() {
		j.NotBefore = time.Now().UTC()
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = 3
	}
	return s.db.WithContext(ctx).Create(j).Error
}

// ClaimJob atomically takes the next due job of a queue, or returns nil.
//
// The claim is an optimistic compare-and-set (UPDATE … WHERE status =
// 'queued'), which is portable across SQLite and PostgreSQL and safe with
// several workers: a worker that loses the race simply looks again.
func (s *Store) ClaimJob(ctx context.Context, queue string, now time.Time) (*Job, error) {
	db := s.db.WithContext(ctx)
	for range 5 {
		var j Job
		err := db.Where("queue = ? AND status = ? AND not_before <= ?", queue, JobQueued, now).
			Order("priority DESC, id").Take(&j).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		res := db.Model(&Job{}).Where("id = ? AND status = ?", j.ID, JobQueued).Updates(map[string]any{
			"status":     JobRunning,
			"attempts":   gorm.Expr("attempts + 1"),
			"started_at": now,
			"error":      "",
		})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 1 {
			j.Status = JobRunning
			j.Attempts++
			j.StartedAt = &now
			return &j, nil
		}
	}
	return nil, nil
}

// FinishJob marks a job done.
func (s *Store) FinishJob(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&Job{ID: id}).Updates(map[string]any{
		"status": JobDone, "progress": 1, "finished_at": now, "error": "",
	}).Error
}

// RetryJob puts a failed attempt back into the queue for later.
func (s *Store) RetryJob(ctx context.Context, id int64, msg string, at time.Time) error {
	return s.db.WithContext(ctx).Model(&Job{ID: id}).Updates(map[string]any{
		"status": JobQueued, "error": CleanText(msg), "not_before": at,
	}).Error
}

// ReleaseJob returns an interrupted job (e.g. on shutdown) to the queue
// without counting the attempt.
func (s *Store) ReleaseJob(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Model(&Job{ID: id}).Updates(map[string]any{
		"status": JobQueued, "attempts": gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"),
	}).Error
}

// DeferJob returns a job to the queue until at without counting the
// attempt (e.g. while every LLM provider is out of quota).
func (s *Store) DeferJob(ctx context.Context, id int64, msg string, at time.Time) error {
	return s.db.WithContext(ctx).Model(&Job{ID: id}).Updates(map[string]any{
		"status": JobQueued, "error": CleanText(msg), "not_before": at,
		"attempts": gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"),
	}).Error
}

// FailJob marks a job permanently failed.
func (s *Store) FailJob(ctx context.Context, id int64, msg string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&Job{ID: id}).Updates(map[string]any{
		"status": JobFailed, "error": CleanText(msg), "finished_at": now,
	}).Error
}

// SetJobProgress records progress (0..1) of a running job.
func (s *Store) SetJobProgress(ctx context.Context, id int64, p float64) error {
	return s.db.WithContext(ctx).Model(&Job{ID: id}).UpdateColumn("progress", p).Error
}

// InterruptedMessage is the error of jobs that were running when the
// process died on their last attempt.
const InterruptedMessage = "interrupted on its last attempt (the process stopped or crashed while it ran)"

// RecoverRunningJobs handles jobs left "running" by a previous process.
// Only one app instance runs, so on start-up every running job is
// orphaned: it goes back to the queue, unless that was its last attempt —
// a job that kills the process (e.g. out of memory) must not crash-loop
// forever. Those fail and are returned, so their failure hooks can run.
func (s *Store) RecoverRunningJobs(ctx context.Context) (requeued int64, failed []Job, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		exhausted := tx.Where("status = ? AND attempts >= max_attempts", JobRunning)
		if err := exhausted.Find(&failed).Error; err != nil {
			return err
		}
		if len(failed) > 0 {
			ids := make([]int64, len(failed))
			for i := range failed {
				ids[i] = failed[i].ID
			}
			if err := tx.Model(&Job{}).Where("id IN ?", ids).Updates(map[string]any{
				"status": JobFailed, "error": InterruptedMessage, "finished_at": time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
		}
		res := tx.Model(&Job{}).Where("status = ?", JobRunning).Updates(map[string]any{"status": JobQueued})
		requeued = res.RowsAffected
		return res.Error
	})
	return requeued, failed, err
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	BookID   *int64
	Statuses []JobStatus
}

// ListJobs returns jobs, newest first.
func (s *Store) ListJobs(ctx context.Context, f JobFilter, offset, limit int) ([]Job, int64, error) {
	q := s.db.WithContext(ctx).Model(&Job{})
	if f.BookID != nil {
		q = q.Where("book_id = ?", *f.BookID)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var js []Job
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&js).Error
	return js, total, err
}

// GetJob loads one job.
func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	var j Job
	if err := s.db.WithContext(ctx).Take(&j, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &j, nil
}

// CancelJob cancels a queued job; running jobs cannot be canceled.
func (s *Store) CancelJob(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Model(&Job{}).Where("id = ? AND status = ?", id, JobQueued).
		Updates(map[string]any{"status": JobCanceled, "finished_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInvalid
	}
	return nil
}

// RetryFailedJob queues a failed or canceled job again with fresh attempts.
func (s *Store) RetryFailedJob(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Model(&Job{}).
		Where("id = ? AND status IN ?", id, []JobStatus{JobFailed, JobCanceled}).
		Updates(map[string]any{
			"status": JobQueued, "attempts": 0, "error": "", "not_before": time.Now().UTC(),
			"finished_at": nil, "progress": 0,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInvalid
	}
	return nil
}

// CleanText makes free text (error messages that may quote HTTP bodies
// cut at a byte limit) storable on both dialects: PostgreSQL rejects
// invalid UTF-8 and NUL bytes in text columns.
func CleanText(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}
