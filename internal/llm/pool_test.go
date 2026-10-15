package llm_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// clock is a fake time source; sleeping advances it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return nil
}

func newPool(s *store.Store) (*llm.Pool, *clock) {
	c := &clock{now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	p := llm.NewPool(s, nil, slog.New(slog.DiscardHandler))
	p.Now, p.Sleep = c.Now, c.Sleep
	p.Getenv = func(k string) string {
		if k == "FAKE_KEY" {
			return "from-env"
		}
		return ""
	}
	return p, c
}

func addProvider(t *testing.T, s *store.Store, srv *llmtest.Server, name string, pos int, mod func(*store.LLMProvider)) *store.LLMProvider {
	t.Helper()
	p := &store.LLMProvider{Name: name, Position: pos, Enabled: true, BaseURL: srv.BaseURL(), Model: "fake-small", APIKey: "secret-" + name}
	if mod != nil {
		mod(p)
	}
	llm.Normalize(p)
	require.NoError(t, llm.Validate(p))
	require.NoError(t, s.CreateLLMProvider(context.Background(), p))
	return p
}

var sumSchema = &llm.Schema{Name: "sum", JSON: []byte(`{"type":"object","properties":{"sum":{"type":"integer"}},"required":["sum"]}`)}

func userReq(text string) llm.Request {
	return llm.Request{Prompt: "test@v1", Messages: []llm.Message{
		{Role: "system", Content: "You add numbers."},
		{Role: "user", Content: text},
	}}
}

func TestCompleteRecordsUsage(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		prov := addProvider(t, s, srv, "gemini", 0, nil)
		pool, _ := newPool(s)

		srv.Enqueue(llmtest.Reply{Content: "hello"})
		res, err := pool.Complete(ctx, userReq("hi"))
		require.NoError(t, err)
		assert.Equal(t, "hello", res.Content)
		assert.Equal(t, "gemini", res.Provider)
		assert.Equal(t, "fake-small", res.Model)

		reqs := srv.Requests()
		require.Len(t, reqs, 1)
		assert.Equal(t, "Bearer secret-gemini", reqs[0].Header.Get("Authorization"))
		assert.Equal(t, "fake-small", reqs[0].Body["model"])
		assert.NotContains(t, reqs[0].Body, "response_format")

		u, err := s.GetLLMUsage(ctx, prov.ID, "2026-10-15")
		require.NoError(t, err)
		assert.EqualValues(t, 1, u.Requests)
		assert.Positive(t, u.InputTokens)
		assert.EqualValues(t, res.Usage.OutputTokens, u.OutputTokens)

		_, err = pool.Complete(ctx, userReq("again"))
		require.NoError(t, err)
		u, err = s.GetLLMUsage(ctx, prov.ID, "2026-10-15")
		require.NoError(t, err)
		assert.EqualValues(t, 2, u.Requests, "usage rows are incremented, not replaced")
	})
}

func TestFallsBackOnRateLimit(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		primary, backup := llmtest.New(t), llmtest.New(t)
		p1 := addProvider(t, s, primary, "primary", 0, nil)
		addProvider(t, s, backup, "backup", 1, nil)
		pool, c := newPool(s)

		primary.Enqueue(llmtest.Reply{Status: 429, Header: map[string]string{"Retry-After": "600"}, Body: `{"error":"slow down"}`})
		backup.Enqueue(llmtest.Reply{Content: "from backup"})
		res, err := pool.Complete(ctx, userReq("hi"))
		require.NoError(t, err)
		assert.Equal(t, "backup", res.Provider)

		u, err := s.GetLLMUsage(ctx, p1.ID, "2026-10-15")
		require.NoError(t, err)
		assert.EqualValues(t, 1, u.Requests)
		assert.EqualValues(t, 1, u.RateLimited)

		// The primary cools down for the hinted 10 minutes …
		res, err = pool.Complete(ctx, userReq("hi"))
		require.NoError(t, err)
		assert.Equal(t, "backup", res.Provider)
		assert.Len(t, primary.Requests(), 1)

		st, err := pool.Status(ctx)
		require.NoError(t, err)
		assert.Equal(t, llm.StateWaiting, st[0].State)
		assert.Contains(t, st[0].LastError, "slow down")
		assert.Equal(t, llm.StateReady, st[1].State)

		// … and is preferred again afterwards.
		require.NoError(t, c.Sleep(ctx, 10*time.Minute))
		res, err = pool.Complete(ctx, userReq("hi"))
		require.NoError(t, err)
		assert.Equal(t, "primary", res.Provider)
	})
}

