package llm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC)

func TestLimiterRequestsPerMinute(t *testing.T) {
	l := newLimiter(Limits{RPM: 2}, time.UTC)
	l.loadDay(dayOf(t0, time.UTC), 0, 0)
	for i := range 2 {
		_, _, ok := l.reserve(t0.Add(time.Duration(i)*time.Second), 10)
		require.True(t, ok)
	}
	a := l.available(t0.Add(5*time.Second), 10)
	assert.Equal(t, t0.Add(time.Minute), a.At, "the oldest request leaves the window")
	assert.Contains(t, a.Reason, "2 requests per minute")
	assert.Empty(t, l.available(t0.Add(time.Minute+time.Millisecond), 10).Reason)
}

func TestLimiterTokensPerMinute(t *testing.T) {
	l := newLimiter(Limits{TPM: 1000}, time.UTC)
	l.loadDay(dayOf(t0, time.UTC), 0, 0)
	r1, _, ok := l.reserve(t0, 300)
	require.True(t, ok)
	r1(600) // actual usage was higher than estimated
	_, _, ok = l.reserve(t0.Add(10*time.Second), 300)
	require.True(t, ok)

	a := l.available(t0.Add(20*time.Second), 200)
	assert.Equal(t, t0.Add(time.Minute), a.At, "fits once the first 600 tokens leave the window")
	assert.False(t, a.Never)

	never := l.available(t0, 1001)
	assert.True(t, never.Never)
	assert.Contains(t, never.Reason, "exceeds")
}

func TestLimiterDailyQuotaResetsAtMidnightInItsZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	l := newLimiter(Limits{RPD: 3}, la)
	day, need := l.needsDay(t0)
	require.True(t, need)
	assert.Equal(t, "2026-10-15", day, "22:00 UTC is still the 15th in Los Angeles")
	l.loadDay(day, 2, 0) // two requests persisted before a restart

	_, _, ok := l.reserve(t0, 10)
	require.True(t, ok)
	a := l.available(t0.Add(time.Second), 10)
	assert.Contains(t, a.Reason, "daily limit of 3 requests")
	// Midnight in Los Angeles is 07:00 UTC (PDT).
	assert.Equal(t, time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC), a.At.UTC())

	next := time.Date(2026, 10, 16, 7, 0, 1, 0, time.UTC)
	day, need = l.needsDay(next)
	assert.True(t, need)
	assert.Equal(t, "2026-10-16", day)
}

func TestLimiterTokensPerDay(t *testing.T) {
	l := newLimiter(Limits{TPD: 1000}, time.UTC)
	l.loadDay(dayOf(t0, time.UTC), 1, 900)
	assert.Empty(t, l.available(t0, 100).Reason)
	assert.Contains(t, l.available(t0, 101).Reason, "daily limit of 1000 tokens")
}

func TestLimiterRateLimitBackoff(t *testing.T) {
	l := newLimiter(Limits{}, time.UTC)
	l.rateLimited(t0, 0, false, "429")
	assert.Equal(t, t0.Add(15*time.Second), l.available(t0, 0).At)
	l.rateLimited(t0, 0, false, "429")
	assert.Equal(t, t0.Add(30*time.Second), l.available(t0, 0).At)
	l.rateLimited(t0, 7*time.Second, false, "429")
	assert.Equal(t, t0.Add(7*time.Second), l.available(t0, 0).At, "the server's hint wins")

	l.rateLimited(t0, 0, true, "quota PerDay")
	a := l.available(t0, 0)
	assert.Equal(t, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), a.At)
	assert.Contains(t, a.Reason, "daily quota")

	l.succeeded()
	assert.Empty(t, l.snapshot(t0).lastError)
}

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
