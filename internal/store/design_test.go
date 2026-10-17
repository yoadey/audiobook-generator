package store_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestSettleDesigns(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := &store.Book{Title: "B"}
		require.NoError(t, s.CreateBook(ctx, b))
		pending := &store.Character{BookID: b.ID, Name: "Pending", DesignStatus: store.DesignQueued}
		lost := &store.Character{BookID: b.ID, Name: "Lost", DesignStatus: store.DesignRunning}
		done := &store.Character{BookID: b.ID, Name: "Done", DesignStatus: store.DesignDone}
		for _, c := range []*store.Character{pending, lost, done} {
			require.NoError(t, s.CreateCharacter(ctx, c))
		}
		job := &store.Job{Type: "design_voice", Queue: "design", Payload: strconv.FormatInt(pending.ID, 10)}
		require.NoError(t, s.EnqueueJob(ctx, job))
		status := func(c *store.Character) (store.DesignStatus, string) {
			got, err := s.GetCharacter(ctx, c.ID)
			require.NoError(t, err)
			return got.DesignStatus, got.DesignError
		}

		// Enabled: only the design without a job fails.
		require.NoError(t, s.SettleDesigns(ctx, "design_voice", true))
		st, _ := status(pending)
		assert.Equal(t, store.DesignQueued, st)
		st, msg := status(lost)
		assert.Equal(t, store.DesignFailed, st)
		assert.Contains(t, msg, "interrupted")
		st, _ = status(done)
		assert.Equal(t, store.DesignDone, st)

		// Turned off: the pending job is canceled, its design fails.
		require.NoError(t, s.SettleDesigns(ctx, "design_voice", false))
		j, err := s.GetJob(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, store.JobCanceled, j.Status)
		st, msg = status(pending)
		assert.Equal(t, store.DesignFailed, st)
		assert.Contains(t, msg, "no longer configured")
	})
}
