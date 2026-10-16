package jobs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics of the job runner. A nil *Metrics records nothing.
type Metrics struct {
	runs     *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers the job runner metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "abg_job_runs_total",
			Help: "Job runs by type and outcome (done, retry, failed, deferred, interrupted).",
		}, []string{"type", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "abg_job_run_duration_seconds",
			Help:    "Duration of job runs by type.",
			Buckets: []float64{0.1, 1, 5, 15, 60, 180, 600, 1800, 3600},
		}, []string{"type"}),
	}
	reg.MustRegister(m.runs, m.duration)
	return m
}

func (m *Metrics) observe(jobType, outcome string, took time.Duration) {
	if m == nil {
		return
	}
	m.runs.WithLabelValues(jobType, outcome).Inc()
	m.duration.WithLabelValues(jobType).Observe(took.Seconds())
}
