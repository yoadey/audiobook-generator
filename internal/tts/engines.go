package tts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	// KindGemini is the Gemini API's speech generation (Google AI Studio),
	// a cloud service with an API key and a daily quota.
	KindGemini = "gemini"
)

// Chatterbox variants the server runs (its model.repo_id).
const (
	// VariantOriginal (0.5B) takes how strongly to act (exaggeration).
	VariantOriginal = "original"
	// VariantTurbo (350M) is faster but ignores it.
	VariantTurbo = "turbo"
	// VariantNano (110M) is Turbo's smaller sibling, the only one near real
	// time on CPU; it ignores exaggeration too. Needs the patched server
	// image (images/chatterbox-nano).
	VariantNano = "nano"
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
	// Variant of a Chatterbox engine: original (default), turbo or nano.
	Variant string `json:"variant,omitempty"`
	// Concurrency bounds parallel requests to this engine (default 1).
	Concurrency int `json:"concurrency,omitempty"`
	// Timeout of one request (default 10m).
	Timeout Duration `json:"timeout,omitempty"`
	// Voices it offers when it cannot list them itself.
	Voices []string `json:"voices,omitempty"`
	// DefaultVoice narrates its books unless they choose one (default: for
	// Chatterbox Emily.wav, else the first of Voices).
	DefaultVoice string `json:"defaultVoice,omitempty"`
	// Gain evens out its loudness against Kokoro's (default 1).
	Gain float64 `json:"gain,omitempty"`
	// Speed: the engine takes a speed (default true for openai, false for
	// chatterbox, whose pace is left to it).
	Speed *bool `json:"speed,omitempty"`
	// MaxChars is the longest text sent in one request: the pieces of a
	// paragraph in one voice are joined up to it (default 1000 for
	// chatterbox, which splits longer text by sentences itself; 2000 for
	// gemini, whose quota counts requests; 400 for openai).
	MaxChars int `json:"maxChars,omitempty"`
	// APIKeyEnv names the environment variable that holds a Gemini
	// engine's API key.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
	// Limits of a Gemini engine's quota (0: unknown; a limit the engine
	// names in a 429 answer is used meanwhile).
	Limits EngineLimits `json:"limits,omitzero"`
	// QuotaTimezone is where a Gemini engine's daily quota resets at
	// midnight (default America/Los_Angeles, as Google's does).
	QuotaTimezone string `json:"quotaTimezone,omitempty"`
	// Emotions: a Gemini or OpenAI-compatible engine is asked to speak the
	// emotion of a quote and thoughts (Gemini: "Say angrily: …" before the
	// text; OpenAI-compatible: "instructions", as gpt-4o-mini-tts and
	// Qwen3-TTS take them; default false: they are given by loudness, like
	// for Kokoro).
	Emotions *bool `json:"emotions,omitempty"`
	// JoinParagraphs: consecutive paragraphs in one voice and style are
	// read in one request, up to MaxChars (default true for gemini, whose
	// quota counts requests; false for others, which read across a
	// paragraph end only where the chapter preparation found it flows on).
	JoinParagraphs *bool `json:"joinParagraphs,omitempty"`
}

// MaxRequestChars bounds MaxChars as set at runtime, by kind: a Gemini
// answer must stay within its audio limit, OpenAI-compatible servers
// often take at most 4096 characters, Chatterbox splits long text itself.
func MaxRequestChars(kind string) int {
	switch kind {
	case KindGemini:
		return 5000
	case KindOpenAI:
		return 4000
	}
	return 10000
}

// MinRequestChars bounds MaxChars as set at runtime from below.
const MinRequestChars = 100

// RequestOptions are how long an engine's requests get.
type RequestOptions struct {
	MaxChars       int
	JoinParagraphs bool
}

