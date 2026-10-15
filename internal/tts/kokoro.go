// Package tts talks to the Kokoro-FastAPI speech server (OpenAI-compatible).
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a Kokoro-FastAPI client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for the server at baseURL (e.g. http://kokoro:8880).
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// BaseURL returns the configured server URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Voice is a Kokoro base voice. Grades come from the upstream model card and
// are empty for ungraded voices.
type Voice struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OverallGrade   string `json:"overall_grade,omitempty"`
	TargetQuality  string `json:"target_quality,omitempty"`
	TrainingAmount string `json:"training_duration,omitempty"`
}

// Voices lists the base voices the server offers.
func (c *Client) Voices(ctx context.Context) ([]Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/audio/voices", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("list voices: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		Voices []Voice `json:"voices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode voices: %w", err)
	}
	return out.Voices, nil
}

// SampleRate of Kokoro's PCM output (signed 16-bit little-endian, mono).
const SampleRate = 24000

// Speech is one synthesis request.
type Speech struct {
	Text  string
	Voice string // a voice or a weighted mix, e.g. "af_bella(2)+af_sky(1)"
	Speed float64
	// Format is "pcm" (raw samples, for assembly) or "mp3" (previews).
	Format string
}

// MaxResponseBytes bounds a single synthesis response (~4 min of PCM).
const MaxResponseBytes = 16 << 20

// Synthesize renders text to audio.
func (c *Client) Synthesize(ctx context.Context, s Speech) ([]byte, error) {
	if s.Format == "" {
		s.Format = "pcm"
	}
	if s.Speed == 0 {
		s.Speed = 1
	}
	body, err := json.Marshal(map[string]any{
		"model":           "kokoro",
		"input":           s.Text,
		"voice":           s.Voice,
		"speed":           s.Speed,
		"response_format": s.Format,
		"stream":          false,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxResponseBytes {
		return nil, fmt.Errorf("synthesis response larger than %d bytes", MaxResponseBytes)
	}
	return data, nil
}

// HTTPError is a non-200 answer of the TTS server.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("tts: %d %s: %s", e.Status, http.StatusText(e.Status), e.Body)
}

// Permanent reports errors that retrying will not fix (bad voice, bad input).
func (e *HTTPError) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests
}
