package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestMigrateIsIdempotent(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		require.NoError(t, s.Migrate(ctx))

		v, err := s.SchemaVersion(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.LatestSchemaVersion(), v)
		require.NoError(t, s.Ping(ctx))
	})
}

func TestLatestMigrationRollsBack(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		require.NoError(t, s.RollbackLast(ctx))
		v, err := s.SchemaVersion(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, store.LatestSchemaVersion(), v)
		require.NoError(t, s.Migrate(ctx))
		v, err = s.SchemaVersion(ctx)
		require.NoError(t, err)
		assert.Equal(t, store.LatestSchemaVersion(), v)
	})
}

func TestListBooks(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()

		books, total, err := s.ListBooks(ctx, 0, 10)
		require.NoError(t, err)
		assert.Empty(t, books)
		assert.Zero(t, total)

		for i := range 3 {
			require.NoError(t, s.CreateBook(ctx, &store.Book{
				Title:  fmt.Sprintf("Book %d", i),
				Status: store.BookImported,
			}))
		}

		books, total, err = s.ListBooks(ctx, 1, 1)
		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		require.Len(t, books, 1)
		assert.Equal(t, "Book 1", books[0].Title)
		assert.False(t, books[0].CreatedAt.IsZero())
	})
}
