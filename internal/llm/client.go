// Package llm is the LLM layer: a small client for OpenAI-compatible chat
// completion endpoints, per-provider rate limits and daily quotas, fallback
// across an ordered list of providers, structured (JSON schema) output with
// validation and one repair round, and versioned prompt templates.
//
// The LLM never sees a request to rewrite book text: callers send
// segmented text with stable IDs and get compact labels back.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Schema is the JSON schema a structured response must match.
type Schema struct {
	Name string
	JSON json.RawMessage
}

// Request is one chat completion.
type Request struct {
	// Prompt identifies the template (name@version) for metrics and logs.
	Prompt   string
	Messages []Message
	Schema   *Schema
	// MaxTokens bounds the answer (capped by the provider's
	// MaxOutputTokens); 0 leaves it to the provider setting or server.
	MaxTokens   int
	Temperature *float64
}

// Usage is what a request consumed.
type Usage struct {
	InputTokens  int `json:"prompt_tokens"`
	OutputTokens int `json:"completion_tokens"`
}

// Completion is the answer of a provider.
type Completion struct {
	Content      string
	Model        string
	FinishReason string
	Usage        Usage
}

// Endpoint is one OpenAI-compatible server with its settings.
type Endpoint struct {
	BaseURL          string
	APIKey           string
	Model            string
	StructuredOutput string
	ExtraParams      map[string]any
	MaxOutputTokens  int
	HTTP             *http.Client
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status int
	Body   string
	// RetryAfter is the server's hint (Retry-After header or a retryDelay
	// in the body); 0 when there was none.
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

// RateLimited reports a 429 answer.
func (e *HTTPError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

// DailyQuota guesses whether a 429 is about a daily quota (Gemini names its
// quotas "...PerDay...") rather than a per-minute limit.
func (e *HTTPError) DailyQuota() bool {
	return e.RateLimited() && (strings.Contains(e.Body, "PerDay") || strings.Contains(e.Body, "per day"))
}

// Temporary reports errors where the same request may succeed later.
func (e *HTTPError) Temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout || e.Status >= 500
}

func (e Endpoint) url(path string) string {
	return strings.TrimRight(e.BaseURL, "/") + "/" + path
}

func (e Endpoint) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return http.DefaultClient
}

// maxResponseBytes bounds a completion response.
const maxResponseBytes = 8 << 20

// Complete sends one chat completion request.
func (e Endpoint) Complete(ctx context.Context, req Request) (Completion, error) {
	msgs := req.Messages
	body := map[string]any{"model": e.Model}
	maps.Copy(body, e.ExtraParams)
	if req.Schema != nil {
		switch e.StructuredOutput {
		case store.StructuredJSONObject:
			body["response_format"] = map[string]any{"type": "json_object"}
			msgs = withSchemaInstructions(msgs, req.Schema)
		case store.StructuredPrompt:
			msgs = withSchemaInstructions(msgs, req.Schema)
		default:
			body["response_format"] = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   req.Schema.Name,
					"schema": req.Schema.JSON,
				},
			}
		}
	}
	body["messages"] = msgs
	// The provider's setting caps what a request asks for.
	n := req.MaxTokens
	if e.MaxOutputTokens > 0 && (n == 0 || n > e.MaxOutputTokens) {
		n = e.MaxOutputTokens
	}
	if n > 0 {
		body["max_tokens"] = n
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Completion{}, err
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := e.do(ctx, http.MethodPost, "chat/completions", payload, &out); err != nil {
		return Completion{}, err
	}
	if len(out.Choices) == 0 {
		return Completion{}, errors.New("response has no choices")
	}
	c := Completion{
		Content:      out.Choices[0].Message.Content,
		Model:        out.Model,
		FinishReason: out.Choices[0].FinishReason,
		Usage:        out.Usage,
	}
	if c.Model == "" {
		c.Model = e.Model
	}
	return c, nil
}

// Models lists the model IDs the server offers (GET /models).
func (e Endpoint) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := e.do(ctx, http.MethodGet, "models", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		// Gemini prefixes its IDs with "models/", but expects them without.
		ids = append(ids, strings.TrimPrefix(m.ID, "models/"))
	}
	return ids, nil
}

func (e Endpoint) do(ctx context.Context, method, path string, payload []byte, out any) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.url(path), body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &HTTPError{
			Status:     resp.StatusCode,
			Body:       strings.TrimSpace(string(msg)),
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), msg, time.Now()),
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("response larger than %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

var retryDelayRe = regexp.MustCompile(`"retryDelay"\s*:\s*"(\d+(?:\.\d+)?)s"`)

// retryAfter reads a Retry-After header (seconds or HTTP date) or, failing
// that, a Google-style "retryDelay": "37s" in the error body.
func retryAfter(header string, body []byte, now time.Time) time.Duration {
	if header != "" {
		if s, err := strconv.ParseFloat(header, 64); err == nil && s >= 0 {
			return time.Duration(s * float64(time.Second))
		}
		if t, err := http.ParseTime(header); err == nil {
			return max(t.Sub(now), 0)
		}
	}
	if m := retryDelayRe.FindSubmatch(body); m != nil {
		if s, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			return time.Duration(s * float64(time.Second))
		}
	}
	return 0
}

// withSchemaInstructions appends the schema to the system message (or adds
// one) for providers without native structured output.
func withSchemaInstructions(msgs []Message, s *Schema) []Message {
	note := "Reply with a single JSON object only, without Markdown fences or any other text. " +
		"It must match this JSON schema:\n" + string(s.JSON)
	out := make([]Message, 0, len(msgs)+1)
	added := false
	for _, m := range msgs {
		if m.Role == "system" && !added {
			m.Content = strings.TrimRight(m.Content, "\n") + "\n\n" + note
			added = true
		}
		out = append(out, m)
	}
	if !added {
		out = append([]Message{{Role: "system", Content: note}}, out...)
	}
	return out
}
