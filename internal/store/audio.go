package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChapterAudio is the result of a finished render.
type ChapterAudio struct {
	Path       string
	DurationMs int64
	Size       int64
	Voice      string
}

// SetAudioStatus sets the audio state (and error message) of chapters.
func (s *Store) SetAudioStatus(ctx context.Context, status AudioStatus, msg string, chapterIDs ...int64) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Chapter{}).Where("id IN ?", chapterIDs).
		Updates(map[string]any{"audio_status": status, "audio_error": msg}).Error
}

// SetChapterAudio records a finished render.
func (s *Store) SetChapterAudio(ctx context.Context, chapterID int64, a ChapterAudio) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&Chapter{ID: chapterID}).Updates(map[string]any{
		"audio_status":      AudioReady,
		"audio_path":        a.Path,
		"audio_duration_ms": a.DurationMs,
		"audio_size":        a.Size,
		"audio_voice":       a.Voice,
		"audio_error":       "",
		"audio_updated_at":  now,
	}).Error
}

// ClearChapterAudio forgets rendered audio (e.g. after the text changed)
// and returns the audio paths that were set, so callers can delete files.
func (s *Store) ClearChapterAudio(ctx context.Context, chapterIDs ...int64) ([]string, error) {
	if len(chapterIDs) == 0 {
		return nil, nil
	}
	var paths []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Chapter{}).Where("id IN ? AND audio_path <> ''", chapterIDs).
			Pluck("audio_path", &paths).Error; err != nil {
			return err
		}
		return tx.Model(&Chapter{}).Where("id IN ?", chapterIDs).Updates(map[string]any{
			"audio_status": AudioNone, "audio_path": "", "audio_duration_ms": 0, "audio_size": 0,
			"audio_voice": "", "audio_error": "", "audio_updated_at": nil,
		}).Error
	})
	return paths, err
}

// ChaptersAhead returns up to n narrated (not excluded) chapters of a book
// in reading order, starting at index from.
func (s *Store) ChaptersAhead(ctx context.Context, bookID int64, from, n int) ([]Chapter, error) {
	var cs []Chapter
	err := s.db.WithContext(ctx).
		Where("book_id = ? AND idx >= ? AND excluded = ?", bookID, from, false).
		Order("idx").Limit(n).Find(&cs).Error
	return cs, err
}

// GetProgress returns the listening position of a book (nil when none).
func (s *Store) GetProgress(ctx context.Context, bookID int64) (*ListenProgress, error) {
	var p ListenProgress
	err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

// SaveProgress upserts the listening position of a book.
func (s *Store) SaveProgress(ctx context.Context, p *ListenProgress) error {
	p.UpdatedAt = time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "book_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"chapter_id", "position_ms", "updated_at"}),
	}).Create(p).Error
}

// PendingRenderJob returns the queued or running render job of a chapter.
func (s *Store) PendingRenderJob(ctx context.Context, jobType string, chapterID int64) (*Job, error) {
	var j Job
	err := s.db.WithContext(ctx).
		Where("type = ? AND chapter_id = ? AND status IN ?", jobType, chapterID, []JobStatus{JobQueued, JobRunning}).
		Take(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &j, err
}
