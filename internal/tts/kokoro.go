// Package tts talks to the Kokoro-FastAPI speech server (OpenAI-compatible).
package tts

import (
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
