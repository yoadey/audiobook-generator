package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Pool sends requests to the configured providers: in order, each within
// its limits, falling back to the next one on rate limits and errors.
type Pool struct {
	Store *store.Store
	// HTTP is shared by all providers; per-request timeouts come from the
	// provider settings.
	HTTP *http.Client
	// Getenv resolves APIKeyEnv of configuration providers.
	Getenv func(string) string
	// MaxWait is how long a request may wait for a provider's per-minute
	// window before falling back to the next provider.
	MaxWait time.Duration
	Metrics *Metrics
	Log     *slog.Logger
	Now     func() time.Time
	Sleep   func(ctx context.Context, d time.Duration) error

	mu       sync.Mutex
	limiters map[int64]*limiter
}

// NewPool returns a pool with production defaults.
func NewPool(st *store.Store, metrics *Metrics, log *slog.Logger) *Pool {
	return &Pool{
		Store:   st,
		HTTP:    &http.Client{},
		Getenv:  os.Getenv,
		MaxWait: 70 * time.Second,
		Metrics: metrics,
		Log:     log,
		Now:     time.Now,
		Sleep:   sleepCtx,
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

// NoProvidersError means no enabled provider is configured. Jobs wait
// (see jobs.Deferrer) instead of failing, so configuring a provider later
// resumes them.
type NoProvidersError struct{ at time.Time }

func (NoProvidersError) Error() string { return "no LLM provider is configured and enabled" }

// DeferUntil implements jobs.Deferrer.
func (e NoProvidersError) DeferUntil() time.Time { return e.at.Add(15 * time.Minute) }

// ExhaustedError means every provider is out of quota or cooling down.
type ExhaustedError struct {
	RetryAt time.Time
	Reasons []string
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("all LLM providers are busy until %s (%s)",
		e.RetryAt.UTC().Format(time.RFC3339), strings.Join(e.Reasons, "; "))
}

// DeferUntil implements jobs.Deferrer.
func (e *ExhaustedError) DeferUntil() time.Time { return e.RetryAt }

// RejectedError means every provider refused this request (HTTP 400-class
// answers), e.g. because it exceeds the model's context.
type RejectedError struct{ Err error }

func (e *RejectedError) Error() string {
	return "every LLM provider refused the request: " + e.Err.Error()
}
func (e *RejectedError) Unwrap() error { return e.Err }

// TooLong reports whether the refusal looks like a size problem (a
// smaller request may be accepted).
func (e *RejectedError) TooLong() bool {
	var he *HTTPError
	if !errors.As(e.Err, &he) {
		return false
	}
	if he.Status == http.StatusRequestEntityTooLarge {
		return true
	}
	body := strings.ToLower(he.Body)
	for _, hint := range []string{"context", "too long", "too large", "maximum", "token", "length"} {
		if strings.Contains(body, hint) {
			return true
		}
	}
	return false
}

// TooLargeError means the request exceeds the limits of every provider;
// waiting cannot help, a smaller request might.
type TooLargeError struct{ Reasons []string }

func (e *TooLargeError) Error() string {
	return "no LLM provider can take this request: " + strings.Join(e.Reasons, "; ")
}

// Result is a completion and where it came from.
type Result struct {
	Completion
	ProviderID int64
	Provider   string
	Latency    time.Duration
}

// estimateTokens guesses the input tokens of a request (~4 characters per
// token for English); the actual usage replaces it once known.
func estimateTokens(req Request) int {
	n := 0
	for _, m := range req.Messages {
		n += len(m.Content)/4 + 4
	}
	if req.Schema != nil {
		n += len(req.Schema.JSON) / 4
	}
	return n
}

func (p *Pool) location(prov *store.LLMProvider) *time.Location {
	loc, err := time.LoadLocation(prov.QuotaTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// limiter returns the provider's limiter, configured with its current
// limits and loaded with today's persisted counters.
func (p *Pool) limiter(ctx context.Context, prov *store.LLMProvider, now time.Time) (*limiter, error) {
	lim := Limits{RPM: prov.RPM, TPM: prov.TPM, RPD: prov.RPD, TPD: prov.TPD}
	loc := p.location(prov)
	p.mu.Lock()
	if p.limiters == nil {
		p.limiters = map[int64]*limiter{}
	}
	l, ok := p.limiters[prov.ID]
	if !ok {
		l = newLimiter(lim, loc)
		p.limiters[prov.ID] = l
	}
	p.mu.Unlock()
	if ok {
		l.configure(lim, loc)
	}
	if day, need := l.needsDay(now); need {
		u, err := p.Store.GetLLMUsage(ctx, prov.ID, day)
		if err != nil {
			return nil, err
		}
		l.loadDay(day, u.Requests, u.InputTokens+u.OutputTokens)
	}
	return l, nil
}

// Forget drops the in-memory state of a provider (after deleting it).
func (p *Pool) Forget(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.limiters, id)
}

func (p *Pool) apiKey(prov *store.LLMProvider) (string, bool) {
	if prov.APIKeyEnv != "" {
		k := p.Getenv(prov.APIKeyEnv)
		return k, k != ""
	}
	return prov.APIKey, true
}

func (p *Pool) endpoint(prov *store.LLMProvider) (Endpoint, error) {
	extra, err := extraParams(prov.ExtraParams)
	if err != nil {
		return Endpoint{}, err
	}
	key, _ := p.apiKey(prov)
	return Endpoint{
		BaseURL:          prov.BaseURL,
		APIKey:           key,
		Model:            prov.Model,
		StructuredOutput: prov.StructuredOutput,
		ExtraParams:      extra,
		MaxOutputTokens:  prov.MaxOutputTokens,
		HTTP:             p.HTTP,
	}, nil
}

// errorKind decides what a failed request means for the provider.
type errorKind int

const (
	errTransient   errorKind = iota // network, 5xx: short cooldown
	errRateLimited                  // 429
	errConfig                       // 401/403/404: provider misconfigured
	errRequest                      // 400/413/422: this request does not fit this provider
)

func classify(err error) errorKind {
	var he *HTTPError
	if !errors.As(err, &he) {
		return errTransient
	}
	switch {
	case he.RateLimited():
		return errRateLimited
	case he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden || he.Status == http.StatusNotFound:
		return errConfig
	case he.Temporary():
		return errTransient
	default:
		return errRequest
	}
}

// send performs one request on a provider whose limiter slot is reserved,
// and does all the bookkeeping: usage counters, limiter state, metrics.
func (p *Pool) send(ctx context.Context, prov *store.LLMProvider, l *limiter, release func(int), req Request) (*Result, error) {
	ep, err := p.endpoint(prov)
	if err != nil {
		release(0)
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(prov.TimeoutSeconds)*time.Second)
	defer cancel()
	start := p.Now()
	c, err := ep.Complete(callCtx, req)
	took := p.Now().Sub(start)
	day := dayOf(start, p.location(prov))

	// Bookkeeping must happen even when ctx is canceled meanwhile.
	bg, bgCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer bgCancel()
	usage := store.LLMUsage{ProviderID: prov.ID, Day: day, Requests: 1}

	if err == nil {
		release(c.Usage.InputTokens + c.Usage.OutputTokens)
		l.succeeded()
		usage.InputTokens, usage.OutputTokens = int64(c.Usage.InputTokens), int64(c.Usage.OutputTokens)
		if uerr := p.Store.AddLLMUsage(bg, usage); uerr != nil {
			p.Log.Warn("record LLM usage", "provider", prov.Name, "err", uerr)
		}
		p.Metrics.success(prov.Name, c.Usage, took)
		p.Log.Debug("llm request", "provider", prov.Name, "prompt", req.Prompt, "took", took,
			"input_tokens", c.Usage.InputTokens, "output_tokens", c.Usage.OutputTokens)
		return &Result{Completion: c, ProviderID: prov.ID, Provider: prov.Name, Latency: took}, nil
	}

	release(0)
	usage.Errors = 1
	now := p.Now()
	if ctx.Err() == nil {
		switch classify(err) {
		case errRateLimited:
			var he *HTTPError
			errors.As(err, &he)
			usage.RateLimited = 1
			l.rateLimited(now, he.RetryAfter, he.DailyQuota(), err.Error())
			p.Metrics.request(prov.Name, outcomeRateLimited)
		case errConfig:
			l.failed(now, 5*time.Minute, err.Error())
			p.Metrics.request(prov.Name, outcomeError)
		case errRequest:
			l.failed(now, 0, err.Error())
			p.Metrics.request(prov.Name, outcomeError)
		default:
			l.failed(now, 30*time.Second, err.Error())
			p.Metrics.request(prov.Name, outcomeError)
		}
	}
	if uerr := p.Store.AddLLMUsage(bg, usage); uerr != nil {
		p.Log.Warn("record LLM usage", "provider", prov.Name, "err", uerr)
	}
	p.Log.Warn("llm request failed", "provider", prov.Name, "prompt", req.Prompt, "err", err)
	return nil, fmt.Errorf("%s: %w", prov.Name, err)
}

func (p *Pool) enabledProviders(ctx context.Context) ([]store.LLMProvider, error) {
	all, err := p.Store.ListLLMProviders(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(pr store.LLMProvider) bool { return !pr.Enabled }), nil
}

// Complete sends a request to the first provider that can take it.
//
// A provider whose per-minute window frees up within MaxWait is waited
// for; otherwise the next provider is tried. When none can take the
// request, an *ExhaustedError says when to retry (jobs defer until then).
func (p *Pool) Complete(ctx context.Context, req Request) (*Result, error) {
	provs, err := p.enabledProviders(ctx)
	if err != nil {
		return nil, err
	}
	if len(provs) == 0 {
		return nil, NoProvidersError{at: p.Now()}
	}
	est := estimateTokens(req)
	rejected := map[int64]bool{} // providers that refused this very request
	failed := map[int64]bool{}   // providers that failed during this call
	var lastErr error
	// Sends are bounded; waiting for a per-minute window is not a send.
	for sends := 0; sends < 2*len(provs)+2; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := p.Now()
		var (
			pick       *store.LLMProvider
			pickLim    *limiter
			pickAt     time.Time
			reasons    []string
			retryAt    time.Time
			keyMissing bool
		)
		for _, prov := range fallbackOrder(provs, failed) {
			if rejected[prov.ID] {
				continue
			}
			if _, ok := p.apiKey(prov); !ok {
				keyMissing = true
				reasons = append(reasons, fmt.Sprintf("%s: API key variable %s is empty", prov.Name, prov.APIKeyEnv))
				continue
			}
			l, err := p.limiter(ctx, prov, now)
			if err != nil {
				return nil, err
			}
			a := l.available(now, est)
			if !a.Never && (a.Reason == "" || a.At.Sub(now) <= p.MaxWait) {
				pick, pickLim, pickAt = prov, l, a.At
				break
			}
			reasons = append(reasons, fmt.Sprintf("%s: %s", prov.Name, a.Reason))
			if a.Never {
				continue
			}
			if retryAt.IsZero() || a.At.Before(retryAt) {
				retryAt = a.At
			}
		}
		if pick == nil {
			switch {
			case !retryAt.IsZero():
				return nil, &ExhaustedError{RetryAt: retryAt, Reasons: reasons}
			case keyMissing:
				// Wait for the configuration to change, like without
				// providers.
				return nil, &ExhaustedError{RetryAt: now.Add(15 * time.Minute), Reasons: reasons}
			case lastErr != nil:
				// Every remaining provider refused this request.
				return nil, &RejectedError{Err: lastErr}
			default:
				// The request exceeds every provider's limits: waiting
				// cannot help.
				return nil, &TooLargeError{Reasons: reasons}
			}
		}
		if wait := pickAt.Sub(now); wait > 0 {
			if err := p.Sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue // re-evaluate: another request may have taken the slot
		}
		release, _, ok := pickLim.reserve(now, est)
		if !ok {
			continue
		}
		sends++
		res, err := p.send(ctx, pick, pickLim, release, req)
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		failed[pick.ID] = true
		if classify(err) == errRequest {
			rejected[pick.ID] = true
		}
	}
	return nil, fmt.Errorf("LLM request failed on every provider: %w", lastErr)
}

// fallbackOrder lists the providers in their order, but those that already
// failed during this call last: after an error the next provider is tried
// before the failed one is waited for (its cooldown may be shorter than
// MaxWait).
func fallbackOrder(provs []store.LLMProvider, failed map[int64]bool) []*store.LLMProvider {
	out := make([]*store.LLMProvider, 0, len(provs))
	for _, wantFailed := range []bool{false, true} {
		for i := range provs {
			if failed[provs[i].ID] == wantFailed {
				out = append(out, &provs[i])
			}
		}
	}
	return out
}

// CompleteJSON is Complete for prompts with a schema: the answer is
// validated against it and decoded into out. An invalid answer gets one
// repair round; if that fails too, an *InvalidOutputError is returned.
func (p *Pool) CompleteJSON(ctx context.Context, req Request, out any) (*Result, error) {
	if req.Schema == nil {
		return nil, errors.New("CompleteJSON needs a schema")
	}
	res, err := p.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	verr := validate(req.Schema, extractJSON(res.Content), out)
	if verr == nil {
		return res, nil
	}
	p.Metrics.request(res.Provider, outcomeInvalid)
	if res.FinishReason == "length" {
		return nil, &InvalidOutputError{Err: fmt.Errorf("answer truncated at the output token limit: %w", verr),
			Content: res.Content, Truncated: true}
	}
	p.Log.Info("llm answer invalid, asking for a repair", "provider", res.Provider, "prompt", req.Prompt, "err", verr)
	repair := req
	repair.Messages = append(slices.Clone(req.Messages),
		Message{Role: "assistant", Content: res.Content},
		Message{Role: "user", Content: "Your answer does not match the required JSON schema: " + verr.Error() +
			"\nReply again with only the corrected JSON object."},
	)
	res2, err := p.Complete(ctx, repair)
	if err != nil {
		return nil, err
	}
	if verr := validate(req.Schema, extractJSON(res2.Content), out); verr != nil {
		p.Metrics.request(res2.Provider, outcomeInvalid)
		return nil, &InvalidOutputError{Err: verr, Content: res2.Content, Truncated: res2.FinishReason == "length"}
	}
	return res2, nil
}

// Models lists the models of a provider definition (which need not be
// saved yet).
func (p *Pool) Models(ctx context.Context, prov *store.LLMProvider) ([]string, error) {
	ep, err := p.endpoint(prov)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ids, err := ep.Models(ctx)
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	return ids, nil
}

// TestResult is the outcome of a provider test.
type TestResult struct {
	OK      bool
	Message string
	Model   string
	Latency time.Duration
	Usage   Usage
}

var testPrompt = MustLoadPrompt("provider_test")

var testWords = []string{"lantern", "harbour", "meadow", "copper", "falcon", "thistle", "ember", "quill"}

// Test sends a small structured-output request to one provider (ignoring
// the order, but respecting its limits) and checks the answer.
func (p *Pool) Test(ctx context.Context, prov *store.LLMProvider) (*TestResult, error) {
	if _, ok := p.apiKey(prov); !ok {
		return &TestResult{Message: fmt.Sprintf("API key variable %s is empty", prov.APIKeyEnv)}, nil
	}
	a, b := rand.IntN(900)+100, rand.IntN(900)+100 //nolint:gosec // not security relevant
	word := testWords[rand.IntN(len(testWords))]   //nolint:gosec // not security relevant
	req, err := testPrompt.Render(map[string]any{"A": a, "B": b, "Word": word})
	if err != nil {
		return nil, err
	}
	now := p.Now()
	l, err := p.limiter(ctx, prov, now)
	if err != nil {
		return nil, err
	}
	release, avail, ok := l.reserve(now, estimateTokens(req))
	if !ok {
		return &TestResult{Message: fmt.Sprintf("not available until %s: %s",
			avail.At.UTC().Format(time.RFC3339), avail.Reason)}, nil
	}
	res, err := p.send(ctx, prov, l, release, req)
	if err != nil {
		return &TestResult{Message: err.Error()}, nil
	}
	tr := &TestResult{Model: res.Model, Latency: res.Latency, Usage: res.Usage}
	var answer struct {
		Sum  int    `json:"sum"`
		Echo string `json:"echo"`
	}
	if verr := validate(req.Schema, extractJSON(res.Content), &answer); verr != nil {
		tr.Message = fmt.Sprintf("the provider answered, but not with valid JSON (%v); try another structured output mode", verr)
		return tr, nil
	}
	if answer.Sum != a+b || !strings.EqualFold(strings.TrimSpace(answer.Echo), word) {
		tr.Message = fmt.Sprintf("valid JSON, but wrong content (sum %d, expected %d; echo %q, expected %q)",
			answer.Sum, a+b, answer.Echo, word)
		return tr, nil
	}
	tr.OK = true
	tr.Message = "structured output works"
	return tr, nil
}

// States of a provider in Status.
const (
	StateReady     = "ready"
	StateWaiting   = "waiting"
	StateExhausted = "exhausted"
	StateDisabled  = "disabled"
	StateNoKey     = "no_key"
)

// Status is a provider with its quota usage and limiter state.
type Status struct {
	Provider       store.LLMProvider
	Today          store.LLMUsage
	MinuteRequests int
	MinuteTokens   int
	State          string
	Reason         string
	AvailableAt    *time.Time
	QuotaResetAt   time.Time
	LastError      string
	LastErrorAt    *time.Time
}

// Status reports every provider in order.
func (p *Pool) Status(ctx context.Context) ([]Status, error) {
	provs, err := p.Store.ListLLMProviders(ctx)
	if err != nil {
		return nil, err
	}
	now := p.Now()
	out := make([]Status, 0, len(provs))
	for i := range provs {
		prov := &provs[i]
		l, err := p.limiter(ctx, prov, now)
		if err != nil {
			return nil, err
		}
		loc := p.location(prov)
		day := dayOf(now, loc)
		today, err := p.Store.GetLLMUsage(ctx, prov.ID, day)
		if err != nil {
			return nil, err
		}
		snap := l.snapshot(now)
		st := Status{
			Provider:       *prov,
			Today:          today,
			MinuteRequests: snap.minuteRequests,
			MinuteTokens:   snap.minuteTokens,
			QuotaResetAt:   nextMidnight(now, loc),
			LastError:      snap.lastError,
			State:          StateReady,
		}
		if !snap.lastErrorAt.IsZero() {
			t := snap.lastErrorAt
			st.LastErrorAt = &t
		}
		a := l.available(now, 0)
		_, hasKey := p.apiKey(prov)
		switch {
		case !prov.Enabled:
			st.State = StateDisabled
		case !hasKey:
			st.State, st.Reason = StateNoKey, fmt.Sprintf("API key variable %s is empty", prov.APIKeyEnv)
		case a.Reason == "":
		case !a.At.Before(st.QuotaResetAt):
			st.State, st.Reason, st.AvailableAt = StateExhausted, a.Reason, &a.At
		default:
			st.State, st.Reason, st.AvailableAt = StateWaiting, a.Reason, &a.At
		}
		out = append(out, st)
	}
	return out, nil
}
