package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListDownloads returns a book's assembled files.
func (s *Store) ListDownloads(ctx context.Context, bookID int64) ([]Download, error) {
	var ds []Download
	err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Order("volume_id").Find(&ds).Error
	return ds, err
}

// GetDownload returns the file of a volume (0: the whole book).
func (s *Store) GetDownload(ctx context.Context, bookID, volumeID int64) (*Download, error) {
	var d Download
	err := s.db.WithContext(ctx).Where("book_id = ? AND volume_id = ?", bookID, volumeID).Take(&d).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

// QueueDownload marks a volume's file as queued (creating its row) and
// clears a previous error; a finished file stays available.
func (s *Store) QueueDownload(ctx context.Context, bookID, volumeID int64) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "book_id"}, {Name: "volume_id"}},
		DoUpdates: clause.Assignments(map[string]any{"status": DownloadQueued, "error": "", "updated_at": now}),
	}).Create(&Download{BookID: bookID, VolumeID: volumeID, Status: DownloadQueued, CreatedAt: now, UpdatedAt: now}).Error
}

// SetDownloadStatus updates the state of a volume's file.
func (s *Store) SetDownloadStatus(ctx context.Context, bookID, volumeID int64, status DownloadStatus, msg string) error {
	res := s.db.WithContext(ctx).Model(&Download{}).Where("book_id = ? AND volume_id = ?", bookID, volumeID).
		Updates(map[string]any{"status": status, "error": CleanText(msg)})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// FinishDownload records an assembled file and returns the path of the
// file it replaces ("" for none).
func (s *Store) FinishDownload(ctx context.Context, d *Download) (string, error) {
	var old string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur Download
		if err := tx.Where("book_id = ? AND volume_id = ?", d.BookID, d.VolumeID).Take(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound // the book's content was reset meanwhile
			}
			return err
		}
		if cur.Path != d.Path {
			old = cur.Path
		}
		return tx.Model(&Download{}).Where("id = ?", cur.ID).Updates(map[string]any{
			"status": DownloadReady, "error": "", "path": d.Path, "size": d.Size,
			"duration_ms": d.DurationMs, "source_key": d.SourceKey,
		}).Error
	})
	return old, err
}
