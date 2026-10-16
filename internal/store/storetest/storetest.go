// Package storetest runs tests against every supported database dialect.
//
// SQLite always runs (a fresh file per test). PostgreSQL runs when
// ABG_TEST_POSTGRES_DSN points at a server; every test then gets its own
// schema, so tests stay isolated and can run in parallel.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yoadey/audiobook-generator/internal/config"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// PostgresDSNEnv names the variable holding the test server's DSN
// (keyword/value form, e.g. "host=localhost user=postgres dbname=postgres").
const PostgresDSNEnv = "ABG_TEST_POSTGRES_DSN"

// ForEachDialect runs fn once per available dialect with a migrated store.
func ForEachDialect(t *testing.T, fn func(t *testing.T, s *store.Store)) {
	t.Helper()
	t.Run(config.DialectSQLite, func(t *testing.T) {
		fn(t, open(t, config.Database{
			Type:       config.DialectSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "test.db"),
		}))
	})
	t.Run(config.DialectPostgres, func(t *testing.T) {
		dsn := os.Getenv(PostgresDSNEnv)
		if dsn == "" {
			t.Skipf("%s not set", PostgresDSNEnv)
		}
		schema := isolatedSchema(t, dsn)
		fn(t, open(t, config.Database{
			Type:        config.DialectPostgres,
			PostgresDSN: dsn + " search_path=" + schema,
		}))
	})
}

func open(t *testing.T, cfg config.Database) *store.Store {
	t.Helper()
	s, err := store.Open(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Migrate(context.Background()))
	return s
}

// isolatedSchema creates a throwaway schema and drops it after the test.
func isolatedSchema(t *testing.T, dsn string) string {
	t.Helper()
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := admin.DB()
	require.NoError(t, err)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		_ = sqlDB.Close()
	})
	return schema
}
