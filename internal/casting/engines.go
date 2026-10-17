package casting

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// EngineVoiceLister lists the voices of the engines besides Kokoro.
type EngineVoiceLister interface {
	EngineVoices(ctx context.Context, engine string) ([]string, error)
}

// Voices of Chatterbox-TTS-Server's predefined voices, by file name: the
// gender of the name (the server describes none; Jordan and Taylor are
// left out, their names tell nothing).
//
//go:embed chatterbox_voices.json
var engineGendersJSON []byte

var engineGenders = func() map[string]store.Gender {
	var m map[string]store.Gender
	if err := json.Unmarshal(engineGendersJSON, &m); err != nil {
		panic(fmt.Errorf("casting: chatterbox_voices.json: %w", err))
	}
	return m
}()

// genderTagRe finds a gender in a voice's file name: "f_anna.wav",
// "anna-female.wav", "male_old_wizard.wav".
var genderTagRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(f|m|female|male|woman|man)(?:[^a-z0-9]|$)`)

// describeEngine describes an engine's voice for casting: English, its
// gender from the known voices or a tag in its name (else unknown, so
// it is only used when chosen by hand).
func describeEngine(engine, name string) BaseVoice {
	v := BaseVoice{ID: engine + ":" + name, Gender: store.GenderUnknown, Accent: "en-US", Available: true}
	if g, ok := engineGenders[name]; ok {
		v.Gender, v.InCatalog = g, true
		return v
	}
	if m := genderTagRe.FindStringSubmatch(strings.TrimSuffix(name, path.Ext(name))); m != nil {
		switch strings.ToLower(m[1]) {
		case "f", "female", "woman":
			v.Gender = store.GenderFemale
		default:
			v.Gender = store.GenderMale
		}
	}
	return v
}

// VoicesFor returns the voices of the engine a voice belongs to: Kokoro's
// catalogue (Voices) for Kokoro voices, else the engine's voices. A book
// speaks with the engine of its narrator.
func (c *Catalog) VoicesFor(ctx context.Context, voice string) ([]BaseVoice, error) {
	engine := tts.EngineOf(voice)
	if engine == tts.KokoroEngine {
		return c.Voices(ctx)
	}
	if c.Engines == nil {
		return nil, fmt.Errorf("%w: %q", tts.ErrUnknownEngine, engine)
	}
	names, err := c.Engines.EngineVoices(ctx, engine)
	out := make([]BaseVoice, 0, len(names))
	for _, n := range names {
		out = append(out, describeEngine(engine, n))
	}
	return out, err
}

// mixable: the voices are Kokoro's, which blend.
func mixable(vs []BaseVoice) bool {
	return len(vs) > 0 && !strings.Contains(vs[0].ID, ":")
}
