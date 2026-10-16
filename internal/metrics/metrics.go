// Package metrics exports the state of the database (job queues, rendered
// chapters) as Prometheus metrics, read at scrape time.
package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Collector reads the gauges from the store on every scrape.
type Collector struct {
	store *store.Store
	log   *slog.Logger

	jobs        *prometheus.Desc
	oldestReady *prometheus.Desc
	chapters    *prometheus.Desc
	up          *prometheus.Desc
}

// NewCollector returns a collector over st.
func NewCollector(st *store.Store, log *slog.Logger) *Collector {
	return &Collector{
		store: st,
		log:   log,
		jobs: prometheus.NewDesc("abg_jobs", "Jobs by type and status.",
			[]string{"type", "status"}, nil),
		oldestReady: prometheus.NewDesc("abg_job_queue_wait_seconds",
			"How long the longest-waiting runnable queued job of a type has waited.", []string{"type"}, nil),
		chapters: prometheus.NewDesc("abg_chapters", "Narrated chapters by audio status.",
			[]string{"audio_status"}, nil),
		up: prometheus.NewDesc("abg_database_metrics_up",
			"Whether the database could be read for these metrics.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.jobs
	ch <- c.oldestReady
	ch <- c.chapters
	ch <- c.up
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	jobs, err := c.store.CountJobs(ctx)
	var audio []store.AudioCount
	if err == nil {
		audio, err = c.store.CountChapterAudio(ctx)
	}
	if err != nil {
		c.log.Warn("collect metrics", "err", err)
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	now := time.Now()
	for _, j := range jobs {
		ch <- prometheus.MustNewConstMetric(c.jobs, prometheus.GaugeValue, float64(j.Count), j.Type, string(j.Status))
		if !j.OldestReady.IsZero() {
			ch <- prometheus.MustNewConstMetric(c.oldestReady, prometheus.GaugeValue,
				max(now.Sub(j.OldestReady).Seconds(), 0), j.Type)
		}
	}
	for _, a := range audio {
		ch <- prometheus.MustNewConstMetric(c.chapters, prometheus.GaugeValue, float64(a.Count), string(a.Status))
	}
}
