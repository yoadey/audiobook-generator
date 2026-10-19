package tts

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/quota"
)

// The Gemini API's speech generation (Google AI Studio): prebuilt voices,
// 24 kHz PCM, and a quota that counts requests per minute and per day.
const (
	geminiURL = "https://generativelanguage.googleapis.com/v1beta"
	// DefaultGeminiModel speaks unless an engine names another.
	DefaultGeminiModel = "gemini-3.1-flash-tts-preview"
	geminiDefaultVoice = "Kore"
	// geminiTimezone: Google's daily quotas reset at midnight Pacific time.
	geminiTimezone = "America/Los_Angeles"
	// geminiMaxWait is how long a request waits for the per-minute window;
	// a longer wait defers the job instead.
	geminiMaxWait = 70 * time.Second
	// geminiKeyWait defers jobs while the API key or model is refused.
	geminiKeyWait = 15 * time.Minute
	// geminiMappingVersion changes with geminiStyles.
	geminiMappingVersion = "1"
)

// geminiVoices are the Gemini API's prebuilt voices.
var geminiVoices = []string{
	"Achernar", "Achird", "Algenib", "Algieba", "Alnilam", "Aoede", "Autonoe", "Callirrhoe", "Charon", "Despina",
	"Enceladus", "Erinome", "Fenrir", "Gacrux", "Iapetus", "Kore", "Laomedeia", "Leda", "Orus", "Puck",
	"Pulcherrima", "Rasalgethi", "Sadachbia", "Sadaltager", "Schedar", "Sulafat", "Umbriel", "Vindemiatrix",
	"Zephyr", "Zubenelgenubi",
}

// geminiStyles: how a quote's emotion is asked for ("Say cheerfully: …",
// as Google documents it); the text itself is sent unchanged after it.
var geminiStyles = map[string]string{
	"happy":      "Say happily",
	"sad":        "Say sadly",
	"angry":      "Say angrily",
	"afraid":     "Say fearfully",
	"surprised":  "Say in surprise",
	"whispering": "Whisper",
	"shouting":   "Shout",
	"sarcastic":  "Say sarcastically",
}

const geminiThoughtStyle = "Say softly, as an inner thought"

// geminiStyle is the instruction put before a speech's text ("" for none).
func geminiStyle(s Speech) string {
	if st, ok := geminiStyles[s.Emotion]; ok {
		return st
	}
	if s.Thought {
		return geminiThoughtStyle
	}
	return ""
}

