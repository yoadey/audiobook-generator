package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/assemble"
	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/auth"
	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/prepare"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
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
	Store      *store.Store
	TTS        TTS
	Jobs       Enqueuer
	Scheduler  *render.Scheduler
	LLM        *llm.Pool
	Analyzer   *analysis.Analyzer
	Caster     *casting.Caster
	Attributor *attribution.Attributor
	Preparer   *prepare.Preparer
	// Designer designs voices from descriptions (nil: not configured).
	Designer       *voicedesign.Designer
	Assembler      *assemble.Assembler
	Voices         *casting.Catalog
	DefaultVoice   string
	DataDir        string
	MaxUploadBytes int64
	// FeedSecret signs podcast feed URLs; empty disables them.
	FeedSecret []byte
	// AuthEnabled reports that users sign in (OIDC).
	AuthEnabled bool
	Log         *slog.Logger
}

// Server implements StrictServerInterface.
type Server struct {
	Options
	previews *previewCache
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer returns the API implementation.
func NewServer(o Options) *Server {
	return &Server{Options: o, previews: newPreviewCache(32 << 20)}
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
		Tts: TTSInfo{Url: s.TTS.BaseURL(), Configured: s.kokoro(), DefaultVoice: s.DefaultVoice,
			VoiceDesign: s.Designer != nil},
		MaxUploadBytes: s.MaxUploadBytes,
	}
	if s.Designer != nil {
		info.Tts.VoiceDesignEngine = &s.Designer.Engine
	}

	if !info.Tts.Configured {
		return GetSystemInfo200JSONResponse(info), nil
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

// kokoro reports whether Kokoro is configured (a TTS that cannot tell has
// it).
func (s *Server) kokoro() bool {
	k, ok := s.TTS.(interface{ Kokoro() bool })
	return !ok || k.Kokoro()
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

// GetMe returns the signed-in user.
func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	me := Me{AuthEnabled: s.AuthEnabled}
	if u := auth.UserFrom(ctx); u != nil {
		me.Subject, me.Name, me.Email = nonEmpty(u.Subject), nonEmpty(u.Name), nonEmpty(u.Email)
	}
	return GetMe200JSONResponse(me), nil
}
