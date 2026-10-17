// Package voicedesign gives characters voices designed from a description
// ("an elderly man, gruff and slow"): a voice design server speaks a
// sample text in such a voice, the clip is uploaded to an engine that
// clones voices (Chatterbox) and becomes the character's voice. The
// server may run only while it is needed: the designer scales its
// Deployment up for a job and down again once idle.
package voicedesign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/kube"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType is the job type of a voice design.
const JobType = "design_voice"

// Queue is the job queue designs run on (one at a time: the server is slow).
const Queue = "design"

// MaxDescriptionRunes bounds a description.
const MaxDescriptionRunes = 500

// SampleText is what a designed voice says: some 15-20 s of plain
// narration, within what Chatterbox clones from (at most 30 s).
const SampleText = "The morning was quiet when I reached the old bridge. I had walked for hours, " +
	"and the river below caught the light like broken glass. Somewhere behind me a bell began to ring, " +
	"slow and patient, and for a moment I forgot why I had come."

// ErrBusy is returned while a character's voice is being designed.
var ErrBusy = errors.New("a voice is being designed for this character")

// Uploader stores a reference clip on an engine that clones voices.
type Uploader interface {
	UploadReference(ctx context.Context, engine, name string, wav []byte) error
}

// Scaler scales the voice design server's Deployment.
type Scaler interface {
	Scale(ctx context.Context, replicas int) error
	Status(ctx context.Context) (kube.Status, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Designer designs voices.
type Designer struct {
	Store  *store.Store
	Jobs   Enqueuer
	Server *Client
	Upload Uploader
	// Engine takes the clips ("chatterbox"); voices become "<engine>:<clip>".
	Engine string
	// Scaler, when set, runs the server only while designs are pending.
	Scaler Scaler
	// Idle is how long the server stays up after the last design.
	Idle time.Duration
	// ReadyTimeout is how long a job waits for the server to start (its
	// first start downloads the model).
	ReadyTimeout time.Duration
	Log          *slog.Logger

	mu       sync.Mutex
	lastUsed time.Time
	busy     int // designs between ensureUp and their end
}

// DefaultDescription describes the voice of a character from its profile,
// as a starting point the user can edit.
func DefaultDescription(c *store.Character) string {
	who := map[store.Gender]string{store.GenderFemale: "woman", store.GenderMale: "man"}[c.Gender]
	if who == "" {
		who = "person"
	}
	switch c.AgeGroup {
	case store.AgeChild:
		who = map[store.Gender]string{store.GenderFemale: "girl", store.GenderMale: "boy"}[c.Gender]
		if who == "" {
			who = "child"
		}
		who = "young " + who
	case store.AgeTeen:
		who = "teenage " + who
	case store.AgeElderly:
		who = "elderly " + who
	case store.AgeAdult:
		who = "adult " + who
	}
	parts := []string{"The voice of " + article(who) + " " + who + "."}
	if s := strings.TrimSpace(c.SpeechStyle); s != "" {
		parts = append(parts, "Speaks "+strings.TrimSuffix(lowerFirst(s), ".")+".")
	}
	if r := strings.TrimSuffix(lowerFirst(strings.TrimSpace(c.Role)), "."); r != "" {
		parts = append(parts, strings.ToUpper(article(r)[:1])+article(r)[1:]+" "+r+".")
	}
	parts = append(parts, "Clear, close recording, natural English.")
	return truncate(strings.Join(parts, " "), MaxDescriptionRunes)
}

func article(w string) string {
	if strings.ContainsRune("aeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return strings.ToLower(string(r)) + s[n:]
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// CleanDescription checks a description ("" and the error message when
// unusable).
func CleanDescription(s string) (string, string) {
	s = strings.Join(strings.Fields(store.CleanText(s)), " ")
	switch {
	case s == "":
		return "", "the description must not be empty"
	case utf8.RuneCountInString(s) > MaxDescriptionRunes:
		return "", fmt.Sprintf("the description must be at most %d characters", MaxDescriptionRunes)
	}
	return s, ""
}

// Start queues the design of a character's voice from a description.
func (d *Designer) Start(ctx context.Context, c *store.Character, description string) error {
	ok, err := d.Store.TransitionDesign(ctx, c.ID, store.DesignQueued,
		map[string]any{"design_description": description, "design_error": ""},
		store.DesignNone, store.DesignDone, store.DesignFailed)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBusy
	}
	if err := d.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &c.BookID, Payload: strconv.FormatInt(c.ID, 10), MaxAttempts: 3,
	}); err != nil {
		// Not left queued without a job.
		if _, rerr := d.Store.TransitionDesign(context.WithoutCancel(ctx), c.ID, store.DesignFailed,
			map[string]any{"design_error": "could not be queued"}, store.DesignQueued); rerr != nil {
			d.Log.Error("undo voice design", "character", c.ID, "err", rerr)
		}
		return err
	}
	return nil
}

// Handler is the job handler for JobType.
func (d *Designer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			id, err := strconv.ParseInt(j.Payload, 10, 64)
			if err != nil {
				return jobs.Permanent(fmt.Errorf("bad voice design payload %q", j.Payload))
			}
			return d.Run(ctx, id, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			id, perr := strconv.ParseInt(j.Payload, 10, 64)
			if perr != nil {
				return
			}
			if _, uerr := d.Store.TransitionDesign(ctx, id, store.DesignFailed,
				map[string]any{"design_error": truncate(store.CleanText(err.Error()), 300)},
				store.DesignQueued, store.DesignRunning); uerr != nil {
				d.Log.Error("mark voice design failed", "character", id, "err", uerr)
			}
		},
	}
}

