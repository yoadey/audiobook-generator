package llm

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Request outcomes as metric labels.
const (
	outcomeOK          = "ok"
	outcomeRateLimited = "rate_limited"
	outcomeError       = "error"
	outcomeInvalid     = "invalid_output"
)

// Metrics of the LLM layer. A nil *Metrics records nothing.
type Metrics struct {
	requests *prometheus.CounterVec
	tokens   *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers the LLM metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "abg_llm_requests_total",
			Help: "LLM requests by provider and outcome (ok, rate_limited, error, invalid_output).",
		}, []string{"provider", "outcome"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "abg_llm_tokens_total",
			Help: "Tokens reported by LLM providers, by direction (input, output).",
		}, []string{"provider", "direction"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "abg_llm_request_duration_seconds",
			Help:    "Duration of successful LLM requests.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 40, 80, 160, 320},
		}, []string{"provider"}),
	}
	reg.MustRegister(m.requests, m.tokens, m.duration)
	return m
}

func (m *Metrics) request(provider, outcome string) {
	if m != nil {
		m.requests.WithLabelValues(provider, outcome).Inc()
	}
}

func (m *Metrics) success(provider string, u Usage, took time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(provider, outcomeOK).Inc()
	m.tokens.WithLabelValues(provider, "input").Add(float64(u.InputTokens))
	m.tokens.WithLabelValues(provider, "output").Add(float64(u.OutputTokens))
	m.duration.WithLabelValues(provider).Observe(took.Seconds())
}