func TestExhaustedProvidersDeferJobs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		// Daily quota already used up (persisted before a restart).
		p1 := addProvider(t, s, srv, "daily", 0, func(p *store.LLMProvider) { p.RPD = 5 })
		require.NoError(t, s.AddLLMUsage(ctx, store.LLMUsage{ProviderID: p1.ID, Day: "2026-10-15", Requests: 5}))
		// Rate-limited for a while.
		addProvider(t, s, srv, "minute", 1, nil)
		srv.Enqueue(llmtest.Reply{Status: 429, Header: map[string]string{"Retry-After": "3600"}, Body: "{}"})
		pool, c := newPool(s)

		_, err := pool.Complete(ctx, userReq("hi"))
		var ex *llm.ExhaustedError
		require.ErrorAs(t, err, &ex)
		assert.Equal(t, c.Now().Add(time.Hour), ex.RetryAt, "the earliest provider decides")
		assert.Contains(t, err.Error(), "daily limit of 5 requests")
		assert.Contains(t, err.Error(), "rate-limited")

		var d jobs.Deferrer
		require.ErrorAs(t, err, &d)
		assert.Equal(t, ex.RetryAt, d.DeferUntil())

		st, err := pool.Status(ctx)
		require.NoError(t, err)
		assert.Equal(t, llm.StateExhausted, st[0].State)
		assert.EqualValues(t, 5, st[0].Today.Requests)
		assert.Equal(t, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), st[0].QuotaResetAt)
	})
}

func TestWaitsForPerMinuteWindow(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "slow", 0, func(p *store.LLMProvider) { p.RPM = 1 })
		pool, c := newPool(s)
		start := c.Now()

		for range 3 {
			_, err := pool.Complete(ctx, userReq("hi"))
			require.NoError(t, err)
		}
		assert.Len(t, srv.Requests(), 3)
		assert.Equal(t, 2*time.Minute, c.Now().Sub(start), "waited for the window twice")
	})
}

func TestSkipsProvidersThatRejectARequest(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		small, big := llmtest.New(t), llmtest.New(t)
		addProvider(t, s, small, "small", 0, nil)
		addProvider(t, s, big, "big", 1, nil)
		pool, _ := newPool(s)

		small.Enqueue(llmtest.Reply{Status: 400, Body: `{"error":"context too long"}`})
		res, err := pool.Complete(ctx, userReq("long"))
		require.NoError(t, err)
		assert.Equal(t, "big", res.Provider)

		// A 400 is about the request, not the provider: no cooldown.
		res, err = pool.Complete(ctx, userReq("short"))
		require.NoError(t, err)
		assert.Equal(t, "small", res.Provider)
	})
}

func TestAllProvidersFailing(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "broken", 0, nil)
		pool, _ := newPool(s)

		srv.Enqueue(llmtest.Reply{Status: 401, Body: `{"error":"bad key"}`})
		_, err := pool.Complete(ctx, userReq("hi"))
		// The provider now cools down for 5 minutes: the job waits.
		var ex *llm.ExhaustedError
		require.ErrorAs(t, err, &ex)
		assert.Contains(t, err.Error(), "broken: failing: HTTP 401")

		srv.Enqueue(llmtest.Reply{Status: 400, Body: `{"error":"nope"}`})
		addProvider(t, s, srv, "other", 1, nil)
		srv.Enqueue(llmtest.Reply{Status: 400, Body: `{"error":"nope"}`})
		pool2, _ := newPool(s)
		_, err = pool2.Complete(ctx, userReq("hi"))
		require.Error(t, err)
		assert.NotErrorAs(t, err, &ex, "every provider refused the request: a real error")
		assert.Contains(t, err.Error(), "HTTP 400")
	})
}

