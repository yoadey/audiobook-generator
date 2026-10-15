package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/yoadey/audiobook-generator/internal/buildinfo"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// Store is the persistence the API needs.
type Store interface {
	Dialect() string
	SchemaVersion(ctx context.Context) (string, error)
	ListBooks(ctx context.Context, offset, limit int) ([]store.Book, int64, error)
}

// TTS is the speech server the API reports on.
type TTS interface {
	BaseURL() string
	Voices(ctx context.Context) ([]tts.Voice, error)
}

// Server implements StrictServerInterface.
type Server struct {
	store Store
	tts   TTS
	log   *slog.Logger
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer wires the API implementation.
func NewServer(s Store, t TTS, log *slog.Logger) *Server {
	return &Server{store: s, tts: t, log: log}
}

// Register mounts the API under /api on mux.
func (s *Server) Register(mux *http.ServeMux) {
	strict := NewStrictHandlerWithOptions(s, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.ErrorContext(r.Context(), "api handler failed", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		},
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseURL:    "/api",
		BaseRouter: mux,
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
	schema, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	info := SystemInfo{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Database: DatabaseInfo{
			Dialect:       DatabaseInfoDialect(s.store.Dialect()),
			SchemaVersion: schema,
		},
		Tts: TTSInfo{Url: s.tts.BaseURL()},
	}

	// The TTS server may be restarting or still loading its model; report
	// that instead of failing the whole request.
	ttsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if voices, err := s.tts.Voices(ttsCtx); err != nil {
		msg := err.Error()
		info.Tts.Error = &msg
	} else {
		n := len(voices)
		info.Tts.Reachable = true
		info.Tts.VoiceCount = &n
	}
	return GetSystemInfo200JSONResponse(info), nil
}

// ListBooks returns a page of books.
func (s *Server) ListBooks(ctx context.Context, req ListBooksRequestObject) (ListBooksResponseObject, error) {
	offset, limit := 0, 50
	if req.Params.Offset != nil {
		offset = max(0, int(*req.Params.Offset))
	}
	if req.Params.Limit != nil {
		limit = min(max(1, int(*req.Params.Limit)), 500)
	}
	books, total, err := s.store.ListBooks(ctx, offset, limit)
	if err != nil {
		return nil, err
	}
	page := BookPage{Items: make([]Book, 0, len(books)), Total: total}
	for _, b := range books {
		page.Items = append(page.Items, Book{
			Id:        b.ID,
			Title:     b.Title,
			Author:    b.Author,
			Status:    BookStatus(b.Status),
			CreatedAt: b.CreatedAt,
			UpdatedAt: b.UpdatedAt,
		})
	}
	return ListBooks200JSONResponse(page), nil
}
