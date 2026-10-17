package tts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Chatterbox-TTS-Server can drop its model (POST /api/unload; on CPU Nano
// frees about 1.7 of its 2.9 GiB) and load it again (POST /restart_server,
// about 10 s); /tts answers 503 meanwhile. The app unloads the model of an
// idle engine and loads it before the engine speaks again.

// modelState tracks whether an engine's model is loaded and in use.
type modelState struct {
	load     sync.Mutex // held while loading or unloading
	mu       sync.Mutex
	users    int       // syntheses running
	lastUsed time.Time // when the last one ended (or the app started)
	unloaded bool
}

// use marks the model in use until the returned func is called, loading
// it first when it was unloaded.
func (r *remote) use(ctx context.Context) (func(), error) {
	m := &r.model
	m.mu.Lock()
	m.users++
	unloaded := m.unloaded
	m.mu.Unlock()
	done := func() {
		m.mu.Lock()
		m.users--
		m.lastUsed = time.Now()
		m.mu.Unlock()
	}
	if unloaded {
		if err := r.loadModel(ctx); err != nil {
			done()
			return nil, err
		}
	}
	return done, nil
}

// loadModel loads the model unless it is loaded (or another call just
// loaded it).
func (r *remote) loadModel(ctx context.Context) error {
	m := &r.model
	m.load.Lock()
	defer m.load.Unlock()
	m.mu.Lock()
	unloaded := m.unloaded
	m.mu.Unlock()
	if !unloaded {
		return nil
	}
	if _, err := r.post(ctx, "/restart_server", map[string]any{}); err != nil {
		return fmt.Errorf("%s: load the model: %w", r.cfg.Name, err)
	}
	m.mu.Lock()
	m.unloaded = false
	m.mu.Unlock()
	return nil
}

// notLoaded reports Chatterbox-TTS-Server's answer to /tts without a
// model (unloaded by someone else, or before the app knew).
func notLoaded(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusServiceUnavailable &&
		strings.Contains(he.Body, "not currently loaded")
}

// reloadModel loads the model after the server reported it unloaded.
func (r *remote) reloadModel(ctx context.Context) error {
	r.model.mu.Lock()
	r.model.unloaded = true
	r.model.mu.Unlock()
	return r.loadModel(ctx)
}

// unloadIfIdle unloads the model when nothing used it for idle; it
// reports whether it did.
func (r *remote) unloadIfIdle(ctx context.Context, idle time.Duration) (bool, error) {
	m := &r.model
	m.load.Lock()
	defer m.load.Unlock()
	m.mu.Lock()
	if m.unloaded || m.users > 0 || time.Since(m.lastUsed) < idle {
		m.mu.Unlock()
		return false, nil
	}
	// From here a synthesis waits for the unload (m.load) and then loads.
	m.unloaded = true
	m.mu.Unlock()
	if _, err := r.post(ctx, "/api/unload", map[string]any{}); err != nil {
		m.mu.Lock()
		m.unloaded = false
		m.lastUsed = time.Now() // try again after another idle period
		m.mu.Unlock()
		return false, fmt.Errorf("%s: unload the model: %w", r.cfg.Name, err)
	}
	return true, nil
}

// UnloadIdle unloads the models of Chatterbox engines that spoke nothing
// for idle, checking every interval while enabled reports true, until ctx
// ends. The next synthesis loads a model again.
func (r *Router) UnloadIdle(ctx context.Context, every, idle time.Duration, enabled func(context.Context) bool,
	log *slog.Logger) {
	for {
		if enabled(ctx) {
			for _, name := range r.names {
				e := r.engines[name]
				if e.cfg.Kind != KindChatterbox {
					continue
				}
				switch done, err := e.unloadIfIdle(ctx, idle); {
				case err != nil:
					log.Warn("unload an idle speech model", "engine", name, "err", err)
				case done:
					log.Info("unloaded the idle speech model", "engine", name)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
