package render

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics of the renderer. A nil *Metrics records nothing.
type Metrics struct {
	audioSeconds   prometheus.Counter
	realtimeFactor prometheus.Histogram
}

// NewMetrics registers the render metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		audioSeconds: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "abg_rendered_audio_seconds_total",
			Help: "Seconds of audio rendered.",
		}),
		realtimeFactor: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "abg_render_realtime_factor",
			Help:    "Seconds of audio per second of rendering, per chapter.",
			Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 8, 13, 20, 40},
		}),
	}
	reg.MustRegister(m.audioSeconds, m.realtimeFactor)
	return m
}

func (m *Metrics) observe(audio, took time.Duration) {
	if m == nil || took <= 0 {
		return
	}
	m.audioSeconds.Add(audio.Seconds())
	m.realtimeFactor.Observe(audio.Seconds() / took.Seconds())
}
