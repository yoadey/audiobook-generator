package tts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Engine kinds besides Kokoro.
const (
	// KindOpenAI is any OpenAI-compatible speech server
	// (POST /v1/audio/speech with model, input, voice, speed,
	// response_format), e.g. a second Kokoro or Orpheus-FastAPI.
	KindOpenAI = "openai"
	// KindChatterbox is Chatterbox-TTS-Server (devnen): with the original
	// model it speaks emotions itself.
	KindChatterbox = "chatterbox"
)

// Chatterbox variants the server runs (its model.repo_id).
const (
	// VariantOriginal (0.5B) takes how strongly to act (exaggeration).
	VariantOriginal = "original"
	// VariantTurbo (350M) is faster but ignores it.
	VariantTurbo = "turbo"
)

// EngineConfig configures an additional speech engine. Its voices are
// written "<name>:<voice>" (e.g. "chatterbox:Emily.wav"); voices without a
// prefix are Kokoro's.
type EngineConfig struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
	// Model is sent as "model" to an OpenAI-compatible engine (default
	// "tts-1").
	Model string `json:"model,omitempty"`
	// Variant of a Chatterbox engine: original (default) or turbo.
	Variant string `json:"variant,omitempty"`
	// Concurrency bounds parallel requests to this engine (default 1).
	Concurrency int `json:"concurrency,omitempty"`
	// Timeout of one request (default 10m).
	Timeout Duration `json:"timeout,omitempty"`
	// Voices it offers when it cannot list them itself.
	Voices []string `json:"voices,omitempty"`
	// Gain evens out its loudness against Kokoro's (default 1).
	Gain float64 `json:"gain,omitempty"`
	// Speed: the engine takes a speed (default true for openai, false for
	// chatterbox, whose pace is left to it).
	Speed *bool `json:"speed,omitempty"`
}

// Engines is a list of engine configurations, read from JSON (an
// environment variable).
type Engines []EngineConfig

// UnmarshalText reads the JSON list; empty is none.
func (e *Engines) UnmarshalText(b []byte) error {
	if strings.TrimSpace(string(b)) == "" {
		*e = nil
		return nil
	}
	var list []EngineConfig
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("engines: %w", err)
	}
	*e = list
	return nil
}

// Duration is a time.Duration written as in Go ("90s", "10m") in JSON.
type Duration time.Duration

// UnmarshalJSON reads "90s" or a number of seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if v, err := time.ParseDuration(s); err == nil {
		*d = Duration(v)
		return nil
	}
	var secs float64
	if _, err := fmt.Sscan(s, &secs); err != nil {
		return fmt.Errorf("duration %q: want e.g. \"90s\"", s)
	}
	*d = Duration(secs * float64(time.Second))
	return nil
}

var engineNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// EngineVoiceRe is what an engine's voice name may be.
var EngineVoiceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Validate checks an engine configuration and fills in defaults.
func (c *EngineConfig) Validate() error {
	if !engineNameRe.MatchString(c.Name) {
		return fmt.Errorf("engine name %q: lower-case letters, digits and dashes", c.Name)
	}
	switch c.Kind {
	case KindOpenAI, KindChatterbox:
	default:
		return fmt.Errorf("engine %s: kind must be %q or %q, got %q", c.Name, KindOpenAI, KindChatterbox, c.Kind)
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("engine %s: url must be http(s), got %q", c.Name, c.URL)
	}
	for _, v := range c.Voices {
		if !EngineVoiceRe.MatchString(v) {
			return fmt.Errorf("engine %s: voice %q: letters, digits, dots, dashes and underscores", c.Name, v)
		}
	}
	if c.Concurrency < 0 || c.Gain < 0 || c.Timeout < 0 {
		return fmt.Errorf("engine %s: concurrency, gain and timeout must not be negative", c.Name)
	}
	switch {
	case c.Kind != KindChatterbox:
	case c.Variant == "":
		c.Variant = VariantOriginal
	case c.Variant != VariantOriginal && c.Variant != VariantTurbo:
		return fmt.Errorf("engine %s: variant must be %q or %q, got %q", c.Name, VariantOriginal, VariantTurbo, c.Variant)
	}
	if c.Concurrency == 0 {
		c.Concurrency = 1
	}
	if c.Timeout == 0 {
		c.Timeout = Duration(10 * time.Minute)
	}
	if c.Gain == 0 {
		c.Gain = 1
	}
	if c.Speed == nil {
		speed := c.Kind != KindChatterbox
		c.Speed = &speed
	}
	return nil
}

