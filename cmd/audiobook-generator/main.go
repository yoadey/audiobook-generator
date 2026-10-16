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
	_ "time/tzdata" // quota time zones of LLM providers, without the OS database

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/config"
	"github.com/yoadey/audiobook-generator/internal/feed"
	"github.com/yoadey/audiobook-generator/internal/httpserver"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/render"
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

	llmProviders, err := llm.ParseProviderConfig(cfg.LLM.Providers)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	if err := st.SyncConfigLLMProviders(ctx, llmProviders); err != nil {
		return fmt.Errorf("sync LLM providers: %w", err)
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	llmPool := llm.NewPool(st, llm.NewMetrics(reg), log)
	llmPool.MaxWait = cfg.LLM.MaxWait

	speech := tts.New(cfg.TTS.URL, cfg.TTS.Timeout)
	runner := jobs.New(st, log)
	imp := &importer.Importer{Store: st, DataDir: cfg.DataDir, Log: log}
	runner.Handle(importer.JobType, imp.Handler())
	voices := casting.NewCatalog(speech)
	attributor := &attribution.Attributor{
		Store: st, LLM: llmPool, Jobs: runner, PartTokens: cfg.LLM.AttributionTokens, Log: log,
	}
	runner.Handle(attribution.JobType, attributor.Handler())
	renderer := &render.Renderer{
		Store:        st,
		TTS:          speech,
		Encoder:      audio.Encoder{FFmpeg: cfg.FFmpeg, Bitrate: cfg.AudioBitrate},
		DataDir:      cfg.DataDir,
		Concurrency:  cfg.TTS.Concurrency,
		DefaultVoice: cfg.TTS.DefaultVoice,
		Voices:       voices,
		Attribution:  attributor,
		Metrics:      render.NewMetrics(reg),
		Log:          log,
	}
	runner.Handle(render.JobType, renderer.Handler())
	analyzer := &analysis.Analyzer{
		Store: st, LLM: llmPool, Jobs: runner, BatchTokens: cfg.LLM.AnalysisBatchTokens, Log: log,
	}
	runner.Handle(analysis.JobType, analyzer.Handler())
	if _, err := casting.ParseRecipe(cfg.TTS.DefaultVoice); err != nil {
		return fmt.Errorf("ABG_TTS_DEFAULT_VOICE %q: %w", cfg.TTS.DefaultVoice, err)
	}
	caster := &casting.Caster{
		Store: st, LLM: llmPool, Jobs: runner, Catalog: voices, DefaultVoice: cfg.TTS.DefaultVoice,
		MainCharacters: cfg.Casting.MainCharacters, Log: log,
	}
	runner.Handle(casting.JobType, caster.Handler())
	// A finished analysis casts the voices of its main characters.
	analyzer.OnDone = func(ctx context.Context, bookID int64) {
		b, err := st.GetBook(ctx, bookID)
		if err == nil {
			err = caster.Start(ctx, b, false)
		}
		if err != nil {
			log.Error("start casting", "book", bookID, "err", err)
		}
	}
	scheduler := &render.Scheduler{Store: st, Jobs: runner, DefaultVoice: cfg.TTS.DefaultVoice}
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		if err := runner.Run(ctx); err != nil {
			log.Error("job runner stopped", "err", err)
		}
	}()

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: httpserver.Handler(httpserver.Options{
			API: api.NewServer(api.Options{
				Store:          st,
				TTS:            speech,
				Jobs:           runner,
				Scheduler:      scheduler,
				LLM:            llmPool,
				Analyzer:       analyzer,
				Caster:         caster,
				Attributor:     attributor,
				Voices:         voices,
				DefaultVoice:   cfg.TTS.DefaultVoice,
				DataDir:        cfg.DataDir,
				MaxUploadBytes: cfg.MaxUploadMB << 20,
				FeedSecret:     []byte(cfg.FeedSecret),
				Log:            log,
			}),
			Feed: &feed.Handler{
				Store: st, Secret: []byte(cfg.FeedSecret), PublicURL: cfg.PublicURL,
				DataDir: cfg.DataDir, Log: log,
			},
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
		stop()
		<-runnerDone
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Running jobs see the canceled context and return to the queue.
	<-runnerDone
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
