// Package config loads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/yoadey/audiobook-generator/internal/tts"
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

	// MaxUploadMB bounds EPUB uploads (and every other request body).
	MaxUploadMB int64 `env:"MAX_UPLOAD_MB" envDefault:"512"`

	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	// FFmpeg is the ffmpeg binary used to encode chapter audio.
	FFmpeg string `env:"FFMPEG" envDefault:"ffmpeg"`
	// FFprobe measures chapter files when assembling downloads.
	FFprobe string `env:"FFPROBE" envDefault:"ffprobe"`
	// AudioBitrate of the AAC chapter files.
	AudioBitrate string `env:"AUDIO_BITRATE" envDefault:"64k"`

	// PublicURL is the external base URL ("https://audiobooks.example.org")
	// used in podcast feeds; empty takes it from each request.
	PublicURL string `env:"PUBLIC_URL"`
	// FeedSecret signs the podcast feed URLs (which work without a login);
	// empty disables the feeds. Changing it invalidates every feed URL.
	FeedSecret string `env:"FEED_SECRET"`

	// OIDC enables sign-in (when the issuer is set).
	OIDC OIDC `envPrefix:"OIDC_"`

	DB  Database `envPrefix:"DB_"`
	TTS TTS      `envPrefix:"TTS_"`
	LLM LLM      `envPrefix:"LLM_"`
	// Casting is the voice casting.
	Casting Casting `envPrefix:"CASTING_"`
	// VoiceDesign designs voices from descriptions (when URL is set).
	VoiceDesign VoiceDesign `envPrefix:"VOICE_DESIGN_"`
}

// VoiceDesign configures the voice design server, whose clips are cloned
// by an engine (Chatterbox).
type VoiceDesign struct {
	// URL of the server; empty disables voice design.
	URL string `env:"URL"`
	// Engine that clones the designed clips (a Chatterbox engine of
	// ABG_TTS_ENGINES).
	Engine string `env:"ENGINE" envDefault:"chatterbox"`
	// Deployment, when set, is the server's Deployment in the app's
	// namespace: scaled up for designs and down when idle.
	Deployment string `env:"DEPLOYMENT"`
	// Idle is how long the server keeps running after the last design.
	Idle time.Duration `env:"IDLE" envDefault:"10m"`
	// Timeout of one design (minutes on CPU).
	Timeout time.Duration `env:"TIMEOUT" envDefault:"30m"`
	// ReadyTimeout is how long a design waits for the server to start
	// (its first start downloads the model).
	ReadyTimeout time.Duration `env:"READY_TIMEOUT" envDefault:"30m"`
}

// LLM configures the LLM layer. Providers can also be managed in the UI.
type LLM struct {
	// Providers is a JSON array of provider definitions (the Helm chart
	// renders it from llm.providers); see llm.ProviderConfig.
	Providers string `env:"PROVIDERS"`
	// MaxWait is how long a request waits for a provider's per-minute
	// window before falling back to the next provider.
	MaxWait time.Duration `env:"MAX_WAIT" envDefault:"70s"`
	// AnalysisBatchTokens is the input size of one character analysis
	// request (fewer, larger requests save daily request quota).
	AnalysisBatchTokens int `env:"ANALYSIS_BATCH_TOKENS" envDefault:"30000"`
	// AttributionTokens is the input size of one speaker attribution
	// request (longer chapters are sent in parts).
	AttributionTokens int `env:"ATTRIBUTION_TOKENS" envDefault:"12000"`
}

// OIDC configures sign-in with OpenID Connect (e.g. Zitadel).
type OIDC struct {
	// Issuer URL; empty disables authentication.
	Issuer       string `env:"ISSUER"`
	ClientID     string `env:"CLIENT_ID"`
	ClientSecret string `env:"CLIENT_SECRET"`
	// Scopes, separated by spaces.
	Scopes string `env:"SCOPES" envDefault:"openid profile email"`
	// SessionKey encrypts the session cookie (at least 32 characters).
	SessionKey string `env:"SESSION_KEY"`
	// SessionTTL is how long a sign-in lasts without use.
	SessionTTL time.Duration `env:"SESSION_TTL" envDefault:"720h"`
}

