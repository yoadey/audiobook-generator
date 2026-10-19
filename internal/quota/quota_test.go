package quota

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC)

func TestLimiterRequestsPerMinute(t *testing.T) {
	l := New(Limits{RPM: 2}, time.UTC)
	l.LoadDay(DayOf(t0, time.UTC), 0, 0)
	for i := range 2 {
		_, _, ok := l.Reserve(t0.Add(time.Duration(i)*time.Second), 10)
		require.True(t, ok)
	}
	a := l.Available(t0.Add(5*time.Second), 10)
	assert.Equal(t, t0.Add(time.Minute), a.At, "the oldest request leaves the window")
	assert.Contains(t, a.Reason, "2 requests per minute")
	assert.Empty(t, l.Available(t0.Add(time.Minute+time.Millisecond), 10).Reason)
}

func TestLimiterTokensPerMinute(t *testing.T) {
	l := New(Limits{TPM: 1000}, time.UTC)
	l.LoadDay(DayOf(t0, time.UTC), 0, 0)
	r1, _, ok := l.Reserve(t0, 300)
	require.True(t, ok)
	r1(600) // actual usage was higher than estimated
	_, _, ok = l.Reserve(t0.Add(10*time.Second), 300)
	require.True(t, ok)

	a := l.Available(t0.Add(20*time.Second), 200)
	assert.Equal(t, t0.Add(time.Minute), a.At, "fits once the first 600 tokens leave the window")
	assert.False(t, a.Never)

	never := l.Available(t0, 1001)
	assert.True(t, never.Never)
	assert.Contains(t, never.Reason, "exceeds")
}

func TestLimiterDailyQuotaResetsAtMidnightInItsZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	l := New(Limits{RPD: 3}, la)
	day, need := l.NeedsDay(t0)
	require.True(t, need)
	assert.Equal(t, "2026-10-15", day, "22:00 UTC is still the 15th in Los Angeles")
	l.LoadDay(day, 2, 0) // two requests persisted before a restart

	_, _, ok := l.Reserve(t0, 10)
	require.True(t, ok)
	a := l.Available(t0.Add(time.Second), 10)
	assert.Contains(t, a.Reason, "daily limit of 3 requests")
	// Midnight in Los Angeles is 07:00 UTC (PDT).
	assert.Equal(t, time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC), a.At.UTC())

	next := time.Date(2026, 10, 16, 7, 0, 1, 0, time.UTC)
	day, need = l.NeedsDay(next)
	assert.True(t, need)
	assert.Equal(t, "2026-10-16", day)
}

func TestLimiterTokensPerDay(t *testing.T) {
	l := New(Limits{TPD: 1000}, time.UTC)
	l.LoadDay(DayOf(t0, time.UTC), 1, 900)
	assert.Empty(t, l.Available(t0, 100).Reason)
	assert.Contains(t, l.Available(t0, 101).Reason, "daily limit of 1000 tokens")
}

func TestLimiterRateLimitBackoff(t *testing.T) {
	l := New(Limits{}, time.UTC)
	l.RateLimited(t0, 0, false, "429")
	assert.Equal(t, t0.Add(15*time.Second), l.Available(t0, 0).At)
	l.RateLimited(t0, 0, false, "429")
	assert.Equal(t, t0.Add(30*time.Second), l.Available(t0, 0).At)
	l.RateLimited(t0, 7*time.Second, false, "429")
	assert.Equal(t, t0.Add(7*time.Second), l.Available(t0, 0).At, "the server's hint wins")

	l.RateLimited(t0, 0, true, "quota PerDay")
	a := l.Available(t0, 0)
	assert.Equal(t, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), a.At)
	assert.Contains(t, a.Reason, "daily quota")

	l.Succeeded()
	assert.Empty(t, l.Snapshot(t0).LastError)
}
