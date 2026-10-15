// Command audiobook-generator runs the API, the job runner and the web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/config"
	"github.com/yoadey/audiobook-generator/internal/httpserver"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
	"github.com/yoadey/audiobook-generator/internal/webui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log, err := newLogger(cfg)
	if err != nil {
		return err
	}
	log.Info("starting", "version", buildinfo.Version, "commit", buildinfo.Commit, "db", cfg.DB.Type)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: httpserver.Handler(httpserver.Options{
			API:      api.NewServer(st, tts.New(cfg.TTS.URL, cfg.TTS.Timeout), log),
			Ready:    st,
			WebUI:    webui.Handler(),
			Registry: reg,
			Log:      log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newLogger(cfg config.Config) (*slog.Logger, error) {
	level, err := cfg.SlogLevel()
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h), nil
}