func TestNoProviders(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "off", 0, func(p *store.LLMProvider) { p.Enabled = false })
		pool, c := newPool(s)
		_, err := pool.Complete(ctx, userReq("hi"))
		var d jobs.Deferrer
		require.ErrorAs(t, err, &d)
		assert.Equal(t, c.Now().Add(15*time.Minute), d.DeferUntil())
		assert.Empty(t, srv.Requests())

		// A configuration provider without its key is skipped.
		addProvider(t, s, srv, "nokey", 1, func(p *store.LLMProvider) { p.APIKey, p.APIKeyEnv = "", "MISSING" })
		_, err = pool.Complete(ctx, userReq("hi"))
		assert.ErrorContains(t, err, "API key variable MISSING is empty")
	})
}

func TestCompleteJSON(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "p", 0, nil)
		pool, _ := newPool(s)
		req := userReq("2+3")
		req.Schema = sumSchema

		var out struct{ Sum int }
		srv.Enqueue(llmtest.Reply{Content: "```json\n{\"sum\": 5}\n```"})
		_, err := pool.CompleteJSON(ctx, req, &out)
		require.NoError(t, err)
		assert.Equal(t, 5, out.Sum)
		rf := srv.Requests()[0].Body["response_format"].(map[string]any)
		assert.Equal(t, "json_schema", rf["type"])
		assert.Equal(t, "sum", rf["json_schema"].(map[string]any)["name"])

		// Invalid, then repaired.
		srv.Enqueue(llmtest.Reply{Content: `{"sum": "five"}`}, llmtest.Reply{Content: `{"sum": 5}`})
		out.Sum = 0
		_, err = pool.CompleteJSON(ctx, req, &out)
		require.NoError(t, err)
		assert.Equal(t, 5, out.Sum)
		repair := srv.Requests()[2].Messages()
		require.Len(t, repair, 4)
		assert.Equal(t, "assistant", repair[2]["role"])
		assert.Contains(t, repair[3]["content"], "at /sum")

		// Invalid twice.
		srv.Enqueue(llmtest.Reply{Content: `nope`}, llmtest.Reply{Content: `still nope`})
		_, err = pool.CompleteJSON(ctx, req, &out)
		var inv *llm.InvalidOutputError
		require.ErrorAs(t, err, &inv)
		assert.Equal(t, "still nope", inv.Content)

		// Truncated answers are not repaired.
		srv.Enqueue(llmtest.Reply{Content: `{"su`, FinishReason: "length"})
		_, err = pool.CompleteJSON(ctx, req, &out)
		require.ErrorAs(t, err, &inv)
		assert.Contains(t, err.Error(), "truncated")
	})
}

func TestStructuredOutputModes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "obj", 0, func(p *store.LLMProvider) {
			p.StructuredOutput = store.StructuredJSONObject
			p.ExtraParams = `{"reasoning_effort":"low"}`
			p.MaxOutputTokens = 512
		})
		pool, _ := newPool(s)
		req := userReq("2+3")
		req.Schema = sumSchema
		srv.Enqueue(llmtest.Reply{Content: `{"sum": 5}`})
		_, err := pool.CompleteJSON(ctx, req, nil)
		require.NoError(t, err)

		body := srv.Requests()[0].Body
		assert.Equal(t, map[string]any{"type": "json_object"}, body["response_format"])
		assert.Equal(t, "low", body["reasoning_effort"])
		assert.EqualValues(t, 512, body["max_tokens"])
		sys := srv.Requests()[0].Messages()[0]["content"].(string)
		assert.Contains(t, sys, "You add numbers.")
		assert.Contains(t, sys, `"required":["sum"]`, "the schema moves into the prompt")
	})
}

