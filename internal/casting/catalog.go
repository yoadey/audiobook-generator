package casting

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// BaseVoice is a Kokoro voice with what casting needs to know about it.
type BaseVoice struct {
	ID     string       `json:"id"`
	Gender store.Gender `json:"gender"`
	// Accent is a language tag: en-US or en-GB.
	Accent string `json:"accent"`
	// Grade is the overall quality grade of the Kokoro model card (A best).
	Grade string `json:"grade"`
	// Traits describe the timbre, as a hint for casting.
	Traits string `json:"traits"`
	// InCatalog is false for voices only the server knows; Available is
	// false for catalogue voices the server does not offer.
	InCatalog bool `json:"-"`
	Available bool `json:"-"`
}

//go:embed voices.json
var catalogJSON []byte

var catalog = func() []BaseVoice {
	var vs []BaseVoice
	if err := json.Unmarshal(catalogJSON, &vs); err != nil {
		panic(fmt.Errorf("casting: voices.json: %w", err))
	}
	for i := range vs {
		vs[i].InCatalog = true
	}
	return vs
}()

// languages maps Kokoro's voice prefixes to language tags.
var languages = map[byte]string{
	'a': "en-US", 'b': "en-GB", 'e': "es", 'f': "fr", 'h': "hi", 'i': "it",
	'j': "ja", 'p': "pt-BR", 'z': "zh",
}

// describe derives what a voice ID tells ("bf_emma": British, female).
func describe(id string) BaseVoice {
	v := BaseVoice{ID: id, Gender: store.GenderUnknown, Accent: "unknown"}
	if len(id) >= 3 && id[2] == '_' {
		if l, ok := languages[id[0]]; ok {
			v.Accent = l
		}
		switch id[1] {
		case 'f':
			v.Gender = store.GenderFemale
		case 'm':
			v.Gender = store.GenderMale
		}
	}
	return v
}

// English reports whether a voice speaks English.
func (v BaseVoice) English() bool { return strings.HasPrefix(v.Accent, "en-") }

// gradeScore ranks quality grades (A best); ungraded voices rank low.
func gradeScore(g string) float64 {
	base := map[byte]float64{'A': 4, 'B': 3, 'C': 2, 'D': 1, 'F': 0}
	if g == "" {
		return 0.5
	}
	s, ok := base[g[0]]
	if !ok {
		return 0.5
	}
	switch {
	case strings.HasSuffix(g, "+"):
		s += 0.3
	case strings.HasSuffix(g, "-"):
		s -= 0.3
	}
	return s
}

// VoiceLister lists the TTS server's voices.
type VoiceLister interface {
	Voices(ctx context.Context) ([]tts.Voice, error)
}

// Catalog reconciles the embedded catalogue with the voices the TTS server
// offers; the server's answer is cached for a few minutes.
type Catalog struct {
	TTS VoiceLister
	TTL time.Duration

	mu      sync.Mutex
	fetched time.Time
	voices  []BaseVoice
	failed  time.Time
	err     error
}

func assumed() []BaseVoice {
	out := slices.Clone(catalog)
	for i := range out {
		out[i].Available = true
	}
	return out
}

// NewCatalog returns a catalogue backed by a TTS server.
func NewCatalog(t VoiceLister) *Catalog { return &Catalog{TTS: t, TTL: 5 * time.Minute} }

// failTTL is how long a failed server request is remembered: callers that
// only need the voices to display them do not wait for the server again
// on every request.
const failTTL = 30 * time.Second

// Voices returns the catalogue merged with the server's voices: catalogue
// voices the server lacks are marked unavailable, server voices the
// catalogue lacks are described from their IDs. When the server cannot be
// asked, the catalogue is returned with the error, its voices assumed
// available (Kokoro ships them all).
func (c *Catalog) Voices(ctx context.Context) ([]BaseVoice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.voices != nil && time.Since(c.fetched) < c.TTL {
		return slices.Clone(c.voices), nil
	}
	if c.err != nil && time.Since(c.failed) < failTTL {
		return assumed(), c.err
	}
	// The answer is shared: a caller that gives up must not cancel it, nor
	// be remembered as a server failure.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	server, err := c.TTS.Voices(fetchCtx)
	if err != nil {
		c.err, c.failed = fmt.Errorf("TTS server: %w", err), time.Now()
		return assumed(), c.err
	}
	// Server-only voices in a stable order, so pool picks stay the same.
	slices.SortFunc(server, func(a, b tts.Voice) int { return strings.Compare(a.ID, b.ID) })
	c.err = nil
	have := map[string]bool{}
	for _, v := range server {
		have[v.ID] = true
	}
	out := make([]BaseVoice, 0, len(catalog)+len(server))
	known := map[string]bool{}
	for _, v := range catalog {
		v.Available = have[v.ID]
		out = append(out, v)
		known[v.ID] = true
	}
	for _, sv := range server {
		if known[sv.ID] {
			continue
		}
		v := describe(sv.ID)
		v.Grade, v.Available = sv.OverallGrade, true
		out = append(out, v)
	}
	c.voices, c.fetched = out, time.Now()
	return slices.Clone(out), nil
}

// Castable returns the available English voices, the ones casting uses.
func Castable(vs []BaseVoice) []BaseVoice {
	var out []BaseVoice
	for _, v := range vs {
		if v.Available && v.English() && v.Gender != store.GenderUnknown {
			out = append(out, v)
		}
	}
	return out
}
