package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestSettings(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		v, err := s.GetSetting(ctx, store.SettingPreviewText)
		require.NoError(t, err)
		assert.Empty(t, v)
		require.NoError(t, s.SetSetting(ctx, store.SettingPreviewText, "one"))
		require.NoError(t, s.SetSetting(ctx, store.SettingPreviewText, "two\x00"))
		v, err = s.GetSetting(ctx, store.SettingPreviewText)
		require.NoError(t, err)
		assert.Equal(t, "two", v)
	})
}