// Casting configures the voice casting.
type Casting struct {
	// MainCharacters is how many speaking characters get their own voice
	// recipe; the others get pool voices.
	MainCharacters int `env:"MAIN_CHARACTERS" envDefault:"50"`
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

// TTS configures the speech engines: Kokoro-FastAPI (unless Kokoro is
// false) and the others (Engines).
type TTS struct {
	// Kokoro: Kokoro-FastAPI at URL narrates; false for a deployment that
	// speaks with other engines only.
	Kokoro  bool          `env:"KOKORO" envDefault:"true"`
	URL     string        `env:"URL" envDefault:"http://localhost:8880"`
	Timeout time.Duration `env:"TIMEOUT" envDefault:"5m"`
	// Concurrency is the number of parallel synthesis requests (roughly
	// Kokoro replicas × 1-2).
	Concurrency int `env:"CONCURRENCY" envDefault:"2"`
	// DefaultVoice narrates books that have no voice set; its engine is
	// the default one. Without Kokoro, a Kokoro voice here stands for the
	// first engine's default voice.
	DefaultVoice string `env:"DEFAULT_VOICE" envDefault:"af_heart"`
	// Engines are speech servers besides Kokoro (a JSON list, see
	// tts.EngineConfig), whose voices are written "<name>:<voice>".
	Engines tts.Engines `env:"ENGINES"`
	// UnloadIdle is how long a Chatterbox engine speaks nothing before its
	// model is unloaded (when the setting allows it; 0: never).
	UnloadIdle time.Duration `env:"UNLOAD_IDLE" envDefault:"10m"`
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
	if _, err := url.ParseRequestURI(c.TTS.URL); c.TTS.Kokoro && err != nil {
		errs = append(errs, fmt.Errorf("ABG_TTS_URL: %w", err))
	}
	if !c.TTS.Kokoro && len(c.TTS.Engines) == 0 {
		errs = append(errs, errors.New("ABG_TTS_KOKORO=false needs another engine (ABG_TTS_ENGINES)"))
	}
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("ABG_PUBLIC_URL must be an http(s) URL, got %q", c.PublicURL))
		}
	}
	if c.OIDC.Issuer != "" {
		if u, err := url.Parse(c.OIDC.Issuer); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("ABG_OIDC_ISSUER must be an http(s) URL, got %q", c.OIDC.Issuer))
		}
		if c.PublicURL == "" {
			errs = append(errs, errors.New("ABG_PUBLIC_URL is required with ABG_OIDC_ISSUER (redirect URI, secure cookies)"))
		}
		if c.OIDC.ClientID == "" {
			errs = append(errs, errors.New("ABG_OIDC_CLIENT_ID is required with ABG_OIDC_ISSUER"))
		}
		if len(c.OIDC.SessionKey) < 32 {
			errs = append(errs, errors.New("ABG_OIDC_SESSION_KEY must be at least 32 characters with ABG_OIDC_ISSUER"))
		}
		if !slices.Contains(strings.Fields(c.OIDC.Scopes), "openid") {
			errs = append(errs, errors.New("ABG_OIDC_SCOPES must contain openid"))
		}
		if c.OIDC.SessionTTL < time.Minute {
			errs = append(errs, fmt.Errorf("ABG_OIDC_SESSION_TTL must be at least 1m, got %s", c.OIDC.SessionTTL))
		}
	}
	if c.FeedSecret != "" && len(c.FeedSecret) < 16 {
		errs = append(errs, errors.New("ABG_FEED_SECRET must be at least 16 characters"))
	}
	if c.TTS.UnloadIdle < 0 {
		errs = append(errs, fmt.Errorf("ABG_TTS_UNLOAD_IDLE must not be negative, got %s", c.TTS.UnloadIdle))
	}
	if c.TTS.Concurrency < 1 {
		errs = append(errs, fmt.Errorf("ABG_TTS_CONCURRENCY must be at least 1, got %d", c.TTS.Concurrency))
	}
	if c.VoiceDesign.URL != "" {
		if u, err := url.Parse(c.VoiceDesign.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("ABG_VOICE_DESIGN_URL must be an http(s) URL, got %q", c.VoiceDesign.URL))
		}
		if c.VoiceDesign.Idle < 0 || c.VoiceDesign.Timeout <= 0 || c.VoiceDesign.ReadyTimeout <= 0 {
			errs = append(errs, errors.New("ABG_VOICE_DESIGN_IDLE, _TIMEOUT and _READY_TIMEOUT must be positive"))
		}
	}
	for i := range c.TTS.Engines {
		if err := c.TTS.Engines[i].Validate(); err != nil {
			errs = append(errs, fmt.Errorf("ABG_TTS_ENGINES: %w", err))
		}
	}
	if c.TTS.DefaultVoice == "" {
		errs = append(errs, errors.New("ABG_TTS_DEFAULT_VOICE must not be empty"))
	}
	if c.Casting.MainCharacters < 1 || c.Casting.MainCharacters > 500 {
		errs = append(errs, fmt.Errorf("ABG_CASTING_MAIN_CHARACTERS must be between 1 and 500, got %d", c.Casting.MainCharacters))
	}
	if c.LLM.AttributionTokens < 1000 {
		errs = append(errs, fmt.Errorf("ABG_LLM_ATTRIBUTION_TOKENS must be at least 1000, got %d", c.LLM.AttributionTokens))
	}
	if c.LLM.AnalysisBatchTokens < 1000 {
		errs = append(errs, fmt.Errorf("ABG_LLM_ANALYSIS_BATCH_TOKENS must be at least 1000, got %d", c.LLM.AnalysisBatchTokens))
	}
	if c.LLM.MaxWait < 0 {
		errs = append(errs, fmt.Errorf("ABG_LLM_MAX_WAIT must not be negative, got %s", c.LLM.MaxWait))
	}
	if c.MaxUploadMB < 1 {
		errs = append(errs, fmt.Errorf("ABG_MAX_UPLOAD_MB must be at least 1, got %d", c.MaxUploadMB))
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
