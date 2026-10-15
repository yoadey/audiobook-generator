// Package store owns persistence: GORM models, repositories and migrations.
//
// Everything here must work on both SQLite and PostgreSQL. Dialect-specific
// code is not allowed outside small, explicitly marked spots that are covered
// by tests on both dialects.
package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yoadey/audiobook-generator/internal/config"
)

// Store wraps the GORM handle.
type Store struct {
	db      *gorm.DB
	dialect string
}

// Open connects to the configured database. It does not run migrations.
func Open(cfg config.Database) (*Store, error) {
	var dialector gorm.Dialector
	switch cfg.Type {
	case config.DialectSQLite:
		if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o750); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
		dialector = gormlite.Open(sqliteDSN(cfg.SQLitePath))
	case config.DialectPostgres:
		dialector = postgres.Open(cfg.PostgresConnString())
	default:
		return nil, fmt.Errorf("unsupported database type %q", cfg.Type)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:         logger.Discard,
		TranslateError: true,
		NowFunc:        func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", cfg.Type, err)
	}
	if cfg.Type == config.DialectSQLite {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}
		// One writer at a time anyway; a small pool keeps reads concurrent
		// while busy_timeout + immediate transactions avoid SQLITE_BUSY.
		sqlDB.SetMaxOpenConns(4)
	}
	return &Store{db: db, dialect: cfg.Type}, nil
}

// sqliteDSN enables WAL, a busy timeout, foreign keys and immediate
// transactions (so concurrent writers queue instead of failing).
func sqliteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(wal)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(normal)")
	q.Set("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

// Dialect returns config.DialectSQLite or config.DialectPostgres.
func (s *Store) Dialect() string { return s.dialect }

// DB exposes the GORM handle for repositories in this package and tests.
func (s *Store) DB() *gorm.DB { return s.db }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close releases the connection pool.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