var (
	envNameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	geminiModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// validateGemini checks a Gemini engine and fills in its defaults.
func (c *EngineConfig) validateGemini() error {
	if c.URL == "" {
		c.URL = geminiURL
	}
	c.Model = strings.TrimPrefix(cmp.Or(c.Model, DefaultGeminiModel), "models/")
	if !geminiModelRe.MatchString(c.Model) {
		return fmt.Errorf("engine %s: model %q: letters, digits, dots, dashes and underscores", c.Name, c.Model)
	}
	if !envNameRe.MatchString(c.APIKeyEnv) {
		return fmt.Errorf("engine %s: apiKeyEnv must name the environment variable with the API key", c.Name)
	}
	if c.Speed != nil && *c.Speed {
		return fmt.Errorf("engine %s: gemini takes no speed", c.Name)
	}
	if l := c.Limits; l.RPM < 0 || l.TPM < 0 || l.RPD < 0 || l.TPD < 0 {
		return fmt.Errorf("engine %s: limits must not be negative", c.Name)
	}
	c.QuotaTimezone = cmp.Or(c.QuotaTimezone, geminiTimezone)
	if _, err := time.LoadLocation(c.QuotaTimezone); err != nil {
		return fmt.Errorf("engine %s: quotaTimezone: %w", c.Name, err)
	}
	if c.Emotions == nil {
		off := false
		c.Emotions = &off
	}
	return nil
}

// Usage is what a cloud engine used on one day.
type Usage struct {
	Requests, InputTokens, OutputTokens int64
	// Errors counts failed requests, RateLimited the 429 answers among
	// them.
	Errors, RateLimited int64
}

// UsageStore keeps the daily usage of cloud engines, so a restart keeps
// the daily quota honest.
type UsageStore interface {
	TTSUsage(ctx context.Context, engine, day string) (Usage, error)
	AddTTSUsage(ctx context.Context, engine, day string, u Usage) error
}

// UnavailableError means a cloud engine takes no request before Until
// (its quota is used up, or it refuses the API key); jobs defer until
// then (jobs.Deferrer).
type UnavailableError struct {
	Engine string
	Until  time.Time
	Reason string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s: %s (next attempt %s)", e.Engine, e.Reason, e.Until.UTC().Format(time.RFC3339))
}

// DeferUntil implements jobs.Deferrer.
func (e *UnavailableError) DeferUntil() time.Time { return e.Until }

// gemini is the quota state of a Gemini engine.
type gemini struct {
	key     string
	loc     *time.Location
	limiter *quota.Limiter
	usage   UsageStore // nil: not persisted

	mu      sync.Mutex
	learned EngineLimits // named by 429 answers
}

func newGemini(c EngineConfig, key string) *gemini {
	loc, err := time.LoadLocation(c.QuotaTimezone)
	if err != nil { // validated before
		loc = time.UTC
	}
	g := &gemini{key: key, loc: loc}
	g.limiter = quota.New(g.limits(c.Limits), loc)
	return g
}

// limits are the configured limits, else those the engine named.
func (g *gemini) limits(cfg EngineLimits) quota.Limits {
	g.mu.Lock()
	defer g.mu.Unlock()
	return quota.Limits{
		RPM: cmp.Or(cfg.RPM, g.learned.RPM),
		TPM: cmp.Or(cfg.TPM, g.learned.TPM),
		RPD: cmp.Or(cfg.RPD, g.learned.RPD),
		TPD: cmp.Or(cfg.TPD, g.learned.TPD),
	}
}

// loadDay loads the persisted counters on the first request of a day.
func (g *gemini) loadDay(ctx context.Context, engine string, now time.Time) error {
	day, need := g.limiter.NeedsDay(now)
	if !need {
		return nil
	}
	var u Usage
	if g.usage != nil {
		var err error
		if u, err = g.usage.TTSUsage(ctx, engine, day); err != nil {
			return err
		}
	}
	g.limiter.LoadDay(day, u.Requests, u.InputTokens)
	return nil
}

func (g *gemini) record(ctx context.Context, engine, day string, u Usage) {
	if g.usage == nil {
		return
	}
	// Even when the request's context ended meanwhile.
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = g.usage.AddTTSUsage(bg, engine, day, u)
}

// geminiSpeak renders a speech with a Gemini engine (SampleRate PCM),
// within its quota: a request waits up to geminiMaxWait for the minute's
// window, else an *UnavailableError says when to come back.
func (r *remote) geminiSpeak(ctx context.Context, s Speech) ([]byte, error) {
	g := r.gemini
	text := s.Text
	if *r.cfg.Emotions {
		if style := geminiStyle(s); style != "" {
			text = style + ": " + text
		}
	}
	est := len(text)/4 + 4
	for {
		now := time.Now()
		if err := g.loadDay(ctx, r.cfg.Name, now); err != nil {
			return nil, err
		}
		a := g.limiter.Available(now, est)
		if a.Never {
			return nil, &HTTPError{Status: http.StatusRequestEntityTooLarge, Body: r.cfg.Name + ": " + a.Reason}
		}
		if a.Reason != "" {
			if a.At.Sub(now) > geminiMaxWait {
				return nil, &UnavailableError{Engine: r.cfg.Name, Until: a.At, Reason: a.Reason}
			}
			if err := sleepCtx(ctx, a.At.Sub(now)); err != nil {
				return nil, err
			}
			continue
		}
		release, _, ok := g.limiter.Reserve(now, est)
		if !ok {
			continue
		}
		return r.geminiSend(ctx, s.Voice, text, now, release)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// geminiAnswer is the part of a generateContent answer that matters here.
type geminiAnswer struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// geminiRefusals are finish reasons that mean the text will never be
// spoken (asking again does not help).
var geminiRefusals = map[string]bool{
	"SAFETY": true, "PROHIBITED_CONTENT": true, "BLOCKLIST": true, "SPII": true, "RECITATION": true,
}

// geminiSend sends one request whose slot is reserved, and does the
// bookkeeping: usage, limiter state.
func (r *remote) geminiSend(ctx context.Context, voice, text string, start time.Time, release func(int)) ([]byte, error) {
	g := r.gemini
	day := quota.DayOf(start, g.loc)
	usage := Usage{Requests: 1}
	defer func() { g.record(ctx, r.cfg.Name, day, usage) }()

	data, status, header, err := r.geminiPost(ctx, voice, text)
	if err != nil {
		release(0)
		usage.Errors = 1
		if ctx.Err() == nil {
			g.limiter.Failed(time.Now(), 0, err.Error())
		}
		return nil, fmt.Errorf("%s: %w", r.cfg.Name, err)
	}
	if status != http.StatusOK {
		release(0)
		usage.Errors = 1
		return nil, r.geminiFailed(status, header, data, &usage)
	}
	var ans geminiAnswer
	if err := json.Unmarshal(data, &ans); err != nil {
		release(0)
		usage.Errors = 1
		return nil, fmt.Errorf("%s: unreadable answer: %w", r.cfg.Name, err)
	}
	usage.InputTokens, usage.OutputTokens = ans.UsageMetadata.PromptTokenCount, ans.UsageMetadata.CandidatesTokenCount
	release(int(usage.InputTokens))
	g.limiter.Succeeded()

	finish := ""
	for _, c := range ans.Candidates {
		finish = cmp.Or(finish, c.FinishReason)
		for _, p := range c.Content.Parts {
			if p.InlineData == nil || p.InlineData.Data == "" {
				continue
			}
			audio, err := base64.StdEncoding.DecodeString(p.InlineData.Data)
			if err != nil {
				usage.Errors = 1
				return nil, fmt.Errorf("%s: audio: %w", r.cfg.Name, err)
			}
			pcm, err := geminiPCM(p.InlineData.MimeType, audio, r.cfg.Gain)
			if err != nil {
				usage.Errors = 1
				return nil, fmt.Errorf("%s: %w", r.cfg.Name, err)
			}
			return pcm, nil
		}
	}
	usage.Errors = 1
	if b := ans.PromptFeedback.BlockReason; b != "" || geminiRefusals[finish] {
		return nil, &HTTPError{Status: http.StatusUnprocessableEntity,
			Body: fmt.Sprintf("%s: refused to speak the text (%s)", r.cfg.Name, cmp.Or(b, finish))}
	}
	// Now and then the model answers without audio; asking again helps.
	return nil, fmt.Errorf("%s: no audio in the answer (finish reason %q)", r.cfg.Name, finish)
}

// geminiPost sends a generateContent request.
func (r *remote) geminiPost(ctx context.Context, voice, text string) ([]byte, int, http.Header, error) {
	body, err := json.Marshal(map[string]any{
		"contents": []any{map[string]any{"parts": []any{map[string]any{"text": text}}}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": voice}},
			},
		},
	})
	if err != nil {
		return nil, 0, nil, err
	}
	u := strings.TrimRight(r.cfg.URL, "/") + "/models/" + url.PathEscape(r.cfg.Model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", r.gemini.key)
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// The audio comes base64-encoded in JSON.
	limit := int64(MaxResponseBytes/3*4 + (1 << 20))
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, 0, nil, err
	}
	if int64(len(data)) > limit {
		return nil, 0, nil, errors.New("synthesis response too large")
	}
	return data, resp.StatusCode, resp.Header, nil
}

// geminiFailed turns an error answer into an error, and tells the limiter.
func (r *remote) geminiFailed(status int, header http.Header, data []byte, usage *Usage) error {
	g := r.gemini
	now := time.Now()
	info := parseGeminiError(data)
	msg := r.cfg.Name + ": " + cmp.Or(info.Message, strings.TrimSpace(string(truncateBytes(data, 1024))))
	he := &HTTPError{Status: status, Body: msg}
	switch {
	case status == http.StatusTooManyRequests:
		usage.RateLimited = 1
		if g.learn(info.limits) {
			g.limiter.Configure(g.limits(r.cfg.Limits), g.loc)
		}
		g.limiter.RateLimited(now, cmp.Or(retryAfter(header.Get("Retry-After"), now), info.retryDelay), info.daily, msg)
		until := now.Add(geminiMaxWait)
		if snap := g.limiter.Snapshot(now); snap.CooldownUntil.After(now) {
			until = snap.CooldownUntil
		}
		reason := "rate-limited by the engine"
		if info.daily {
			reason = "daily quota exhausted (the engine answered 429)"
		}
		return &UnavailableError{Engine: r.cfg.Name, Until: until, Reason: reason}
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound ||
		info.Reason == "API_KEY_INVALID":
		// A wrong key or model: wait for the configuration to be fixed.
		g.limiter.Failed(now, geminiKeyWait, msg)
		return &UnavailableError{Engine: r.cfg.Name, Until: now.Add(geminiKeyWait), Reason: msg}
	default:
		g.limiter.Failed(now, 0, msg)
		return he
	}
}

func truncateBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// retryAfter reads a Retry-After header (seconds or a date).
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// geminiError is what an error answer tells.
type geminiError struct {
	Message    string
	Reason     string // e.g. API_KEY_INVALID
	retryDelay time.Duration
	daily      bool
	limits     EngineLimits // the quotas it names
}

// parseGeminiError reads a Google API error: its message, and for 429 the
// violated quotas (google.rpc.QuotaFailure, e.g. quotaId
// "GenerateRequestsPerDayPerProjectPerModel-FreeTier", quotaValue "15")
// and when to retry (google.rpc.RetryInfo).
func parseGeminiError(data []byte) geminiError {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Type       string `json:"@type"`
				Reason     string `json:"reason"`
				RetryDelay string `json:"retryDelay"`
				Violations []struct {
					QuotaID    string `json:"quotaId"`
					QuotaValue string `json:"quotaValue"`
				} `json:"violations"`
			} `json:"details"`
		} `json:"error"`
	}
	var out geminiError
	if json.Unmarshal(data, &body) != nil {
		out.daily = strings.Contains(string(data), "PerDay")
		return out
	}
	out.Message = body.Error.Message
	for _, d := range body.Error.Details {
		out.Reason = cmp.Or(out.Reason, d.Reason)
		if d.RetryDelay != "" {
			if v, err := time.ParseDuration(d.RetryDelay); err == nil && v > 0 {
				out.retryDelay = v
			}
		}
		for _, v := range d.Violations {
			n, err := strconv.Atoi(v.QuotaValue)
			daily := strings.Contains(v.QuotaID, "PerDay")
			out.daily = out.daily || daily
			if err != nil || n <= 0 {
				continue
			}
			tokens := strings.Contains(v.QuotaID, "Token")
			switch {
			case daily && tokens:
				out.limits.TPD = n
			case daily:
				out.limits.RPD = n
			case strings.Contains(v.QuotaID, "PerMinute") && tokens:
				out.limits.TPM = n
			case strings.Contains(v.QuotaID, "PerMinute"):
				out.limits.RPM = n
			}
		}
	}
	return out
}