// Run designs the voice of a character.
func (d *Designer) Run(ctx context.Context, characterID int64, progress func(float64)) error {
	c, err := d.Store.GetCharacter(ctx, characterID)
	if errors.Is(err, store.ErrNotFound) {
		return jobs.Permanent(errors.New("the character was deleted"))
	} else if err != nil {
		return err
	}
	if ok, err := d.Store.TransitionDesign(ctx, c.ID, store.DesignRunning, nil,
		store.DesignQueued, store.DesignRunning); err != nil || !ok {
		return err // asked again or undone meanwhile
	}
	d.begin()
	defer d.end()
	if err := d.ensureUp(ctx); err != nil {
		return err
	}
	progress(0.2)
	// A new seed each time: designing again gives another voice.
	wav, err := d.Server.Design(ctx, SampleText, c.DesignDescription, rand.IntN(1<<31)) //nolint:gosec // a voice seed, not a secret
	if err != nil {
		return err
	}
	progress(0.8)
	// A new name each time: the engine keeps an existing clip of a name.
	sum := sha256.Sum256([]byte(c.DesignDescription + "\x00" + time.Now().UTC().String()))
	name := fmt.Sprintf("%s_b%d-c%d-%s.wav", genderTag(c.Gender), c.BookID, c.ID, hex.EncodeToString(sum[:4]))
	if err := d.Upload.UploadReference(ctx, d.Engine, name, wav); err != nil {
		return err
	}
	note := truncate("designed: "+c.DesignDescription, 200)
	ok, err := d.Store.AssignDesignedVoice(ctx, c.ID, d.Engine+":"+name, note)
	if err != nil {
		return err
	}
	if !ok {
		d.Log.Info("voice designed but no longer wanted (a voice was chosen meanwhile)", "character", c.ID)
		return nil
	}
	d.Log.Info("voice designed", "character", c.ID, "voice", d.Engine+":"+name)
	return nil
}

// genderTag names a clip so its gender is known to casting ("f_", "m_").
func genderTag(g store.Gender) string {
	switch g {
	case store.GenderFemale:
		return "f"
	case store.GenderMale:
		return "m"
	}
	return "x"
}

func (d *Designer) used() {
	d.mu.Lock()
	d.lastUsed = time.Now()
	d.mu.Unlock()
}

// begin marks a design as running: the idler waits for it (it takes the
// same lock, so it never scales down between its check and a design).
func (d *Designer) begin() {
	d.mu.Lock()
	d.busy++
	d.lastUsed = time.Now()
	d.mu.Unlock()
}

func (d *Designer) end() {
	d.mu.Lock()
	d.busy--
	d.lastUsed = time.Now()
	d.mu.Unlock()
}

// pollInterval is how often a starting server is checked.
const pollInterval = 5 * time.Second

// ensureUp scales the server up (if it runs on demand) and waits until it
// answers. A server that takes longer than ReadyTimeout fails the attempt
// (a pod that never starts, e.g. out of memory, must not keep running for
// retries forever).
func (d *Designer) ensureUp(ctx context.Context) error {
	if d.Scaler != nil {
		st, err := d.Scaler.Status(ctx)
		if err != nil {
			return err
		}
		if st.Replicas < 1 {
			d.Log.Info("starting the voice design server")
			if err := d.Scaler.Scale(ctx, 1); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(d.ReadyTimeout)
	for {
		ready := true
		if d.Scaler != nil {
			st, err := d.Scaler.Status(ctx)
			ready = err == nil && st.Ready > 0
		}
		if ready && d.Server.Health(ctx) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the voice design server did not start within %s", d.ReadyTimeout)
		}
		d.used()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Idler scales the server down once no design is pending and it has been
// idle for Idle; it runs until ctx ends. A server left running (an app
// restart) is scaled down too.
func (d *Designer) Idler(ctx context.Context, every time.Duration) {
	if d.Scaler == nil {
		return
	}
	for {
		d.scaleDownIfIdle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (d *Designer) scaleDownIfIdle(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy > 0 || time.Since(d.lastUsed) < d.Idle {
		return
	}
	if n, err := d.Store.PendingJobsOfType(ctx, JobType); err != nil || n > 0 {
		return
	}
	st, err := d.Scaler.Status(ctx)
	if err != nil {
		d.Log.Warn("voice design server status", "err", err)
		return
	}
	if st.Replicas > 0 {
		d.Log.Info("stopping the idle voice design server")
		if err := d.Scaler.Scale(ctx, 0); err != nil {
			d.Log.Warn("stop the voice design server", "err", err)
		}
	}
}

// Client talks to the voice design server: POST /design takes
// {"text", "description", "seed"} and answers a WAV; GET /health answers
// 200 once the model is loaded.
type Client struct {
	url  string
	http *http.Client
}

// NewClient returns a client for the server at url.
func NewClient(url string, timeout time.Duration) *Client {
	return &Client{url: strings.TrimRight(url, "/"), http: &http.Client{Timeout: timeout}}
}

// maxClip bounds a designed clip (30 s of 48 kHz 32-bit stereo is ~12 MB).
const maxClip = 32 << 20

// Design speaks text in a voice described by description.
func (c *Client) Design(ctx context.Context, text, description string, seed int) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"text": text, "description": description, "seed": seed})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/design", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voice design server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxClip+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("voice design server: %s: %s", resp.Status, truncate(strings.TrimSpace(string(data)), 300))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, jobs.Permanent(err)
		}
		return nil, err
	}
	if len(data) > maxClip || len(data) < 12 || string(data[:4]) != "RIFF" {
		return nil, errors.New("voice design server: the answer is not a WAV file")
	}
	return data, nil
}

// Health reports whether the server is up with its model loaded.
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("voice design server: %s", resp.Status)
	}
	return nil
}
