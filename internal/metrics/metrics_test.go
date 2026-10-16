package metrics_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/metrics"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestCollector(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		past := time.Now().UTC().Add(-time.Hour)
		for _, j := range []*store.Job{
			{Type: "render_chapter", Queue: "q", NotBefore: past},
			{Type: "render_chapter", Queue: "q"},
			{Type: "render_chapter", Queue: "q", NotBefore: time.Now().UTC().Add(time.Hour)}, // deferred
		} {
			require.NoError(t, s.EnqueueJob(ctx, j))
		}
		b := &store.Book{Title: "B", Status: store.BookImported}
		require.NoError(t, s.CreateBook(ctx, b))
		require.NoError(t, s.CreateChapterContent(ctx, &store.ChapterContent{
			Chapter: store.Chapter{BookID: b.ID, Title: "C", AudioStatus: store.AudioNone},
		}))

		reg := prometheus.NewRegistry()
		reg.MustRegister(metrics.NewCollector(s, slog.New(slog.DiscardHandler)))
		require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP abg_jobs Jobs by type and status.
# TYPE abg_jobs gauge
abg_jobs{status="queued",type="render_chapter"} 3
# HELP abg_chapters Narrated chapters by audio status.
# TYPE abg_chapters gauge
abg_chapters{audio_status="none"} 1
# HELP abg_database_metrics_up Whether the database could be read for these metrics.
# TYPE abg_database_metrics_up gauge
abg_database_metrics_up 1
`), "abg_jobs", "abg_chapters", "abg_database_metrics_up"))
		wait, err := testutil.GatherAndCount(reg, "abg_job_queue_wait_seconds")
		require.NoError(t, err)
		assert.Equal(t, 1, wait)
		mfs, err := reg.Gather()
		require.NoError(t, err)
		for _, mf := range mfs {
			if mf.GetName() == "abg_job_queue_wait_seconds" {
				assert.InDelta(t, 3600, mf.GetMetric()[0].GetGauge().GetValue(), 60)
			}
		}
	})
}