// learn keeps the limits a 429 answer named; it reports a change.
func (g *gemini) learn(l EngineLimits) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := EngineLimits{
		RPM: cmp.Or(l.RPM, g.learned.RPM), TPM: cmp.Or(l.TPM, g.learned.TPM),
		RPD: cmp.Or(l.RPD, g.learned.RPD), TPD: cmp.Or(l.TPD, g.learned.TPD),
	}
	changed := next != g.learned
	g.learned = next
	return changed
}

// geminiPCM converts the answer's audio to SampleRate PCM: raw 16-bit PCM
// ("audio/L16;codec=pcm;rate=24000") or WAV.
func geminiPCM(mimeType string, audio []byte, gain float64) ([]byte, error) {
	mt, params, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return nil, fmt.Errorf("audio type %q: %w", mimeType, err)
	}
	switch strings.ToLower(mt) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return pcmFromWAV(audio, gain)
	case "audio/l16", "audio/pcm":
		rate := SampleRate
		if v, ok := params["rate"]; ok {
			if rate, err = strconv.Atoi(v); err != nil {
				return nil, fmt.Errorf("audio type %q: rate: %w", mimeType, err)
			}
		}
		return pcmFromL16(audio, rate, gain)
	}
	return nil, fmt.Errorf("unsupported audio type %q", mimeType)
}