// EngineLimits are a cloud engine's quota; 0 means none. Tokens are the
// input (text) tokens.
type EngineLimits struct {
	RPM int `json:"rpm,omitempty"`
	TPM int `json:"tpm,omitempty"`
	RPD int `json:"rpd,omitempty"`
	TPD int `json:"tpd,omitempty"`
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
	if !engineNameRe.MatchString(c.Name) || c.Name == KokoroEngine {
		return fmt.Errorf("engine name %q: lower-case letters, digits and dashes, not %q", c.Name, KokoroEngine)
	}
	switch c.Kind {
	case KindOpenAI, KindChatterbox, KindGemini:
	default:
		return fmt.Errorf("engine %s: kind must be %q, %q or %q, got %q",
			c.Name, KindOpenAI, KindChatterbox, KindGemini, c.Kind)
	}
	switch {
	case c.Kind == KindGemini:
		if err := c.validateGemini(); err != nil {
			return err
		}
	case c.APIKeyEnv != "" || c.Limits != (EngineLimits{}) || c.QuotaTimezone != "":
		return fmt.Errorf("engine %s: apiKeyEnv, limits and quotaTimezone are for gemini engines", c.Name)
	case c.Emotions != nil && *c.Emotions && c.Kind != KindOpenAI:
		return fmt.Errorf("engine %s: emotions are for gemini and openai engines", c.Name)
	}
	if c.Emotions == nil {
		off := false
		c.Emotions = &off
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("engine %s: url must be http(s), got %q", c.Name, c.URL)
	}
	voices := slices.Clone(c.Voices)
	if c.DefaultVoice != "" {
		voices = append(voices, c.DefaultVoice)
	}
	for _, v := range voices {
		if !EngineVoiceRe.MatchString(v) {
			return fmt.Errorf("engine %s: voice %q: letters, digits, dots, dashes and underscores", c.Name, v)
		}
	}
	if c.Concurrency < 0 || c.Gain < 0 || c.Timeout < 0 || c.MaxChars < 0 {
		return fmt.Errorf("engine %s: concurrency, gain, timeout and maxChars must not be negative", c.Name)
	}
	switch {
	case c.Kind != KindChatterbox:
	case c.Variant == "":
		c.Variant = VariantOriginal
	case c.Variant != VariantOriginal && c.Variant != VariantTurbo && c.Variant != VariantNano:
		return fmt.Errorf("engine %s: variant must be %q, %q or %q, got %q",
			c.Name, VariantOriginal, VariantTurbo, VariantNano, c.Variant)
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
		speed := c.Kind == KindOpenAI
		c.Speed = &speed
	}
	if c.JoinParagraphs == nil {
		join := c.Kind == KindGemini
		c.JoinParagraphs = &join
	}
	if c.MaxChars == 0 {
		switch c.Kind {
		case KindChatterbox:
			c.MaxChars = 1000
		case KindGemini:
			c.MaxChars = 4000
		default:
			c.MaxChars = 400
		}
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
	// Variant of a Chatterbox engine ("" otherwise).
	Variant string
	// Model of a Gemini engine ("" otherwise).
	Model string
	// Emotions: it speaks emotions itself (no pace and loudness for them).
	Emotions bool
	// Speed: it takes a voice's speed.
	Speed  bool
	Voices []string
	// DefaultVoice narrates its books unless they choose one.
	DefaultVoice string
	// Err tells why its voices could not be listed.
	Err string
	// Quota of a cloud engine (nil for others).
	Quota *Quota
	// Requests: how long its requests get (as set now), and Defaults: as
	// configured; MaxChars is the most a request may be set to.
	Requests, Defaults RequestOptions
	MaxChars           int
}

// Router sends a request to Kokoro or, for "<engine>:<voice>" voices, to
// that engine. It is what the renderer and the API talk to. Kokoro is
// optional: a deployment may speak with other engines only.
type Router struct {
	kokoro  *Client // nil: no Kokoro
	engines map[string]*remote
	names   []string
	// mp3 encodes PCM for previews of engines that speak PCM only.
	mp3 func(ctx context.Context, pcm []byte) ([]byte, error)
}

// ErrNoKokoro is returned for a Kokoro voice when Kokoro is not configured.
var ErrNoKokoro = errors.New("tts: Kokoro is not configured")

// NewRouter returns a router over Kokoro (nil for none) and the given
// engines (validated); at least one of them is needed. API keys are read
// from the environment.
func NewRouter(kokoro *Client, engines []EngineConfig) (*Router, error) {
	if kokoro == nil && len(engines) == 0 {
		return nil, errors.New("no speech engine: configure Kokoro or another engine")
	}
	r := &Router{kokoro: kokoro, engines: map[string]*remote{}}
	for _, c := range engines {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if r.engines[c.Name] != nil {
			return nil, fmt.Errorf("engine %s configured twice", c.Name)
		}
		rem := newRemote(c)
		if c.Kind == KindGemini {
			key := os.Getenv(c.APIKeyEnv)
			if key == "" {
				return nil, fmt.Errorf("engine %s: the API key variable %s is empty", c.Name, c.APIKeyEnv)
			}
			rem.gemini = newGemini(c, key)
		}
		r.engines[c.Name] = rem
		r.names = append(r.names, c.Name)
	}
	slices.Sort(r.names)
	return r, nil
}

func (r *Router) route(voice string) (*remote, string, error) {
	engine, name, ok := SplitVoice(voice)
	if !ok {
		if r.kokoro == nil {
			return nil, "", &HTTPError{Status: 400, Body: ErrNoKokoro.Error()}
		}
		return nil, voice, nil
	}
	e := r.engines[engine]
	if e == nil {
		return nil, "", &HTTPError{Status: 400, Body: fmt.Sprintf("%v: %q", ErrUnknownEngine, engine)}
	}
	return e, name, nil
}

// SetUsageStore keeps the daily usage of cloud engines (call before the
// first request).
func (r *Router) SetUsageStore(u UsageStore) {
	for _, e := range r.engines {
		if e.gemini != nil {
			e.gemini.usage = u
		}
	}
}

// SetMP3Encoder sets how previews of engines that speak PCM only are
// encoded (call before the first request).
func (r *Router) SetMP3Encoder(f func(ctx context.Context, pcm []byte) ([]byte, error)) { r.mp3 = f }

// Synthesize renders text with the voice's engine.
func (r *Router) Synthesize(ctx context.Context, s Speech) ([]byte, error) {
	e, name, err := r.route(s.Voice)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return r.kokoro.Synthesize(ctx, s)
	}
	s.Voice = name
	if e.gemini == nil || s.Format != "mp3" {
		return e.synthesize(ctx, s)
	}
	if r.mp3 == nil {
		return nil, fmt.Errorf("%s: no MP3 encoder for previews", e.cfg.Name)
	}
	// A preview is not queued behind a chapter's requests.
	s.Format = "pcm"
	pcm, err := e.geminiSpeak(ctx, s, true)
	if err != nil {
		return nil, err
	}
	return r.mp3(ctx, pcm)
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
	return r.kokoro.SynthesizeTimed(ctx, s)
}

