package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/httpserver"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

type fakeTTS struct{ err error }

func (fakeTTS) BaseURL() string { return "http://kokoro:8880" }
func (f fakeTTS) Voices(context.Context) ([]tts.Voice, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []tts.Voice{{ID: "af_heart"}, {ID: "bm_george"}}, nil
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func newServer(t *testing.T, s *store.Store, speech api.TTS, ready httpserver.Pinger) *httptest.Server {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(httpserver.Handler(httpserver.Options{
		API:   api.NewServer(s, speech, log),
		Ready: ready,
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("spa"))
		}),
		Registry: prometheus.NewRegistry(),
		Log:      log,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestEndpoints(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		srv := newServer(t, s, fakeTTS{}, s)

		code, body := get(t, srv.URL+"/api/system")
		require.Equal(t, http.StatusOK, code, body)
		var info api.SystemInfo
		require.NoError(t, json.Unmarshal([]byte(body), &info))
		assert.Equal(t, api.DatabaseInfoDialect(s.Dialect()), info.Database.Dialect)
		assert.NotEmpty(t, info.Database.SchemaVersion)
		assert.True(t, info.Tts.Reachable)
		require.NotNil(t, info.Tts.VoiceCount)
		assert.EqualValues(t, 2, *info.Tts.VoiceCount)

		code, body = get(t, srv.URL+"/api/books?limit=10")
		require.Equal(t, http.StatusOK, code, body)
		assert.JSONEq(t, `{"items":[],"total":0}`, body)

		code, _ = get(t, srv.URL+"/api/books?limit=abc")
		assert.Equal(t, http.StatusBadRequest, code)

		code, body = get(t, srv.URL+"/api/nope")
		assert.Equal(t, http.StatusNotFound, code)
		assert.JSONEq(t, `{"message":"not found"}`, body)

		code, body = get(t, srv.URL+"/library/1")
		assert.Equal(t, http.StatusOK, code)
		assert.Equal(t, "spa", body)

		code, _ = get(t, srv.URL+"/readyz")
		assert.Equal(t, http.StatusOK, code)

		code, body = get(t, srv.URL+"/metrics")
		assert.Equal(t, http.StatusOK, code)
		assert.True(t, strings.Contains(body, `route="GET /api/books"`), "metrics use route patterns")
	})
}

func TestSystemInfoWithTTSDown(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		srv := newServer(t, s, fakeTTS{err: errors.New("connection refused")}, s)

		code, body := get(t, srv.URL+"/api/system")
		require.Equal(t, http.StatusOK, code, body)
		var info api.SystemInfo
		require.NoError(t, json.Unmarshal([]byte(body), &info))
		assert.False(t, info.Tts.Reachable)
		require.NotNil(t, info.Tts.Error)
		assert.Contains(t, *info.Tts.Error, "connection refused")
	})
}

func TestReadyzFailsWithoutDatabase(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		srv := newServer(t, s, fakeTTS{}, pinger{err: errors.New("down")})
		code, _ := get(t, srv.URL+"/readyz")
		assert.Equal(t, http.StatusServiceUnavailable, code)

		code, _ = get(t, srv.URL+"/healthz")
		assert.Equal(t, http.StatusOK, code)
	})
}
