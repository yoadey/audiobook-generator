package llm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, 20*time.Second, retryAfter("20", nil, now))
	assert.Equal(t, 90*time.Second, retryAfter("Thu, 15 Oct 2026 12:01:30 GMT", nil, now))
	body := []byte(`{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"37.5s"}]}}`)
	assert.Equal(t, 37500*time.Millisecond, retryAfter("", body, now))
	assert.Zero(t, retryAfter("", []byte("{}"), now))
}

func TestExtractJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:                 `{"a":1}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"<think>hmm {not this}</think>\n{\"a\":1}":  `{"a":1}`,
		"Sure! Here is the JSON:\n{\"a\":1}\nDone.": `{"a":1}`,
		`[1,2]`:                       `[1,2]`,
		"{\"a\":1}\nHope that helps!": `{"a":1}`,
	} {
		assert.JSONEq(t, want, string(extractJSON(in)), in)
	}
}

func TestValidate(t *testing.T) {
	s := &Schema{Name: "t", JSON: json.RawMessage(`{"type":"object","properties":{"sum":{"type":"integer"}},"required":["sum"]}`)}
	var out struct{ Sum int }
	require.NoError(t, validate(s, []byte(`{"sum": 5}`), &out))
	assert.Equal(t, 5, out.Sum)

	err := validate(s, []byte(`{"sum": "five"}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at /sum")

	err = validate(s, []byte(`{}`), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sum")

	assert.ErrorContains(t, validate(s, []byte(`not json`), nil), "not valid JSON")
}

func TestPrompts(t *testing.T) {
	p, err := LoadPrompt("provider_test")
	require.NoError(t, err)
	assert.Equal(t, "provider_test@v1", p.ID())
	req, err := p.Render(map[string]any{"A": 2, "B": 3, "Word": "ember"})
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "system", req.Messages[0].Role)
	assert.Contains(t, req.Messages[1].Content, "Add 2 and 3")
	assert.Equal(t, "provider_test@v1", req.Prompt)
	require.NotNil(t, req.Schema)

	_, err = p.Render(map[string]any{"A": 2})
	assert.Error(t, err, "missing template keys are errors")

	_, err = LoadPrompt("nope")
	assert.Error(t, err)
}