// EngineNames lists the configured engines besides Kokoro, sorted.
func (r *Router) EngineNames() []string { return slices.Clone(r.names) }

// Kokoro reports whether Kokoro is configured.
func (r *Router) Kokoro() bool { return r.kokoro != nil }

// BaseURL is Kokoro's URL ("" without Kokoro).
func (r *Router) BaseURL() string {
	if r.kokoro == nil {
		return ""
	}
	return r.kokoro.BaseURL()
}

// Voices lists Kokoro's voices (none without Kokoro).
func (r *Router) Voices(ctx context.Context) ([]Voice, error) {
	if r.kokoro == nil {
		return nil, nil
	}
	return r.kokoro.Voices(ctx)
}

// EngineVoices lists an engine's voices (names without the prefix).
func (r *Router) EngineVoices(ctx context.Context, engine string) ([]string, error) {
	e := r.engines[engine]
	if e == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	return e.voices(ctx, false)
}

// UploadReference stores a reference clip on an engine that clones voices
// (Chatterbox); it is then the voice "<engine>:<name>".
func (r *Router) UploadReference(ctx context.Context, engine, name string, wav []byte) error {
	e := r.engines[engine]
	if e == nil {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	if !EngineVoiceRe.MatchString(name) {
		return fmt.Errorf("voice name %q: letters, digits, dots, dashes and underscores", name)
	}
	return e.upload(ctx, name, wav)
}

// Clones reports whether an engine takes reference clips to clone.
func (r *Router) Clones(engine string) bool {
	e := r.engines[engine]
	return e != nil && e.cfg.Kind == KindChatterbox
}

// Mixable reports whether a voice's engine mixes voices (Kokoro only).
func (r *Router) Mixable(voice string) bool {
	_, _, ok := SplitVoice(voice)
	return !ok
}

// DefaultVoice is the voice books of an engine are narrated with unless
// they choose one ("<engine>:<voice>"; "" when the engine has none).
func (r *Router) DefaultVoice(engine string) string {
	e := r.engines[engine]
	if e == nil {
		return ""
	}
	v := e.cfg.DefaultVoice
	switch {
	case v != "":
	case e.cfg.Kind == KindChatterbox:
		v = chatterboxDefaultVoice
	case e.cfg.Kind == KindGemini:
		v = geminiDefaultVoice
	case len(e.cfg.Voices) > 0:
		v = e.cfg.Voices[0]
	default:
		return ""
	}
	return engine + ":" + v
}

// chatterboxDefaultVoice is Chatterbox-TTS-Server's default voice.
const chatterboxDefaultVoice = "Emily.wav"

// ResolveDefault returns the narrator of books without their own: voice
// (ABG_TTS_DEFAULT_VOICE) when its engine is configured, else the default
// voice of the first engine.
func (r *Router) ResolveDefault(voice string) (string, error) {
	if r.Configured(voice) {
		return voice, nil
	}
	if _, _, ok := SplitVoice(voice); ok {
		return "", fmt.Errorf("the engine of the default voice %q is not configured", voice)
	}
	for _, n := range r.names {
		if v := r.DefaultVoice(n); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("the default voice %q has no engine, and no engine has a default voice", voice)
}

// EngineOf names the engine of a voice ("kokoro" for Kokoro's voices).
func EngineOf(voice string) string {
	if e, _, ok := SplitVoice(voice); ok {
		return e
	}
	return KokoroEngine
}

// KokoroEngine is the name the API gives Kokoro.
const KokoroEngine = "kokoro"

// Fingerprint identifies the settings of a voice's engine that change
// its audio ("" for Kokoro's voices).
func (r *Router) Fingerprint(voice string) string {
	e, _, err := r.route(voice)
	if err != nil || e == nil {
		return ""
	}
	return e.fingerprint()
}

// Configured reports whether a voice's engine is configured.
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

// JoinLimit is the longest text a voice's engine takes in one request
// (0 for Kokoro's voices: their pieces are never joined).
func (r *Router) JoinLimit(voice string) int {
	e, _, err := r.route(voice)
	if err != nil || e == nil {
		return 0
	}
	return e.requestOptions().MaxChars
}

// JoinParagraphs reports whether a voice's engine reads consecutive
// paragraphs in one request.
func (r *Router) JoinParagraphs(voice string) bool {
	e, _, err := r.route(voice)
	return err == nil && e != nil && e.requestOptions().JoinParagraphs
}

// RequestOverride changes some of an engine's request options while
// running (nil fields keep the configuration).
type RequestOverride struct {
	MaxChars       *int
	JoinParagraphs *bool
}

// overridden applies an override to the configured options and checks
// the result.
func (e *remote) overridden(o *RequestOverride) (RequestOptions, error) {
	opts := e.defaultOptions()
	if o == nil {
		return opts, nil
	}
	if o.MaxChars != nil {
		if limit := MaxRequestChars(e.cfg.Kind); *o.MaxChars < MinRequestChars || *o.MaxChars > limit {
			return opts, fmt.Errorf("maxChars must be between %d and %d for %s", MinRequestChars, limit, e.cfg.Name)
		}
		opts.MaxChars = *o.MaxChars
	}
	if o.JoinParagraphs != nil {
		opts.JoinParagraphs = *o.JoinParagraphs
	}
	return opts, nil
}

// CheckRequestOverride reports whether an override can be applied.
func (r *Router) CheckRequestOverride(engine string, o *RequestOverride) error {
	e := r.engines[engine]
	if e == nil {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	_, err := e.overridden(o)
	return err
}

// SetRequestOverride changes how long an engine's requests get (nil:
// back to its configuration).
func (r *Router) SetRequestOverride(engine string, o *RequestOverride) error {
	e := r.engines[engine]
	if e == nil {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	opts, err := e.overridden(o)
	if err != nil {
		return err
	}
	e.options.Store(&opts)
	return nil
}

// Engines lists the configured engines with their voices.
func (r *Router) Engines(ctx context.Context) []Engine {
	out := make([]Engine, 0, len(r.names))
	for _, n := range r.names {
		e := r.engines[n]
		info := Engine{Name: n, Kind: e.cfg.Kind, Variant: e.cfg.Variant, Emotions: e.expressive(), Speed: *e.cfg.Speed,
			DefaultVoice: r.DefaultVoice(n)}
		if e.gemini != nil {
			info.Model = e.cfg.Model
		}
		info.Requests, info.Defaults, info.MaxChars = e.requestOptions(), e.defaultOptions(), MaxRequestChars(e.cfg.Kind)
		vs, err := e.voices(ctx, false)
		if err != nil {
			info.Err = err.Error()
		}
		info.Voices = vs
		if e.gemini != nil {
			// A usage that cannot be read shows as none, with the error.
			q, err := e.quotaStatus(ctx)
			if err != nil {
				q.LastError = strings.TrimPrefix(q.LastError+"; today's usage could not be read: "+err.Error(), "; ")
			}
			info.Quota = q
		}
		out = append(out, info)
	}
	return out
}

func (r *remote) expressive() bool {
	switch r.cfg.Kind {
	case KindChatterbox:
		return r.cfg.Variant == VariantOriginal
	case KindGemini, KindOpenAI:
		return *r.cfg.Emotions
	}
	return false
}
