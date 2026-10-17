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
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // quota time zones of LLM providers, without the OS database

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/assemble"
	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/auth"
	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/config"
	"github.com/yoadey/audiobook-generator/internal/feed"
	"github.com/yoadey/audiobook-generator/internal/httpserver"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/kube"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/metrics"
	"github.com/yoadey/audiobook-generator/internal/prepare"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/transfer"
	"github.com/yoadey/audiobook-generator/internal/tts"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
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

	var kokoro *tts.Client
	if cfg.TTS.Kokoro {
		kokoro = tts.New(cfg.TTS.URL, cfg.TTS.Timeout)
	}
	speech, err := tts.NewRouter(kokoro, cfg.TTS.Engines)
	if err != nil {
		return fmt.Errorf("ABG_TTS_ENGINES: %w", err)
	}
	// Books without a voice of their own speak with the default engine.
	defaultVoice, err := speech.ResolveDefault(cfg.TTS.DefaultVoice)
	if err != nil {
		return fmt.Errorf("ABG_TTS_DEFAULT_VOICE: %w", err)
	}
	runner := jobs.New(st, log)
	runner.Metrics = jobs.NewMetrics(reg)
	reg.MustRegister(metrics.NewCollector(st, log))
	imp := &importer.Importer{Store: st, DataDir: cfg.DataDir, Log: log}
	runner.Handle(importer.JobType, imp.Handler())
	runner.Handle(transfer.JobType, (&transfer.Restorer{Store: st, DataDir: cfg.DataDir, Log: log}).Handler())
	voices := casting.NewCatalog(speech)
	voices.Engines = speech
	attributor := &attribution.Attributor{
		Store: st, LLM: llmPool, Jobs: runner, PartTokens: cfg.LLM.AttributionTokens, Log: log,
	}
	runner.Handle(attribution.JobType, attributor.Handler())
	preparer := &prepare.Preparer{Store: st, LLM: llmPool, Jobs: runner, Log: log}
	runner.Handle(prepare.JobType, preparer.Handler())
	renderer := &render.Renderer{
		Store:        st,
		TTS:          speech,
		Encoder:      audio.Encoder{FFmpeg: cfg.FFmpeg, Bitrate: cfg.AudioBitrate},
		DataDir:      cfg.DataDir,
		Concurrency:  cfg.TTS.Concurrency,
		DefaultVoice: defaultVoice,
		Voices:       voices,
		Attribution:  attributor,
		Preparation:  preparer,
		Metrics:      render.NewMetrics(reg),
		Log:          log,
	}
	runner.Handle(render.JobType, renderer.Handler())
	analyzer := &analysis.Analyzer{
		Store: st, LLM: llmPool, Jobs: runner, BatchTokens: cfg.LLM.AnalysisBatchTokens, Log: log,
	}
	runner.Handle(analysis.JobType, analyzer.Handler())
	if _, err := casting.ParseRecipe(defaultVoice); err != nil {
		return fmt.Errorf("ABG_TTS_DEFAULT_VOICE %q: %w", defaultVoice, err)
	}
	caster := &casting.Caster{
		Store: st, LLM: llmPool, Jobs: runner, Catalog: voices, DefaultVoice: defaultVoice,
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
	var designer *voicedesign.Designer
	if vd := cfg.VoiceDesign; vd.URL != "" {
		if !speech.Clones(vd.Engine) {
			return fmt.Errorf("ABG_VOICE_DESIGN_ENGINE %q must be a Chatterbox engine of ABG_TTS_ENGINES", vd.Engine)
		}
		designer = &voicedesign.Designer{
			Store: st, Jobs: runner, Server: voicedesign.NewClient(vd.URL, vd.Timeout), Upload: speech,
			Engine: vd.Engine, Idle: vd.Idle, ReadyTimeout: vd.ReadyTimeout, Log: log,
		}
		if vd.Deployment != "" {
			scaler, err := kube.InCluster(vd.Deployment)
			if err != nil {
				return fmt.Errorf("ABG_VOICE_DESIGN_DEPLOYMENT: %w", err)
			}
			designer.Scaler = scaler
		}
		runner.Handle(voicedesign.JobType, designer.Handler())
	}
	if err := st.SettleDesigns(ctx, voicedesign.JobType, designer != nil); err != nil {
		return fmt.Errorf("settle voice designs: %w", err)
	}
	scheduler := &render.Scheduler{Store: st, Jobs: runner, DefaultVoice: defaultVoice}
	assembler := &assemble.Assembler{
		Store: st, Jobs: runner, Scheduler: scheduler, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
		DataDir: cfg.DataDir, Log: log,
	}
	runner.Handle(assemble.JobType, assembler.Handler())
	if err := render.ReconcileFiles(ctx, st, cfg.DataDir, log); err != nil {
		log.Error("check audio files", "err", err)
	}
	// Books whose engine changed with the deployment get their characters
	// cast from it.
	if n, err := caster.Reconcile(ctx); err != nil {
		log.Error("recast voices of other engines", "err", err)
	} else if n > 0 {
		log.Info("recasting books whose engine changed", "books", n)
	}
	if designer != nil {
		// Stops the voice design server once idle (also one left running).
		go designer.Idler(ctx, time.Minute)
	}
	if cfg.TTS.UnloadIdle > 0 {
		// Frees the memory of idle Chatterbox models unless turned off in
		// the settings; the next synthesis loads a model again.
		go speech.UnloadIdle(ctx, time.Minute, cfg.TTS.UnloadIdle, func(ctx context.Context) bool {
			on, err := st.UnloadIdleModels(ctx)
			if err != nil {
				log.Warn("read the setting to unload idle speech models", "err", err)
			}
			return on && err == nil
		}, log)
	}
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		if err := runner.Run(ctx); err != nil {
			log.Error("job runner stopped", "err", err)
		}
	}()

	var authn *auth.Authenticator
	if cfg.OIDC.Issuer != "" {
		authn, err = auth.New(auth.Config{
			Issuer: cfg.OIDC.Issuer, ClientID: cfg.OIDC.ClientID, ClientSecret: cfg.OIDC.ClientSecret,
			Scopes: strings.Fields(cfg.OIDC.Scopes), PublicURL: cfg.PublicURL,
			SessionKey: []byte(cfg.OIDC.SessionKey), SessionTTL: cfg.OIDC.SessionTTL, Log: log,
		})
		if err != nil {
			return err
		}
		log.Info("sign-in required", "issuer", cfg.OIDC.Issuer)
	}

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
				Preparer:       preparer,
				Designer:       designer,
				Assembler:      assembler,
				Voices:         voices,
				DefaultVoice:   defaultVoice,
				DataDir:        cfg.DataDir,
				MaxUploadBytes: cfg.MaxUploadMB << 20,
				FeedSecret:     []byte(cfg.FeedSecret),
				AuthEnabled:    authn != nil,
				UnloadIdle:     cfg.TTS.UnloadIdle,
				Log:            log,
			}),
			Auth:      authn,
			PublicURL: cfg.PublicURL,
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
