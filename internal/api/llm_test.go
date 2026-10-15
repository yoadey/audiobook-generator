package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestLLMProviders(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 1<<20)
		fake := llmtest.New(t)

		var list []api.LLMProvider
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/llm/providers", nil, &list))
		assert.Empty(t, list)

		// Models of a server that is not saved yet.
		var models []string
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/models",
			map[string]any{"baseUrl": fake.BaseURL()}, &models))
		assert.Equal(t, []string{"fake-large", "fake-small"}, models)

		var bad api.Error
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/llm/providers", map[string]any{
			"name": "x y", "baseUrl": "nope", "model": "m", "quotaTimezone": "Nowhere/Land",
		}, &bad))
		assert.Contains(t, bad.Message, "baseUrl must be")
		assert.Contains(t, bad.Message, "quotaTimezone")

		var created api.LLMProvider
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/llm/providers", map[string]any{
			"name": "gemini", "baseUrl": fake.BaseURL(), "model": "fake-small", "apiKey": "k1",
			"limits": map[string]int{"rpm": 10, "rpd": 250},
		}, &created))
		assert.True(t, created.HasApiKey)
		assert.Equal(t, api.LLMProviderSourceUi, created.Source)
		assert.Equal(t, api.LLMStructuredOutputJsonSchema, created.StructuredOutput)
		assert.Equal(t, 250, *created.Limits.Rpd)
		assert.Equal(t, "UTC", created.QuotaTimezone)
		assert.Equal(t, api.LLMProviderStateReady, created.Status.State)
		id := itoa(created.Id)

		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/llm/providers", map[string]any{
			"name": "gemini", "baseUrl": fake.BaseURL(), "model": "m",
		}, nil))

		var second api.LLMProvider
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/llm/providers", map[string]any{
			"name": "groq", "baseUrl": fake.BaseURL(), "model": "fake-large", "structuredOutput": "json_object",
		}, &second))
		assert.Equal(t, created.Position+1, second.Position, "appended after the last provider")
		assert.False(t, second.HasApiKey)

		var res api.LLMTestResult
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/providers/"+id+"/test", nil, &res))
		assert.True(t, res.Ok, res.Message)
		require.NotNil(t, res.LatencyMs)
		assert.Equal(t, "Bearer k1", fake.Requests()[0].Header.Get("Authorization"))

		fake.Enqueue(llmtest.Reply{Status: 429, Header: map[string]string{"Retry-After": "120"}, Body: `{"error":"quota"}`})
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/providers/"+id+"/test", nil, &res))
		assert.False(t, res.Ok)
		assert.Contains(t, res.Message, "HTTP 429")

		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/llm/providers", nil, &list))
		require.Len(t, list, 2)
		assert.Equal(t, api.LLMProviderStateWaiting, list[0].Status.State)
		require.NotNil(t, list[0].Status.LastError)
		assert.EqualValues(t, 2, list[0].Status.Today.Requests)
		assert.EqualValues(t, 1, list[0].Status.Today.RateLimited)
		assert.Equal(t, 2, list[0].Status.MinuteRequests)
		assert.Equal(t, api.LLMProviderStateReady, list[1].Status.State)

		// Partial update; an empty key removes it, the stored key is never returned.
		var updated api.LLMProvider
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/llm/providers/"+id, map[string]any{
			"enabled": false, "apiKey": "", "limits": map[string]int{"rpm": 5},
		}, &updated))
		assert.False(t, updated.Enabled)
		assert.False(t, updated.HasApiKey)
		assert.Equal(t, 5, *updated.Limits.Rpm)
		assert.Equal(t, 250, *updated.Limits.Rpd, "other limits unchanged")
		assert.Equal(t, api.LLMProviderStateDisabled, updated.Status.State)
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/llm/providers/"+id,
			map[string]any{"extraParams": "[1]"}, nil))
		require.Equal(t, http.StatusConflict, c.json(http.MethodPatch, "/api/llm/providers/"+id,
			map[string]any{"name": "groq"}, nil))

		// Configuration providers are read-only.
		require.NoError(t, s.SyncConfigLLMProviders(context.Background(), []store.LLMProvider{{
			Name: "helm", BaseURL: fake.BaseURL(), Model: "fake-small", APIKeyEnv: "ABG_TEST_UNSET_KEY", Enabled: true,
			StructuredOutput: store.StructuredJSONSchema, TimeoutSeconds: 60, QuotaTimezone: "UTC",
		}}))
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/llm/providers", nil, &list))
		require.Len(t, list, 3)
		var helm api.LLMProvider
		for _, p := range list {
			if p.Name == "helm" {
				helm = p
			}
		}
		assert.Equal(t, api.LLMProviderSourceConfig, helm.Source)
		assert.Equal(t, "ABG_TEST_UNSET_KEY", *helm.ApiKeyEnv)
		assert.Equal(t, api.LLMProviderStateNoKey, helm.Status.State)
		var conflict api.Error
		require.Equal(t, http.StatusConflict, c.json(http.MethodPatch, "/api/llm/providers/"+itoa(helm.Id),
			map[string]any{"enabled": false}, &conflict))
		assert.Contains(t, conflict.Message, "Helm values")
		require.Equal(t, http.StatusConflict, c.json(http.MethodDelete, "/api/llm/providers/"+itoa(helm.Id), nil, nil))

		// Models of a stored provider use its stored settings.
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/models",
			map[string]any{"providerId": second.Id}, &models))
		assert.Len(t, models, 2)
		require.Equal(t, http.StatusBadGateway, c.json(http.MethodPost, "/api/llm/models",
			map[string]any{"baseUrl": "http://127.0.0.1:1/v1"}, nil))

		// A stored key never goes to another URL.
		other := llmtest.New(t)
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/models",
			map[string]any{"providerId": created.Id, "baseUrl": other.BaseURL()}, &models))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/llm/providers/"+itoa(second.Id),
			map[string]any{"apiKey": "k2"}, nil))
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/llm/providers/"+itoa(second.Id),
			map[string]any{"baseUrl": other.BaseURL()}, &bad))
		assert.Contains(t, bad.Message, "enter the API key again")
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/llm/providers/"+itoa(second.Id),
			map[string]any{"baseUrl": other.BaseURL(), "apiKey": "k3"}, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/llm/models",
			map[string]any{"providerId": second.Id}, &models))
		assert.Equal(t, []string{"", "Bearer k3"}, other.ModelsAuthorization(),
			"no key for an unsaved URL; the new key after re-entering it")

		require.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/llm/providers/"+id, nil, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodDelete, "/api/llm/providers/"+id, nil, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/llm/providers/"+id+"/test", nil, nil))
	})
}
