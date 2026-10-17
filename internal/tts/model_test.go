package tts

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelServer is a Chatterbox-TTS-Server whose model can be unloaded and
// loaded again; it records the calls.
type modelServer struct {
	mu       sync.Mutex
	loaded   bool
	calls    []string
	failNext string // a path that answers 500 once
	tts      chan struct{}
}

func (m *modelServer) handler(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.URL.Path)
	fail := m.failNext == r.URL.Path
	if fail {
		m.failNext = ""
	}
	m.mu.Unlock()
	if fail {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	switch r.URL.Path {
	case "/get_predefined_voices":
		_, _ = w.Write([]byte(`[{"display_name":"Emily","filename":"Emily.wav"}]`))
	case "/get_reference_files":
		_, _ = w.Write([]byte(`[]`))
	case "/api/unload":
		m.set(false)
		_, _ = w.Write([]byte(`{"status":"unloaded"}`))
	case "/restart_server":
		m.set(true)
		_, _ = w.Write([]byte(`{"message":"ok","restart_needed":false}`))
	case "/tts":
		_, _ = io.Copy(io.Discard, r.Body)
		if m.tts != nil {
			<-m.tts
		}
		if !m.isLoaded() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"detail":"TTS engine model is not currently loaded or available."}`))
			return
		}
		_, _ = w.Write(wav(SampleRate, 1, []int16{1, 2, 3}))
	default:
		http.NotFound(w, r)
	}
}

func (m *modelServer) set(loaded bool) { m.mu.Lock(); m.loaded = loaded; m.mu.Unlock() }

func (m *modelServer) isLoaded() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.loaded }

// took returns the calls since the last one and forgets them.
func (m *modelServer) took() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.calls
	m.calls = nil
	return out
}

func newModelRouter(t *testing.T, m *modelServer) (*Router, *remote) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(m.handler))
	t.Cleanup(srv.Close)
	r, err := NewRouter(nil, []EngineConfig{{Name: "chatterbox", Kind: KindChatterbox, URL: srv.URL}})
	require.NoError(t, err)
	return r, r.engines["chatterbox"]
}

func TestUnloadIdleModel(t *testing.T) {
	m := &modelServer{loaded: true}
	r, e := newModelRouter(t, m)
	ctx := context.Background()
	speak := func() error {
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "chatterbox:Emily.wav"})
		return err
	}

	// Not idle long enough yet (the app just started).
	done, err := e.unloadIfIdle(ctx, time.Hour)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, speak())
	m.took()
	done, err = e.unloadIfIdle(ctx, 0)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, []string{"/api/unload"}, m.took())
	assert.False(t, m.isLoaded())
	done, err = e.unloadIfIdle(ctx, 0)
	require.NoError(t, err)
	assert.False(t, done, "already unloaded")
	assert.Empty(t, m.took())

	// The next synthesis loads the model first, once.
	require.NoError(t, speak())
	require.NoError(t, speak())
	assert.Equal(t, []string{"/restart_server", "/tts", "/tts"}, m.took())

	// Unloaded behind the app's back: /tts says so, the model is loaded
	// and the request sent again.
	m.set(false)
	require.NoError(t, speak())
	assert.Equal(t, []string{"/tts", "/restart_server", "/tts"}, m.took())

	// A failed load fails the synthesis (the job retries); the next one
	// tries again.
	_, err = e.unloadIfIdle(ctx, 0)
	require.NoError(t, err)
	m.took()
	m.failNext = "/restart_server"
	require.Error(t, speak())
	require.NoError(t, speak())
	assert.Equal(t, []string{"/restart_server", "/restart_server", "/tts"}, m.took())

	// A failed unload keeps the model counted as loaded.
	m.failNext = "/api/unload"
	done, err = e.unloadIfIdle(ctx, 0)
	require.Error(t, err)
	assert.False(t, done)
	require.NoError(t, speak())
	assert.Equal(t, []string{"/api/unload", "/tts"}, m.took())
}

func TestUnloadWaitsForSpeech(t *testing.T) {
	m := &modelServer{loaded: true, tts: make(chan struct{})}
	r, e := newModelRouter(t, m)
	ctx := context.Background()
	errc := make(chan error, 1)
	go func() {
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "chatterbox:Emily.wav"})
		errc <- err
	}()
	require.Eventually(t, func() bool {
		e.model.mu.Lock()
		defer e.model.mu.Unlock()
		return e.model.users == 1
	}, time.Second, time.Millisecond)
	done, err := e.unloadIfIdle(ctx, 0)
	require.NoError(t, err)
	assert.False(t, done, "not while speaking")
	m.tts <- struct{}{}
	require.NoError(t, <-errc)
	assert.True(t, m.isLoaded())
}

func TestUnloadIdleLoop(t *testing.T) {
	m := &modelServer{loaded: true}
	r, _ := newModelRouter(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	on := false
	enabled := func(context.Context) bool { mu.Lock(); defer mu.Unlock(); return on }
	go r.UnloadIdle(ctx, time.Millisecond, 0, enabled, slog.New(slog.DiscardHandler))

	time.Sleep(20 * time.Millisecond)
	assert.True(t, m.isLoaded(), "turned off in the settings")
	mu.Lock()
	on = true
	mu.Unlock()
	require.Eventually(t, func() bool { return !m.isLoaded() }, time.Second, time.Millisecond)
}
