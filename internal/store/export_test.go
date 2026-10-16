package store

import (
	"context"

	"github.com/go-gormigrate/gormigrate/v2"
)

// RollbackLast undoes the newest migration (tests only).
func (s *Store) RollbackLast(ctx context.Context) error {
	return gormigrate.New(s.db.WithContext(ctx), gormigrate.DefaultOptions, migrations).RollbackLast()
}
