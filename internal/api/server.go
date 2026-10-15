package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// TTS is the speech server.
type TTS interface {
	BaseURL() string
	Voices(ctx context.Context) ([]tts.Voice, error)
	Synthesize(ctx context.Context, s tts.Speech) ([]byte, error)
}

// Enqueuer schedules background jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Options wires the API's dependencies.
type Options struct {
	Store          *store.Store
	TTS            TTS
	Jobs           Enqueuer
	Scheduler      *render.Scheduler
	LLM            *llm.Pool
	Analyzer       *analysis.Analyzer
	DefaultVoice   string
	DataDir        string
	MaxUploadBytes int64
	Log            *slog.Logger
}

// Server implements StrictServerInterface.
type Server struct {
	Options
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer returns the API implementation.
func NewServer(o Options) *Server {
	return &Server{Options: o}
}

// Register mounts the API under /api on mux.
func (s *Server) Register(mux *http.ServeMux) {
	strict := NewStrictHandlerWithOptions(s, []StrictMiddlewareFunc{withRequest}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Log.ErrorContext(r.Context(), "api handler failed", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		},
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:    "/api",
		BaseRouter: mux,
		Middlewares: []MiddlewareFunc{func(next http.Handler) http.Handler {
			// Bounds every request body; uploads are the only large ones.
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes)
				next.ServeHTTP(w, r)
			})
		}},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
	})
	// Unknown API paths must not fall through to the SPA.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
}

// GetSystemInfo reports version, database and TTS status.
func (s *Server) GetSystemInfo(ctx context.Context, _ GetSystemInfoRequestObject) (GetSystemInfoResponseObject, error) {
	schema, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	info := SystemInfo{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Database: DatabaseInfo{
			Dialect:       DatabaseInfoDialect(s.Store.Dialect()),
			SchemaVersion: schema,
		},
		Tts:            TTSInfo{Url: s.TTS.BaseURL()},
		MaxUploadBytes: s.MaxUploadBytes,
	}

	// The TTS server may be restarting or still loading its model; report
	// that instead of failing the whole request.
	ttsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if voices, err := s.TTS.Voices(ttsCtx); err != nil {
		msg := err.Error()
		info.Tts.Error = &msg
	} else {
		n := len(voices)
		info.Tts.Reachable = true
		info.Tts.VoiceCount = &n
	}
	return GetSystemInfo200JSONResponse(info), nil
}

type requestKey struct{}

// withRequest makes the *http.Request available to handlers that need it
// (range requests for audio).
func withRequest(f StrictHandlerFunc, _ string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, requestKey{}, r), w, r, req)
	}
}

func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}

func errMsg(msg string) Error { return Error{Message: msg} }

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

func page(offset *int32, limit *int32, def, maxLimit int) (int, int) {
	o, l := 0, def
	if offset != nil {
		o = max(0, int(*offset))
	}
	if limit != nil {
		l = min(max(1, int(*limit)), maxLimit)
	}
	return o, l
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