// SplitVoice splits an engine voice ("chatterbox:erin"); ok is false for
// a Kokoro voice or mix.
func SplitVoice(voice string) (engine, name string, ok bool) {
	engine, name, ok = strings.Cut(voice, ":")
	if !ok || !engineNameRe.MatchString(engine) || !EngineVoiceRe.MatchString(name) {
		return "", "", false
	}
	return engine, name, true
}

// ErrUnknownEngine is returned for a voice of an engine that is not
// configured.
var ErrUnknownEngine = errors.New("tts: the voice's engine is not configured")

// Engine is a configured engine as the API shows it.
type Engine struct {
	Name string
	Kind string
	// Emotions: it speaks emotions itself (no pace and loudness for them).
	Emotions bool
	// Speed: it takes a voice's speed.
	Speed  bool
	Voices []string
	// Err tells why its voices could not be listed.
	Err string
}

// Router sends a request to Kokoro or, for "<engine>:<voice>" voices, to
// that engine. It is what the renderer and the API talk to.
type Router struct {
	*Client
	engines map[string]*remote
	names   []string
}

// NewRouter returns a router over Kokoro and the given engines (validated).
func NewRouter(kokoro *Client, engines []EngineConfig) (*Router, error) {
	r := &Router{Client: kokoro, engines: map[string]*remote{}}
	for _, c := range engines {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if r.engines[c.Name] != nil {
			return nil, fmt.Errorf("engine %s configured twice", c.Name)
		}
		r.engines[c.Name] = newRemote(c)
		r.names = append(r.names, c.Name)
	}
	slices.Sort(r.names)
	return r, nil
}

func (r *Router) route(voice string) (*remote, string, error) {
	engine, name, ok := SplitVoice(voice)
	if !ok {
		return nil, voice, nil
	}
	e := r.engines[engine]
	if e == nil {
		return nil, "", &HTTPError{Status: 400, Body: fmt.Sprintf("%v: %q", ErrUnknownEngine, engine)}
	}
	return e, name, nil
}

// Synthesize renders text with the voice's engine.
func (r *Router) Synthesize(ctx context.Context, s Speech) ([]byte, error) {
	e, name, err := r.route(s.Voice)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return r.Client.Synthesize(ctx, s)
	}
	s.Voice = name
	return e.synthesize(ctx, s)
}

// SynthesizeTimed renders with word times; other engines have none.
func (r *Router) SynthesizeTimed(ctx context.Context, s Speech) ([]byte, []Word, error) {
	e, _, err := r.route(s.Voice)
	if err != nil {
		return nil, nil, err
	}
	if e != nil {
		return nil, nil, ErrNoTimestamps
	}
	return r.Client.SynthesizeTimed(ctx, s)
}

// Fingerprint identifies the settings of a voice's engine that change
// its audio ("" for Kokoro's voices).
func (r *Router) Fingerprint(voice string) string {
	e, _, err := r.route(voice)
	if err != nil || e == nil {
		return ""
	}
	return e.fingerprint()
}

// Configured reports whether a voice's engine is configured (Kokoro's
// voices always are).
func (r *Router) Configured(voice string) bool {
	_, _, err := r.route(voice)
	return err == nil
}

// Expressive reports whether a voice's engine speaks emotions itself.
func (r *Router) Expressive(voice string) bool {
	e, _, err := r.route(voice)
	return err == nil && e != nil && e.expressive()
}

// TakesSpeed reports whether a voice's engine takes a speed.
func (r *Router) TakesSpeed(voice string) bool {
	e, _, err := r.route(voice)
	return err == nil && (e == nil || *e.cfg.Speed)
}

// Engines lists the configured engines with their voices.
func (r *Router) Engines(ctx context.Context) []Engine {
	out := make([]Engine, 0, len(r.names))
	for _, n := range r.names {
		e := r.engines[n]
		info := Engine{Name: n, Kind: e.cfg.Kind, Emotions: e.expressive(), Speed: *e.cfg.Speed}
		vs, err := e.voices(ctx)
		if err != nil {
			info.Err = err.Error()
		}
		info.Voices = vs
		out = append(out, info)
	}
	return out
}

func (r *remote) expressive() bool {
	return r.cfg.Kind == KindChatterbox && r.cfg.Variant == VariantOriginal
}
