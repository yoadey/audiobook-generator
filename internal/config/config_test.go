package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ABG_DATA_DIR", "/srv/data")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, DialectSQLite, cfg.DB.Type)
	assert.Equal(t, filepath.Join("/srv/data", "audiobook-generator.db"), cfg.DB.SQLitePath)
	assert.Equal(t, "http://localhost:8880", cfg.TTS.URL)
}

func TestLoadPostgres(t *testing.T) {
	t.Setenv("ABG_DB_TYPE", "postgres")
	t.Setenv("ABG_DB_POSTGRES_HOST", "db.example")
	t.Setenv("ABG_DB_POSTGRES_USER", "app")
	t.Setenv("ABG_DB_POSTGRES_PASSWORD", "it's secret")
	t.Setenv("ABG_DB_POSTGRES_DATABASE", "audiobook")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t,
		`host=db.example port=5432 user=app dbname=audiobook sslmode=prefer password='it\'s secret'`,
		cfg.DB.PostgresConnString())
}

func TestLoadPostgresDSNWins(t *testing.T) {
	t.Setenv("ABG_DB_TYPE", "postgres")
	t.Setenv("ABG_DB_POSTGRES_DSN", "postgres://u:p@h/db")
	t.Setenv("ABG_DB_POSTGRES_HOST", "ignored")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@h/db", cfg.DB.PostgresConnString())
}

func TestValidate(t *testing.T) {
	tests := map[string]map[string]string{
		"unknown db type":       {"ABG_DB_TYPE": "mysql"},
		"postgres without host": {"ABG_DB_TYPE": "postgres"},
		"bad log level":         {"ABG_LOG_LEVEL": "loud"},
		"bad log format":        {"ABG_LOG_FORMAT": "xml"},
		"bad tts url":           {"ABG_TTS_URL": "not a url"},
		"bad engine":            {"ABG_TTS_ENGINES": `[{"name":"x","kind":"piper","url":"http://x"}]`},
		"bad engines json":      {"ABG_TTS_ENGINES": `[{`},
	}
	for name, envs := range tests {
		t.Run(name, func(t *testing.T) {
			for k, v := range envs {
				t.Setenv(k, v)
			}
			_, err := Load()
			assert.Error(t, err)
		})
	}
}