func TestProviderTest(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		prov := addProvider(t, s, srv, "p", 0, func(p *store.LLMProvider) { p.APIKey, p.APIKeyEnv = "", "FAKE_KEY" })
		pool, _ := newPool(s)

		res, err := pool.Test(ctx, prov)
		require.NoError(t, err)
		assert.True(t, res.OK, res.Message)
		assert.Equal(t, "fake-small", res.Model)
		assert.Equal(t, "Bearer from-env", srv.Requests()[0].Header.Get("Authorization"))

		srv.Enqueue(llmtest.Reply{Content: `{"sum": 1, "echo": "x"}`})
		res, err = pool.Test(ctx, prov)
		require.NoError(t, err)
		assert.False(t, res.OK)
		assert.Contains(t, res.Message, "wrong content")

		srv.Enqueue(llmtest.Reply{Content: `I cannot do JSON`})
		res, err = pool.Test(ctx, prov)
		require.NoError(t, err)
		assert.Contains(t, res.Message, "structured output mode")

		srv.Enqueue(llmtest.Reply{Status: 401, Body: `{"error":"invalid key"}`})
		res, err = pool.Test(ctx, prov)
		require.NoError(t, err)
		assert.Contains(t, res.Message, "invalid key")

		models, err := pool.Models(ctx, prov)
		require.NoError(t, err)
		assert.Equal(t, []string{"fake-large", "fake-small"}, models)
	})
}

func TestConfigProviders(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		ps, err := llm.ParseProviderConfig(`[
			{"name": "gemini", "baseUrl": "https://generativelanguage.googleapis.com/v1beta/openai/",
			 "model": "gemini-flash", "apiKeyEnv": "ABG_LLM_PROVIDER_0_API_KEY",
			 "limits": {"rpm": 10, "rpd": 250}, "quotaTimezone": "America/Los_Angeles",
			 "extraParams": {"reasoning_effort": "low"}},
			{"name": "groq", "baseUrl": "https://api.groq.com/openai/v1", "model": "llama",
			 "structuredOutput": "json_object", "enabled": false}
		]`)
		require.NoError(t, err)
		require.Len(t, ps, 2)
		assert.Equal(t, 10, ps[0].RPM)
		assert.JSONEq(t, `{"reasoning_effort":"low"}`, ps[0].ExtraParams)
		assert.False(t, ps[1].Enabled)
		assert.Equal(t, 1, ps[1].Position)

		// A UI provider with a config name is taken over; others stay.
		ui := &store.LLMProvider{Name: "groq", BaseURL: "http://x", Model: "m", Enabled: true, Source: store.ProviderSourceUI}
		require.NoError(t, s.CreateLLMProvider(ctx, ui))
		mine := &store.LLMProvider{Name: "local", BaseURL: "http://ollama:11434/v1", Model: "m", Enabled: true, Position: 5}
		require.NoError(t, s.CreateLLMProvider(ctx, mine))

		require.NoError(t, s.SyncConfigLLMProviders(ctx, ps))
		all, err := s.ListLLMProviders(ctx)
		require.NoError(t, err)
		require.Len(t, all, 3)
		assert.Equal(t, []string{"gemini", "groq", "local"}, []string{all[0].Name, all[1].Name, all[2].Name})
		assert.Equal(t, store.ProviderSourceConfig, all[1].Source)
		assert.Equal(t, ui.ID, all[1].ID)
		assert.False(t, all[1].Enabled)
		assert.Equal(t, "America/Los_Angeles", all[0].QuotaTimezone)

		// Removed from the configuration: deleted.
		require.NoError(t, s.SyncConfigLLMProviders(ctx, ps[:1]))
		all, err = s.ListLLMProviders(ctx)
		require.NoError(t, err)
		assert.Len(t, all, 2)
		require.NoError(t, s.SyncConfigLLMProviders(ctx, nil))
		all, err = s.ListLLMProviders(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Equal(t, "local", all[0].Name)

		// Duplicate names are refused.
		require.ErrorIs(t, s.CreateLLMProvider(ctx, &store.LLMProvider{Name: "local", BaseURL: "http://y", Model: "m"}), store.ErrConflict)
	})
}

