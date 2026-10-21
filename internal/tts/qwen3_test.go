package tts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQwen3Config(t *testing.T) {
	c := EngineConfig{Name: "qwen3", Kind: KindQwen3, URL: "http://qwen3:8091"}
	require.NoError(t, c.Validate())
	assert.Equal(t, DefaultQwen3Model, c.Model)
	assert.Equal(t, "English", c.Language)
	assert.True(t, *c.Emotions, "it acts by default")
	assert.False(t, *c.Speed, "its pace follows the instructions")
	assert.True(t, *c.JoinParagraphs)
	assert.Equal(t, 1000, c.MaxChars)

	for _, bad := range []EngineConfig{
		{Name: "q", Kind: KindQwen3, URL: "http://q", Language: "Eng\"lish"},
		{Name: "q", Kind: KindQwen3, URL: "http://q", APIKeyEnv: "KEY"},
		{Name: "o", Kind: KindOpenAI, URL: "http://o", Language: "English"},
		{Name: "o", Kind: KindOpenAI, URL: "http://o", Emotions: new(true)},
	} {
		assert.Error(t, bad.Validate(), "%+v", bad)
	}
}

func TestInstruction(t *testing.T) {
	for _, c := range []struct {
		s    Speech
		want string
	}{
		{Speech{}, ""},
		{Speech{Mood: "tense"}, "with suspense, tense and a little faster"},
		{Speech{Emotion: "angry"}, "angrily, with force and tension"},
		{Speech{Emotion: "angry", Direction: "through clenched teeth, seething."}, "through clenched teeth, seething"},
		{Speech{Thought: true}, "softly and intimately, as an inner thought"},
		{Speech{Direction: "warm, amused", Voicing: "telepathy", Thought: true},
			"warm, amused, as a telepathic voice heard inside the mind, close and slightly ethereal"},
		{Speech{Voicing: "speech", Mood: "calm"}, "calmly and evenly, as a storyteller"},
		{Speech{Voicing: "remote", Mood: "calm"}, "as a voice heard through a phone, radio or loudspeaker, a little distant"},
	} {
		assert.Equal(t, c.want, instruction(c.s), "%+v", c.s)
	}
}

func TestRouterQwen3(t *testing.T) {
	var got []map[string]any
	listing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/audio/voices" {
			if !listing {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"voices":["vivian","ryan","f_clone"]}`))
			return
		}
		assert.Equal(t, "/v1/audio/speech", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		got = append(got, body)
		_, _ = w.Write(wav(SampleRate, 1, []int16{5}))
	}))
	defer srv.Close()
	r, err := NewRouter(nil, []EngineConfig{{Name: "qwen3", Kind: KindQwen3, URL: srv.URL}})
	require.NoError(t, err)
	ctx := context.Background()
	assert.True(t, r.Expressive("qwen3:ryan"))
	assert.False(t, r.TakesSpeed("qwen3:ryan"))
	assert.Equal(t, "qwen3:Ryan", r.DefaultVoice("qwen3"))

	_, err = r.Synthesize(ctx, Speech{Text: "Run!", Voice: "qwen3:ryan", Speed: 1.2, Emotion: "afraid",
		Direction: "panicked, fast and breathless", Voicing: "speech"})
	require.NoError(t, err)
	_, err = r.Synthesize(ctx, Speech{Text: "The door creaked.", Voice: "qwen3:ryan"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, map[string]any{
		"model": DefaultQwen3Model, "input": "Run!", "voice": "ryan", "response_format": "wav", "language": "English",
		"instructions": "Say it panicked, fast and breathless.",
	}, got[0])
	assert.NotContains(t, got[1], "instructions", "plain narration: the engine's usual way")

	es := r.Engines(ctx)
	require.Len(t, es, 1)
	assert.Equal(t, []string{"f_clone", "ryan", "vivian"}, es[0].Voices)
	assert.Equal(t, DefaultQwen3Model, es[0].Model)

	// A server that lists no voices offers the CustomVoice speakers.
	listing = false
	r, err = NewRouter(nil, []EngineConfig{{Name: "qwen3", Kind: KindQwen3, URL: srv.URL, Timeout: Duration(time.Second)}})
	require.NoError(t, err)
	vs, err := r.EngineVoices(ctx, "qwen3")
	require.NoError(t, err)
	assert.Equal(t, qwen3Voices, vs)

	// Without emotions, no instructions.
	off := false
	r, err = NewRouter(nil, []EngineConfig{{Name: "qwen3", Kind: KindQwen3, URL: srv.URL, Emotions: &off}})
	require.NoError(t, err)
	assert.False(t, r.Expressive("qwen3:ryan"))
	_, err = r.Synthesize(ctx, Speech{Text: "No.", Voice: "qwen3:ryan", Emotion: "angry"})
	require.NoError(t, err)
	assert.NotContains(t, got[len(got)-1], "instructions")
}
