// Package httpserver assembles the HTTP handler: API, probes, metrics and
// the embedded web UI.
package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Pinger reports whether a dependency is usable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options configures the handler.
type Options struct {
	// API registers the API routes on the mux.
	API interface{ Register(*http.ServeMux) }
	// Feed registers the podcast feed routes (optional).
	Feed interface{ Register(*http.ServeMux) }
	// Ready is checked by /readyz. Only the database: the UI must stay
	// reachable while the TTS server restarts or loads its model.
	Ready    Pinger
	WebUI    http.Handler
	Registry *prometheus.Registry
	Log      *slog.Logger
}

// Handler builds the root handler.
func Handler(o Options) http.Handler {
	mux := http.NewServeMux()
	o.API.Register(mux)
	if o.Feed != nil {
		o.Feed.Register(mux)
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := o.Ready.Ping(ctx); err != nil {
			o.Log.WarnContext(r.Context(), "not ready", "err", err)
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(o.Registry, promhttp.HandlerOpts{}))
	// No method in the pattern: "GET /" would conflict with the API's "/api/".
	mux.Handle("/", o.WebUI)

	return instrument(mux, o.Registry, o.Log)
}

// instrument records Prometheus metrics and logs every request. Route
// labels use the matched ServeMux pattern, never the raw path, to keep
// cardinality bounded.
func instrument(next http.Handler, reg *prometheus.Registry, log *slog.Logger) http.Handler {
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "abg_http_request_duration_seconds",
		Help:    "HTTP request duration by route pattern and status code.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "code"})
	reg.MustRegister(duration)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		duration.WithLabelValues(route, strconv.Itoa(rec.status)).Observe(elapsed.Seconds())

		path := r.URL.Path
		if strings.HasPrefix(path, "/feed/") {
			path = "/feed/…" // the token works like a password
		}
		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelWarn
		}
		log.Log(r.Context(), level, "http request",
			"method", r.Method, "path", path, "route", route,
			"status", rec.status, "duration", elapsed)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush etc. (needed for SSE later).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
