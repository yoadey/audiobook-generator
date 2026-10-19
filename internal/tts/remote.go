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
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	fetched    time.Time     // of the last listing (zero: none yet)
	listErr    error         // of the last attempt
	failed     time.Time     // when it failed
	listing    chan struct{} // closed when the running listing is done

	model modelState // Chatterbox only

	gemini *gemini // Gemini only

	options atomic.Pointer[RequestOptions] // nil: as configured
}

// defaultOptions are the configured request options.
func (r *remote) defaultOptions() RequestOptions {
	return RequestOptions{MaxChars: r.cfg.MaxChars, JoinParagraphs: *r.cfg.JoinParagraphs}
}

// requestOptions are the request options in force.
func (r *remote) requestOptions() RequestOptions {
	if o := r.options.Load(); o != nil {
		return *o
	}
	return r.defaultOptions()
}

func newRemote(c EngineConfig) *remote {
	return &remote{
		cfg:   c,
		http:  &http.Client{Timeout: time.Duration(c.Timeout)},
		sem:   make(chan struct{}, c.Concurrency),
		model: modelState{lastUsed: time.Now()},
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

// chunkRunes is the chunk size Chatterbox splits longer text into, by
// sentences; text up to 1.5 times it is generated at once (one generation
// is at most about 40 s of speech, 450 characters about 30 s).
const chunkRunes = 300

// synthesize renders with the engine: SampleRate mono PCM for "pcm", the
// engine's MP3 for "mp3".
func (r *remote) synthesize(ctx context.Context, s Speech) ([]byte, error) {
	if r.gemini != nil {
		// It takes its slot itself, after waiting for its quota.
		return r.geminiSpeak(ctx, s, false)
	}
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
	var data []byte
	var err error
	if r.cfg.Kind == KindChatterbox {
		var done func()
		if done, err = r.use(ctx); err != nil {
			return nil, err
		}
		data, err = r.chatterbox(ctx, s, format)
		done()
	} else {
		model := r.cfg.Model
		if model == "" {
			model = "tts-1"
		}
		fields := map[string]any{"model": model, "input": s.Text, "voice": s.Voice, "response_format": format}
		if r.speed(s) {
			fields["speed"] = s.Speed
		}
		data, err = r.post(ctx, "/v1/audio/speech", fields)
	}
	if err != nil {
		return nil, err
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

func (r *remote) speed(s Speech) bool { return *r.cfg.Speed && s.Speed > 0 && s.Speed != 1 }

// chatterbox asks Chatterbox-TTS-Server's own endpoint, which takes the
// controls per request (its OpenAI one uses the configured defaults).
func (r *remote) chatterbox(ctx context.Context, s Speech, format string) ([]byte, error) {
	clone, err := r.clone(ctx, s.Voice)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		"text": s.Text, "output_format": format, "split_text": true, "chunk_size": chunkRunes,
		"seed": seedOf(s.Voice),
	}
	if r.cfg.Variant == VariantOriginal {
		e := expressionOf(s)
		fields["exaggeration"], fields["cfg_weight"] = e.exaggeration, e.cfgWeight
	}
	if r.speed(s) {
		fields["speed_factor"] = s.Speed
	}
	mode := func(clone bool) {
		delete(fields, "reference_audio_filename")
		delete(fields, "predefined_voice_id")
		if clone {
			fields["voice_mode"], fields["reference_audio_filename"] = "clone", s.Voice
		} else {
			fields["voice_mode"], fields["predefined_voice_id"] = "predefined", s.Voice
		}
	}
	mode(clone)
	loads := r.loadCount()
	data, err := r.post(ctx, "/tts", fields)
	if notLoaded(err) {
		if err = r.reloadModel(ctx, loads); err != nil {
			return nil, err
		}
		data, err = r.post(ctx, "/tts", fields)
	}
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		// A clip added or removed since the voices were listed: the other
		// kind of voice, and list them again soon.
		r.mu.Lock()
		if !r.fetched.IsZero() {
			r.fetched = time.Now().Add(-voicesTTL)
		}
		r.mu.Unlock()
		mode(!clone)
		data, err = r.post(ctx, "/tts", fields)
	}
	return data, err
}

// post sends a synthesis request and reads its audio.
func (r *remote) post(ctx context.Context, path string, fields map[string]any) ([]byte, error) {
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
	return data, nil
}

// Fingerprint identifies an engine's settings that change its audio (and
// the version of how styles are mapped to its controls).
func (r *remote) fingerprint() string {
	if r.cfg.Kind == KindGemini {
		return fmt.Sprintf("%s|%s|%s|%g|%t|%s", r.cfg.Kind, r.cfg.URL, r.cfg.Model, r.cfg.Gain, *r.cfg.Emotions,
			geminiMappingVersion)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%g|%s", r.cfg.Kind, r.cfg.URL, r.cfg.Variant, r.cfg.Model, r.cfg.Gain, mappingVersion)
}

// mappingVersion changes with expressionOf's tables or chunkRunes.
const mappingVersion = "2"

// seedOf is a voice's fixed seed: Chatterbox samples, and a fixed seed
// keeps a character sounding the same from piece to piece.
func seedOf(voice string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(voice))
	return int(h.Sum32() & 0x7fffffff)
}

// errUnlisted: an engine's voices have never been listed, so it is not
// known whether a voice is predefined or cloned (transient).
var errUnlisted = errors.New("its voices could not be listed yet")

// clone: the voice is one of the server's reference clips (cloned), not
// one of its predefined voices.
func (r *remote) clone(ctx context.Context, voice string) (bool, error) {
	r.mu.Lock()
	unlisted := r.fetched.IsZero()
	r.mu.Unlock()
	// Before a first listing, a remembered failure (e.g. of a listing
	// while the server was busy) is not trusted: this request holds the
	// engine's slot, so the server is more likely free now.
	_, err := r.voices(ctx, unlisted)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fetched.IsZero() {
		return false, fmt.Errorf("%s: %w: %w", r.cfg.Name, errUnlisted, err)
	}
	return r.references[voice], nil
}

// Voice listings: kept for voicesTTL, a failure remembered for
// listFailTTL (a busy Chatterbox server answers nothing while it
// synthesizes), one request at a time.
const (
	voicesTTL   = time.Minute
	listFailTTL = 30 * time.Second
	listTimeout = 10 * time.Second
)

// voices lists the engine's voices: the configured ones plus those it
// lists itself (Chatterbox-TTS-Server: its predefined voices and
// reference clips; OpenAI-compatible: /v1/audio/voices, as Kokoro-FastAPI
// and others offer). After a failure the last listing is returned with
// the error.
func (r *remote) voices(ctx context.Context, retryFailed bool) ([]string, error) {
	r.mu.Lock()
	for {
		switch {
		case !r.fetched.IsZero() && time.Since(r.fetched) < voicesTTL:
			defer r.mu.Unlock()
			return slices.Clone(r.listed), nil
		case !retryFailed && r.listErr != nil && time.Since(r.failed) < listFailTTL:
			defer r.mu.Unlock()
			return r.known(), r.listErr
		case r.listing != nil:
			// Another caller is asking; wait for its answer.
			wait := r.listing
			r.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			r.mu.Lock()
			continue
		}
		break
	}
	done := make(chan struct{})
	r.listing = done
	r.mu.Unlock()
	finished := false
	defer func() { // also if fetch panics: waiters must not wait forever
		if !finished {
			r.mu.Lock()
			r.listing = nil
			close(done)
			r.mu.Unlock()
		}
	}()

	// Shared by every waiting caller: one that gives up must not cancel it.
	listCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listTimeout)
	defer cancel()
	listed, references, err := r.fetch(listCtx)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.listing, finished = nil, true
	close(done)
	if err != nil {
		r.listErr, r.failed = fmt.Errorf("%s: %w", r.cfg.Name, err), time.Now()
		return r.known(), r.listErr
	}
	r.listErr = nil
	r.listed, r.references, r.fetched = r.merge(listed), references, time.Now()
	return slices.Clone(r.listed), nil
}

// known: the last listing, or the configured voices (locked).
func (r *remote) known() []string {
	if !r.fetched.IsZero() {
		return slices.Clone(r.listed)
	}
	return r.merge(nil)
}

// merge adds listed voices to the configured ones, sorted.
func (r *remote) merge(listed []string) []string {
	out := slices.Clone(r.cfg.Voices)
	for _, v := range listed {
		if EngineVoiceRe.MatchString(v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

func (r *remote) fetch(ctx context.Context) ([]string, map[string]bool, error) {
	references := map[string]bool{}
	if r.cfg.Kind == KindGemini {
		return slices.Clone(geminiVoices), references, nil
	}
	if r.cfg.Kind != KindChatterbox {
		listed, err := r.list(ctx, "/v1/audio/voices")
		return listed, references, err
	}
	// Predefined voices and reference clips to clone.
	listed, err := r.list(ctx, "/get_predefined_voices")
	if err != nil {
		return nil, nil, err
	}
	refs, err := r.list(ctx, "/get_reference_files")
	if err != nil {
		return nil, nil, err
	}
	for _, f := range refs {
		references[f] = true
	}
	return append(listed, refs...), references, nil
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

// upload stores a reference clip on a Chatterbox-TTS-Server, which then
// offers it as a voice to clone ("<name>.wav"). The server keeps an
// existing file of the same name, so names must be new.
func (r *remote) upload(ctx context.Context, name string, wav []byte) error {
	if r.cfg.Kind != KindChatterbox {
		return fmt.Errorf("%s: only Chatterbox takes reference clips", r.cfg.Name)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", name)
	if err != nil {
		return err
	}
	if _, err := fw.Write(wav); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.URL, "/")+"/upload_reference", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", r.cfg.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Status: resp.StatusCode, Body: r.cfg.Name + ": " + strings.TrimSpace(string(data))}
	}
	var out struct {
		Uploaded []string `json:"uploaded_files"`
		Errors   []struct {
			Error string `json:"error"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("%s: upload: %w", r.cfg.Name, err)
	}
	if !slices.Contains(out.Uploaded, name) {
		msg := "not stored"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Error
		}
		return fmt.Errorf("%s: upload %s: %s", r.cfg.Name, name, msg)
	}
	// List the voices again before the next request.
	r.mu.Lock()
	if !r.fetched.IsZero() {
		r.fetched = time.Now().Add(-voicesTTL)
	}
	r.mu.Unlock()
	return nil
}
