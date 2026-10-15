// Package config loads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Database dialects.
const (
	DialectSQLite   = "sqlite"
	DialectPostgres = "postgres"
)

// Config is the complete runtime configuration. Every field maps to an
// ABG_-prefixed environment variable.
type Config struct {
	ListenAddr string `env:"LISTEN_ADDR" envDefault:":8080"`
	// DataDir holds uploaded EPUBs, rendered audio and (in SQLite mode) the
	// database file.
	DataDir string `env:"DATA_DIR" envDefault:"/data"`

	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	DB  Database `envPrefix:"DB_"`
	TTS TTS      `envPrefix:"TTS_"`
}

// Database selects and configures the GORM dialect.
type Database struct {
	Type string `env:"TYPE" envDefault:"sqlite"`

	// SQLitePath defaults to <DataDir>/audiobook-generator.db.
	SQLitePath string `env:"SQLITE_PATH"`

	// PostgresDSN, when set, is used verbatim; otherwise the DSN is built
	// from the individual fields below.
	PostgresDSN      string `env:"POSTGRES_DSN"`
	PostgresHost     string `env:"POSTGRES_HOST"`
	PostgresPort     int    `env:"POSTGRES_PORT" envDefault:"5432"`
	PostgresUser     string `env:"POSTGRES_USER"`
	PostgresPassword string `env:"POSTGRES_PASSWORD"`
	PostgresDatabase string `env:"POSTGRES_DATABASE"`
	PostgresSSLMode  string `env:"POSTGRES_SSLMODE" envDefault:"prefer"`
}

// TTS configures the Kokoro-FastAPI (OpenAI-compatible) speech server.
type TTS struct {
	URL     string        `env:"URL" envDefault:"http://localhost:8880"`
	Timeout time.Duration `env:"TIMEOUT" envDefault:"5m"`
}

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Prefix: "ABG_"})
	if err != nil {
		return Config{}, err
	}
	if cfg.DB.Type == DialectSQLite && cfg.DB.SQLitePath == "" {
		cfg.DB.SQLitePath = filepath.Join(cfg.DataDir, "audiobook-generator.db")
	}
	return cfg, cfg.Validate()
}

// Validate checks values that env parsing alone cannot.
func (c Config) Validate() error {
	var errs []error
	switch c.DB.Type {
	case DialectSQLite:
		if c.DB.SQLitePath == "" {
			errs = append(errs, errors.New("ABG_DB_SQLITE_PATH must not be empty"))
		}
	case DialectPostgres:
		if c.DB.PostgresDSN == "" && (c.DB.PostgresHost == "" || c.DB.PostgresUser == "" || c.DB.PostgresDatabase == "") {
			errs = append(errs, errors.New("postgres needs ABG_DB_POSTGRES_DSN or ABG_DB_POSTGRES_HOST, _USER and _DATABASE"))
		}
	default:
		errs = append(errs, fmt.Errorf("ABG_DB_TYPE must be %q or %q, got %q", DialectSQLite, DialectPostgres, c.DB.Type))
	}
	if _, err := url.ParseRequestURI(c.TTS.URL); err != nil {
		errs = append(errs, fmt.Errorf("ABG_TTS_URL: %w", err))
	}
	if _, err := c.SlogLevel(); err != nil {
		errs = append(errs, err)
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("ABG_LOG_FORMAT must be json or text, got %q", c.LogFormat))
	}
	return errors.Join(errs...)
}

// SlogLevel parses LogLevel.
func (c Config) SlogLevel() (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return 0, fmt.Errorf("ABG_LOG_LEVEL: %w", err)
	}
	return l, nil
}

// PostgresConnString returns the libpq/pgx connection string.
func (d Database) PostgresConnString() string {
	if d.PostgresDSN != "" {
		return d.PostgresDSN
	}
	kv := []string{
		"host=" + quoteDSN(d.PostgresHost),
		fmt.Sprintf("port=%d", d.PostgresPort),
		"user=" + quoteDSN(d.PostgresUser),
		"dbname=" + quoteDSN(d.PostgresDatabase),
		"sslmode=" + quoteDSN(d.PostgresSSLMode),
	}
	if d.PostgresPassword != "" {
		kv = append(kv, "password="+quoteDSN(d.PostgresPassword))
	}
	return strings.Join(kv, " ")
}

// quoteDSN quotes a keyword/value connection string value.
func quoteDSN(v string) string {
	if v != "" && !strings.ContainsAny(v, ` '\`) {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}
