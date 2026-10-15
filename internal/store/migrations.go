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
}

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