// Quota states of a cloud engine.
const (
	QuotaReady     = "ready"
	QuotaWaiting   = "waiting"
	QuotaExhausted = "exhausted"
)

// Quota is how much of its quota a cloud engine used today.
type Quota struct {
	Day   string
	Today Usage
	// Limits in force: the configured ones, else those the engine named in
	// a 429 answer (Learned).
	Limits         EngineLimits
	Learned        bool
	MinuteRequests int
	MinuteTokens   int
	ResetAt        time.Time
	Timezone       string
	State          string
	Reason         string
	AvailableAt    *time.Time
	LastError      string
	LastErrorAt    *time.Time
}

// status reports the engine's quota.
func (r *remote) quotaStatus(ctx context.Context) (*Quota, error) {
	g := r.gemini
	now := time.Now()
	if err := g.loadDay(ctx, r.cfg.Name, now); err != nil {
		return nil, err
	}
	day := quota.DayOf(now, g.loc)
	var today Usage
	if g.usage != nil {
		var err error
		if today, err = g.usage.TTSUsage(ctx, r.cfg.Name, day); err != nil {
			return nil, err
		}
	}
	lim := g.limits(r.cfg.Limits)
	snap := g.limiter.Snapshot(now)
	q := &Quota{
		Day: day, Today: today,
		Limits:         EngineLimits{RPM: lim.RPM, TPM: lim.TPM, RPD: lim.RPD, TPD: lim.TPD},
		Learned:        lim != quota.Limits(r.cfg.Limits),
		MinuteRequests: snap.MinuteRequests, MinuteTokens: snap.MinuteTokens,
		ResetAt: quota.NextMidnight(now, g.loc), Timezone: g.loc.String(),
		State: QuotaReady, LastError: snap.LastError,
	}
	if !snap.LastErrorAt.IsZero() {
		t := snap.LastErrorAt
		q.LastErrorAt = &t
	}
	if a := g.limiter.Available(now, 0); a.Reason != "" {
		q.Reason, q.AvailableAt = a.Reason, &a.At
		q.State = QuotaWaiting
		if !a.At.Before(q.ResetAt) {
			q.State = QuotaExhausted
		}
	}
	return q, nil
}
