package tts

import (
	"cmp"
	"fmt"
	"strings"
	"unicode"
)

// Qwen3-TTS behind an OpenAI-compatible server (vLLM-Omni and others):
// POST /v1/audio/speech with "instructions" on how to speak, which the
// CustomVoice models follow (emotion, volume, pace, voice quality). It is
// deployed on its own (it needs a GPU); the app only talks to it.
const (
	// DefaultQwen3Model is sent as "model" unless an engine names another.
	DefaultQwen3Model = "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"
	qwen3DefaultVoice = "Ryan"
	qwen3Language     = "English"
	// qwen3MappingVersion changes with how instructions are worded.
	qwen3MappingVersion = "1"
)

// qwen3Voices are the CustomVoice models' speakers, offered when the
// server lists none.
var qwen3Voices = []string{"Aiden", "Dylan", "Eric", "Ono_Anna", "Ryan", "Serena", "Sohee", "Uncle_Fu", "Vivian"}

// validateQwen3 checks a Qwen3 engine and fills in its defaults.
func (c *EngineConfig) validateQwen3() error {
	c.Model = cmp.Or(c.Model, DefaultQwen3Model)
	c.Language = cmp.Or(c.Language, qwen3Language)
	if strings.ContainsAny(c.Language, "\n\r\"") || len(c.Language) > 40 {
		return fmt.Errorf("engine %s: language %q: a name such as English", c.Name, c.Language)
	}
	if c.Emotions == nil {
		on := true
		c.Emotions = &on
	}
	return nil
}

// Wordings of emotions, voicings and moods in instructions, each to follow
// "Say it …". A direction from the attribution replaces the emotion's.
var (
	instructedEmotions = map[string]string{
		"happy":      "happily, with warmth and a smile in the voice",
		"sad":        "sadly, in a subdued, heavy voice",
		"angry":      "angrily, with force and tension",
		"afraid":     "fearfully, with a shaky, tense voice",
		"surprised":  "in surprise, with a lifted, startled voice",
		"whispering": "in a whisper",
		"shouting":   "shouting, loud and forceful",
		"sarcastic":  "sarcastically, with a dry, mocking edge",
	}
	instructedVoicings = map[string]string{
		"thought":   "softly and intimately, as an inner thought",
		"telepathy": "as a telepathic voice heard inside the mind, close and slightly ethereal",
		"remote":    "as a voice heard through a phone, radio or loudspeaker, a little distant",
		"system":    "in a clear, neutral, slightly synthetic announcer tone",
		"writing":   "as if reading written words aloud",
		"song":      "in a sing-song, melodic way, as if singing",
	}
	instructedMoods = map[string]string{
		"calm":       "calmly and evenly, as a storyteller",
		"tense":      "with suspense, tense and a little faster",
		"action":     "briskly and energetically",
		"somber":     "somberly and slowly",
		"warm":       "warmly and gently",
		"light":      "lightly, with a playful touch",
		"mysterious": "in a hushed, mysterious tone",
	}
)

// instruction tells an engine how to speak a piece ("" for the engine's
// usual way): the attribution's direction or the quote's emotion, what
// kind of voice it is, else the narration's mood.
func instruction(s Speech) string {
	var parts []string
	switch d := strings.TrimSpace(s.Direction); {
	case d != "":
		parts = append(parts, d)
	case instructedEmotions[s.Emotion] != "":
		parts = append(parts, instructedEmotions[s.Emotion])
	}
	voicing := s.Voicing
	if voicing == "" && s.Thought {
		voicing = "thought"
	}
	if v := instructedVoicings[voicing]; v != "" {
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		if m := instructedMoods[s.Mood]; m != "" {
			parts = append(parts, m)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimRightFunc(strings.Join(parts, ", "), func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
}

// qwen3Fields is the request body of a Qwen3 synthesis.
func (r *remote) qwen3Fields(s Speech, format string) map[string]any {
	fields := map[string]any{
		"model": r.cfg.Model, "input": s.Text, "voice": s.Voice, "response_format": format,
		"language": r.cfg.Language,
	}
	if *r.cfg.Emotions {
		if in := instruction(s); in != "" {
			fields["instructions"] = "Say it " + in + "."
		}
	}
	if r.speed(s) {
		fields["speed"] = s.Speed
	}
	return fields
}
