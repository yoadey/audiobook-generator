package tts

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// remote is an engine besides Kokoro.
type remote struct {
	cfg  EngineConfig
	http *http.Client
	sem  chan struct{}

	mu         sync.Mutex
	listed     []string
	references map[string]bool
	fetched    time.Time
}

func newRemote(c EngineConfig) *remote {
	return &remote{
		cfg:  c,
		http: &http.Client{Timeout: time.Duration(c.Timeout)},
		sem:  make(chan struct{}, c.Concurrency),
	}
}

// expression is how strongly Chatterbox acts (exaggeration, 0.25-2,
// neutral 0.5) and how closely it keeps the voice's pace (cfg_weight,
// 0-1, default 0.5; lower is slower and more deliberate, which suits
// strong exaggeration).
type expression struct{ exaggeration, cfgWeight float64 }

var chatterboxNeutral = expression{0.5, 0.5}

var chatterboxEmotions = map[string]expression{
	"happy":      {0.65, 0.45},
	"sad":        {0.6, 0.5},
	"angry":      {0.85, 0.35},
	"afraid":     {0.75, 0.4},
	"surprised":  {0.75, 0.4},
	"whispering": {0.35, 0.5},
	"shouting":   {1.0, 0.3},
	"sarcastic":  {0.6, 0.45},
}

var chatterboxMoods = map[string]expression{
	"calm":       {0.4, 0.5},
	"tense":      {0.6, 0.45},
	"action":     {0.7, 0.4},
	"somber":     {0.45, 0.5},
	"warm":       {0.55, 0.5},
	"light":      {0.55, 0.5},
	"mysterious": {0.5, 0.5},
}

var chatterboxThought = expression{0.4, 0.5}

// expressionOf maps a speech's style to Chatterbox's controls.
func expressionOf(s Speech) expression {
	e, ok := chatterboxEmotions[s.Emotion]
	switch {
	case ok:
	case s.Thought:
		e = chatterboxThought
	default:
		if e, ok = chatterboxMoods[s.Mood]; !ok {
			e = chatterboxNeutral
		}
	}
	return e
}

// chunkRunes is the chunk size Chatterbox splits longer pieces into
// (one generation is at most about 40 s of speech).
const chunkRunes = 250

// synthesize renders with the engine: SampleRate mono PCM for "pcm", the
// engine's MP3 for "mp3".
func (r *remote) synthesize(ctx context.Context, s Speech) ([]byte, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	format := "wav"
	if s.Format == "mp3" {
		format = "mp3"
	}
	path := "/v1/audio/speech"
	var fields map[string]any
	speed := *r.cfg.Speed && s.Speed > 0 && s.Speed != 1
	switch r.cfg.Kind {
	case KindChatterbox:
		// Chatterbox-TTS-Server's own endpoint, which takes the controls
		// per request (its OpenAI one uses the configured defaults).
		path = "/tts"
		fields = map[string]any{
			"text": s.Text, "output_format": format, "split_text": true, "chunk_size": chunkRunes,
			"seed": seedOf(s.Voice),
		}
		if r.clone(ctx, s.Voice) {
			fields["voice_mode"], fields["reference_audio_filename"] = "clone", s.Voice
		} else {
			fields["voice_mode"], fields["predefined_voice_id"] = "predefined", s.Voice
		}
		if r.cfg.Variant == VariantOriginal {
			e := expressionOf(s)
			fields["exaggeration"], fields["cfg_weight"] = e.exaggeration, e.cfgWeight
		}
		if speed {
			fields["speed_factor"] = s.Speed
		}
	default:
		model := r.cfg.Model
		if model == "" {
			model = "tts-1"
		}
		fields = map[string]any{"model": model, "input": s.Text, "voice": s.Voice, "response_format": format}
		if speed {
			fields["speed"] = s.Speed
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.URL, "/")+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.cfg.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &HTTPError{Status: resp.StatusCode, Body: r.cfg.Name + ": " + strings.TrimSpace(string(msg))}
	}
	// WAV may hold up to 4 bytes a sample (float, or 16-bit stereo).
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2*MaxResponseBytes {
		return nil, fmt.Errorf("%s: synthesis response too large", r.cfg.Name)
	}
	if format == "mp3" {
		return data, nil
	}
	pcm, err := pcmFromWAV(data, r.cfg.Gain)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.cfg.Name, err)
	}
	return pcm, nil
}

// seedOf is a voice's fixed seed: Chatterbox samples, and a fixed seed
// keeps a character sounding the same from piece to piece.
func seedOf(voice string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(voice))
	return int(h.Sum32() & 0x7fffffff)
}

// clone: the voice is one of the server's reference clips (cloned), not
// one of its predefined voices.
func (r *remote) clone(ctx context.Context, voice string) bool {
	_, _ = r.voices(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.references[voice]
}

// voicesTTL is how long a listing of an engine's voices is kept.
const voicesTTL = time.Minute

// voices lists the engine's voices: the configured ones plus those it
// lists itself (Chatterbox-TTS-Server: its predefined voices and
// reference clips; OpenAI-compatible: /v1/audio/voices, as Kokoro-FastAPI
// and others offer).
func (r *remote) voices(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listed != nil && time.Since(r.fetched) < voicesTTL {
		return slices.Clone(r.listed), nil
	}
	listCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var listed []string
	var err error
	references := map[string]bool{}
	if r.cfg.Kind == KindChatterbox {
		// Predefined voices and reference clips to clone.
		if listed, err = r.list(listCtx, "/get_predefined_voices"); err == nil {
			var refs []string
			refs, err = r.list(listCtx, "/get_reference_files")
			for _, f := range refs {
				references[f] = true
			}
			listed = append(listed, refs...)
		}
	} else {
		listed, err = r.list(listCtx, "/v1/audio/voices")
	}
	out := slices.Clone(r.cfg.Voices)
	for _, v := range listed {
		if EngineVoiceRe.MatchString(v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	if err != nil {
		return out, err
	}
	r.listed, r.references, r.fetched = out, references, time.Now()
	return slices.Clone(out), nil
}

func (r *remote) list(ctx context.Context, path string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list voices: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return voiceNames(data)
}

// voiceNames reads a voice listing: {"voices": [...]} or a bare array, of
// names or of objects with a "filename", "name" or "id".
func voiceNames(data []byte) ([]string, error) {
	var wrapped struct {
		Voices json.RawMessage `json:"voices"`
	}
	raw := json.RawMessage(data)
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Voices != nil {
		raw = wrapped.Voices
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("list voices: unexpected answer")
	}
	var out []string
	for _, it := range items {
		var name string
		if json.Unmarshal(it, &name) != nil {
			var obj struct{ Filename, Name, ID string }
			if json.Unmarshal(it, &obj) != nil {
				continue
			}
			name = cmp.Or(obj.Filename, obj.Name, obj.ID)
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}
