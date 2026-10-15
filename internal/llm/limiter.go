package llm

import (
	"fmt"
	"sync"
	"time"
)

// Limits of a provider; 0 means unlimited. Per-minute limits use a sliding
// 60 s window, daily limits reset at midnight in the provider's quota
// time zone.
type Limits struct {
	RPM, TPM, RPD, TPD int
}

// Availability is when a provider can take the next request, and why not
// earlier.
type Availability struct {
	At     time.Time
	Reason string
	// Never is set when the request can never fit the limits.
	Never bool
}

type event struct {
	id     uint64
	at     time.Time
	tokens int
}

// limiter tracks one provider. Daily counters are persisted by the pool
// (store.LLMUsage) and loaded into the limiter on the first request of a
// day, so restarts keep the daily quota honest; per-minute windows and
// cooldowns live in memory only.
type limiter struct {
	mu     sync.Mutex
	limits Limits
	loc    *time.Location

	window []event // requests of the last minute
	seq    uint64

	day         string // YYYY-MM-DD in loc the counters below belong to
	dayLoaded   bool
	dayRequests int64
	dayTokens   int64

	cooldownUntil time.Time
	cooldownWhy   string
	strikes       int // consecutive rate-limit answers

	lastError   string
	lastErrorAt time.Time
}

func newLimiter(l Limits, loc *time.Location) *limiter {
	return &limiter{limits: l, loc: loc}
}

// configure updates the limits (settings may change while running).
func (l *limiter) configure(lim Limits, loc *time.Location) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loc.String() != loc.String() {
		l.dayLoaded = false
	}
	l.limits, l.loc = lim, loc
}

func dayOf(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.DateOnly) }

// nextMidnight is the start of the day after t in loc.
func nextMidnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// needsDay reports whether the daily counters for now must be (re)loaded.
func (l *limiter) needsDay(now time.Time) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := dayOf(now, l.loc)
	return d, !l.dayLoaded || l.day != d
}

// loadDay sets the persisted counters of a day.
func (l *limiter) loadDay(day string, requests, tokens int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.day, l.dayLoaded, l.dayRequests, l.dayTokens = day, true, requests, tokens
}

func (l *limiter) prune(now time.Time) {
	cut := now.Add(-time.Minute)
	i := 0
	for i < len(l.window) && !l.window[i].at.After(cut) {
		i++
	}
	l.window = l.window[i:]
}

// available returns when a request of about est tokens may be sent.
// Reason is empty when that is now.
func (l *limiter) available(now time.Time, est int) Availability {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.availableLocked(now, est)
}

func (l *limiter) availableLocked(now time.Time, est int) Availability {
	l.prune(now)
	lim := l.limits
	if now.Before(l.cooldownUntil) {
		return Availability{At: l.cooldownUntil, Reason: l.cooldownWhy}
	}
	if l.dayLoaded && l.day == dayOf(now, l.loc) {
		if lim.RPD > 0 && l.dayRequests >= int64(lim.RPD) {
			return Availability{At: nextMidnight(now, l.loc), Reason: fmt.Sprintf("daily limit of %d requests reached", lim.RPD)}
		}
		if lim.TPD > 0 && l.dayTokens+int64(est) > int64(lim.TPD) {
			return Availability{At: nextMidnight(now, l.loc), Reason: fmt.Sprintf("daily limit of %d tokens reached", lim.TPD)}
		}
	}
	if lim.RPM > 0 && len(l.window) >= lim.RPM {
		at := l.window[len(l.window)-lim.RPM].at.Add(time.Minute)
		return Availability{At: at, Reason: fmt.Sprintf("%d requests per minute", lim.RPM)}
	}
	if lim.TPM > 0 {
		if est > lim.TPM {
			return Availability{At: now, Never: true, Reason: fmt.Sprintf("request (~%d tokens) exceeds the limit of %d tokens per minute", est, lim.TPM)}
		}
		used := 0
		for _, e := range l.window {
			used += e.tokens
		}
		// Drop the oldest events until the request fits.
		for i := 0; used+est > lim.TPM && i < len(l.window); i++ {
			used -= l.window[i].tokens
			if used+est <= lim.TPM {
				return Availability{At: l.window[i].at.Add(time.Minute), Reason: fmt.Sprintf("%d tokens per minute", lim.TPM)}
			}
		}
	}
	return Availability{At: now}
}

// reserve takes a slot for a request if one is free right now; it returns
// a release function that records the actual token count.
func (l *limiter) reserve(now time.Time, est int) (func(tokens int), Availability, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.availableLocked(now, est); a.Reason != "" {
		return nil, a, false
	}
	l.seq++
	id := l.seq
	l.window = append(l.window, event{id: id, at: now, tokens: est})
	if l.dayLoaded && l.day == dayOf(now, l.loc) {
		l.dayRequests++
	}
	return func(tokens int) {
		l.mu.Lock()
		defer l.mu.Unlock()
		// The window may have been pruned and reallocated meanwhile.
		for i := range l.window {
			if l.window[i].id == id {
				l.window[i].tokens = tokens
				break
			}
		}
		if l.dayLoaded && l.day == dayOf(now, l.loc) {
			l.dayTokens += int64(tokens)
		}
	}, Availability{At: now}, true
}

// rateLimited records a 429: wait for the server's hint, or back off
// exponentially (15 s, 30 s, … up to 15 min); a daily quota waits for the
// next reset.
func (l *limiter) rateLimited(now time.Time, hint time.Duration, daily bool, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.strikes++
	wait := hint
	switch {
	case daily:
		l.cooldownUntil = nextMidnight(now, l.loc)
		l.cooldownWhy = "daily quota exhausted (provider answered 429)"
		l.recordErrorLocked(now, msg)
		return
	case wait <= 0:
		wait = min(15*time.Second<<min(l.strikes-1, 6), 15*time.Minute)
	}
	l.cooldownUntil = now.Add(wait)
	l.cooldownWhy = "rate-limited by the provider"
	l.recordErrorLocked(now, msg)
}

// failed records another error; cooldown keeps a broken provider (wrong
// key, unreachable) from being hit on every request.
func (l *limiter) failed(now time.Time, cooldown time.Duration, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cooldown > 0 {
		l.cooldownUntil = now.Add(cooldown)
		l.cooldownWhy = "failing: " + truncate(msg, 200)
	}
	l.recordErrorLocked(now, msg)
}

func (l *limiter) succeeded() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.strikes = 0
	l.lastError = ""
}

func (l *limiter) recordErrorLocked(now time.Time, msg string) {
	l.lastError = truncate(msg, 500)
	l.lastErrorAt = now
}

// snapshot is the limiter state for status displays.
type snapshot struct {
	minuteRequests int
	minuteTokens   int
	cooldownUntil  time.Time
	cooldownWhy    string
	lastError      string
	lastErrorAt    time.Time
}

func (l *limiter) snapshot(now time.Time) snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	s := snapshot{
		minuteRequests: len(l.window),
		lastError:      l.lastError,
		lastErrorAt:    l.lastErrorAt,
	}
	for _, e := range l.window {
		s.minuteTokens += e.tokens
	}
	if now.Before(l.cooldownUntil) {
		s.cooldownUntil, s.cooldownWhy = l.cooldownUntil, l.cooldownWhy
	}
	return s
}

// truncate shortens s to at most n runes (never splitting one).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