func TestParseProviderConfigErrors(t *testing.T) {
	_, err := llm.ParseProviderConfig(`[{"name": "a b", "baseUrl": "ftp://x", "model": "",
		"structuredOutput": "xml", "quotaTimezone": "Mars/Base", "limits": {"rpm": -1}},
		{"name": "ok", "baseUrl": "http://x", "model": "m", "extraParams": {"model": "other"}}]`)
	require.Error(t, err)
	for _, want := range []string{"name must be", "baseUrl must be", "model must not be empty",
		"structuredOutput must be", "quotaTimezone", "not be negative", `must not set "model"`} {
		assert.Contains(t, err.Error(), want)
	}
	_, err = llm.ParseProviderConfig(`[{"name": "x", "bogus": 1}]`)
	assert.ErrorContains(t, err, "unknown field")
	ps, err := llm.ParseProviderConfig(" ")
	require.NoError(t, err)
	assert.Empty(t, ps)
}

func TestFallsBackOnTransientErrors(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		primary, backup := llmtest.New(t), llmtest.New(t)
		addProvider(t, s, primary, "primary", 0, nil)
		addProvider(t, s, backup, "backup", 1, nil)
		pool, c := newPool(s)

		for _, failure := range []llmtest.Reply{
			{Status: 503, Body: `{"error":"overloaded"}`},
			{Status: 429, Body: `{"error":"slow down"}`}, // no Retry-After: short backoff
		} {
			start := c.Now()
			primary.Enqueue(failure)
			res, err := pool.Complete(ctx, userReq("hi"))
			require.NoError(t, err)
			assert.Equal(t, "backup", res.Provider, "the healthy provider takes over at once")
			assert.Equal(t, start, c.Now(), "without waiting for the failed one's cooldown")
			require.NoError(t, c.Sleep(ctx, time.Hour))
		}

		// When the backup fails too, the primary is waited for again.
		primary.Enqueue(llmtest.Reply{Status: 503, Body: "{}"})
		backup.Enqueue(llmtest.Reply{Status: 503, Body: "{}"})
		res, err := pool.Complete(ctx, userReq("hi"))
		require.NoError(t, err)
		assert.Equal(t, "primary", res.Provider)
	})
}

func TestRequestTooLargeForEveryProvider(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		srv := llmtest.New(t)
		addProvider(t, s, srv, "tiny", 0, func(p *store.LLMProvider) { p.TPM = 10 })
		pool, _ := newPool(s)
		_, err := pool.Complete(context.Background(), userReq("a request that is far longer than ten tokens per minute allow"))
		require.Error(t, err)
		var d jobs.Deferrer
		assert.NotErrorAs(t, err, &d, "waiting cannot help, so the job must not be deferred")
		assert.Contains(t, err.Error(), "exceeds the limit")
		assert.Empty(t, srv.Requests())
	})
}

func TestRejectionsAndTruncatedRepairs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		srv := llmtest.New(t)
		addProvider(t, s, srv, "p", 0, nil)
		pool, _ := newPool(s)

		srv.Enqueue(llmtest.Reply{Status: 400, Body: `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`})
		_, err := pool.Complete(ctx, userReq("long"))
		var rej *llm.RejectedError
		require.ErrorAs(t, err, &rej)
		assert.True(t, rej.TooLong())

		srv.Enqueue(llmtest.Reply{Status: 400, Body: `{"error":"invalid schema"}`})
		_, err = pool.Complete(ctx, userReq("bad"))
		require.ErrorAs(t, err, &rej)
		assert.False(t, rej.TooLong())

		req := userReq("2+3")
		req.Schema = sumSchema
		srv.Enqueue(llmtest.Reply{Content: `{"sum": "x"}`}, llmtest.Reply{Content: `{"su`, FinishReason: "length"})
		_, err = pool.CompleteJSON(ctx, req, nil)
		var inv *llm.InvalidOutputError
		require.ErrorAs(t, err, &inv)
		assert.True(t, inv.Truncated, "a cut-off repair answer counts as truncated")
	})
}
