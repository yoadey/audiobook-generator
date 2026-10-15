package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migrations is the ordered, append-only schema history. Never edit a
// migration that has been released; add a new one instead. Each migration
// declares its own snapshot of the structs it touches, so later changes to
// the models in models.go cannot alter what an old migration does.
var migrations = []*gormigrate.Migration{
	{
		ID: "202610150001_create_books",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				ID        int64  `gorm:"primaryKey"`
				Title     string `gorm:"not null"`
				Author    string `gorm:"not null;default:''"`
				Status    string `gorm:"not null;index"`
				CreatedAt time.Time
				UpdatedAt time.Time
			}
			return tx.Migrator().CreateTable(&Book{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("books")
		},
	},
	{
		ID: "202610150002_book_content_and_jobs",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				Language       string  `gorm:"not null;default:''"`
				EpubPath       string  `gorm:"not null;default:''"`
				CoverPath      string  `gorm:"not null;default:''"`
				CoverMediaType string  `gorm:"not null;default:''"`
				QuoteStyle     string  `gorm:"not null;default:''"`
				WordCount      int64   `gorm:"not null;default:0"`
				ChapterCount   int     `gorm:"not null;default:0"`
				Progress       float64 `gorm:"not null;default:0"`
				Error          string  `gorm:"not null;default:''"`
			}
			for _, col := range []string{"Language", "EpubPath", "CoverPath", "CoverMediaType",
				"QuoteStyle", "WordCount", "ChapterCount", "Progress", "Error"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}

			type Volume struct {
				ID     int64  `gorm:"primaryKey"`
				BookID int64  `gorm:"not null;index"`
				Idx    int    `gorm:"not null"`
				Title  string `gorm:"not null"`
			}
			type Chapter struct {
				ID         int64  `gorm:"primaryKey"`
				BookID     int64  `gorm:"not null;index:idx_chapters_book_idx,priority:1"`
				VolumeID   *int64 `gorm:"index"`
				Idx        int    `gorm:"not null;index:idx_chapters_book_idx,priority:2"`
				Title      string `gorm:"not null"`
				WordCount  int    `gorm:"not null;default:0"`
				QuoteCount int    `gorm:"not null;default:0"`
				Excluded   bool   `gorm:"not null;default:false"`
				Status     string
				CreatedAt  time.Time
				UpdatedAt  time.Time
			}
			type Paragraph struct {
				ID        int64  `gorm:"primaryKey"`
				ChapterID int64  `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:1"`
				Idx       int    `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:2"`
				Kind      string `gorm:"not null"`
				Text      string `gorm:"not null"`
				WordCount int    `gorm:"not null;default:0"`
			}
			type Segment struct {
				ID          int64  `gorm:"primaryKey"`
				ChapterID   int64  `gorm:"not null;index:idx_segments_chapter_idx,priority:1"`
				ParagraphID int64  `gorm:"not null;index"`
				Idx         int    `gorm:"not null;index:idx_segments_chapter_idx,priority:2"`
				Kind        string `gorm:"not null"`
				QuoteRef    int    `gorm:"not null;default:0"`
				Text        string `gorm:"not null"`
			}
			type Job struct {
				ID          int64     `gorm:"primaryKey"`
				Type        string    `gorm:"not null"`
				Queue       string    `gorm:"not null;index:idx_jobs_claim,priority:1"`
				Status      string    `gorm:"not null;index:idx_jobs_claim,priority:2"`
				Priority    int       `gorm:"not null;default:0"`
				BookID      *int64    `gorm:"index"`
				ChapterID   *int64    `gorm:"index"`
				Payload     string    `gorm:"not null;default:''"`
				Attempts    int       `gorm:"not null;default:0"`
				MaxAttempts int       `gorm:"not null;default:3"`
				NotBefore   time.Time `gorm:"not null"`
				Progress    float64   `gorm:"not null;default:0"`
				Error       string    `gorm:"not null;default:''"`
				CreatedAt   time.Time
				UpdatedAt   time.Time
				StartedAt   *time.Time
				FinishedAt  *time.Time
			}
			return tx.Migrator().CreateTable(&Volume{}, &Chapter{}, &Paragraph{}, &Segment{}, &Job{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("jobs", "segments", "paragraphs", "chapters", "volumes")
		},
	},
	{
		ID: "202610150003_audio_and_progress",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				NarratorVoice string  `gorm:"not null;default:''"`
				NarratorSpeed float64 `gorm:"not null;default:1"`
				Lookahead     int     `gorm:"not null;default:3"`
			}
			for _, col := range []string{"NarratorVoice", "NarratorSpeed", "Lookahead"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}
			type Chapter struct {
				AudioStatus     string `gorm:"not null;default:'none'"`
				AudioPath       string `gorm:"not null;default:''"`
				AudioDurationMs int64  `gorm:"not null;default:0"`
				AudioSize       int64  `gorm:"not null;default:0"`
				AudioError      string `gorm:"not null;default:''"`
				AudioVoice      string `gorm:"not null;default:''"`
				AudioUpdatedAt  *time.Time
			}
			for _, col := range []string{"AudioStatus", "AudioPath", "AudioDurationMs", "AudioSize",
				"AudioError", "AudioVoice", "AudioUpdatedAt"} {
				if err := tx.Migrator().AddColumn(&Chapter{}, col); err != nil {
					return fmt.Errorf("chapters.%s: %w", col, err)
				}
			}
			type ListenProgress struct {
				ID         int64 `gorm:"primaryKey"`
				BookID     int64 `gorm:"not null;uniqueIndex"`
				ChapterID  int64 `gorm:"not null"`
				PositionMs int64 `gorm:"not null;default:0"`
				UpdatedAt  time.Time
			}
			return tx.Migrator().CreateTable(&ListenProgress{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("listen_progresses")
		},
	},
}

// LatestSchemaVersion is the ID of the newest migration.
func LatestSchemaVersion() string { return migrations[len(migrations)-1].ID }

// Migrate applies all pending migrations.
func (s *Store) Migrate(ctx context.Context) error {
	m := gormigrate.New(s.db.WithContext(ctx), gormigrate.DefaultOptions, migrations)
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// SchemaVersion returns the ID of the last applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (string, error) {
	var row struct{ ID string }
	err := s.db.WithContext(ctx).
		Table(gormigrate.DefaultOptions.TableName).
		Select(gormigrate.DefaultOptions.IDColumnName + " AS id").
		Order(gormigrate.DefaultOptions.IDColumnName + " DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return row.ID, err
}
