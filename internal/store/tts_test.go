package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestTTSUsage(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		u, err := s.GetTTSUsage(ctx, "gemini", "2026-10-28")
		require.NoError(t, err)
		assert.Zero(t, u.Requests, "no row yet")

		require.NoError(t, s.AddTTSUsage(ctx, store.TTSUsage{Engine: "gemini", Day: "2026-10-28", Requests: 1, InputTokens: 10, OutputTokens: 250}))
		require.NoError(t, s.AddTTSUsage(ctx, store.TTSUsage{Engine: "gemini", Day: "2026-10-28", Requests: 1, Errors: 1, RateLimited: 1}))
		require.NoError(t, s.AddTTSUsage(ctx, store.TTSUsage{Engine: "gemini", Day: "2026-10-29", Requests: 1}))
		require.NoError(t, s.AddTTSUsage(ctx, store.TTSUsage{Engine: "other", Day: "2026-10-28", Requests: 5}))

		u, err = s.GetTTSUsage(ctx, "gemini", "2026-10-28")
		require.NoError(t, err)
		assert.EqualValues(t, 2, u.Requests)
		assert.EqualValues(t, 10, u.InputTokens)
		assert.EqualValues(t, 250, u.OutputTokens)
		assert.EqualValues(t, 1, u.Errors)
		assert.EqualValues(t, 1, u.RateLimited)
	})
}
