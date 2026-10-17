package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wav builds a 16-bit PCM WAV of the samples.
func wav(rate, channels int, samples []int16) []byte {
	var b bytes.Buffer
	data := new(bytes.Buffer)
	_ = binary.Write(data, binary.LittleEndian, samples)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data.Len()))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{
		uint32(16), uint16(1), uint16(channels), uint32(rate), uint32(rate * channels * 2),
		uint16(channels * 2), uint16(16),
	} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func samples16(t *testing.T, pcm []byte) []int16 {
	t.Helper()
	out := make([]int16, len(pcm)/2)
	_, err := binary.Decode(pcm, binary.LittleEndian, out)
	require.NoError(t, err)
	return out
}

func TestPCMFromWAV(t *testing.T) {
	pcm, err := pcmFromWAV(wav(SampleRate, 1, []int16{0, 1000, -1000}), 1)
	require.NoError(t, err)
	assert.Equal(t, []int16{0, 1000, -1000}, samples16(t, pcm))

	// Stereo at 48 kHz: mixed down and halved in rate.
	pcm, err = pcmFromWAV(wav(48000, 2, []int16{100, 300, 200, 200, 400, 400, 600, 600}), 1)
	require.NoError(t, err)
	assert.Equal(t, []int16{200, 400}, samples16(t, pcm))

	// The gain never clips.
	pcm, err = pcmFromWAV(wav(SampleRate, 1, []int16{30000, -30000}), 2)
	require.NoError(t, err)
	assert.Equal(t, []int16{31784, -31784}, samples16(t, pcm))

	_, err = pcmFromWAV([]byte("not a wav"), 1)
	assert.Error(t, err)
}

func TestEngineConfig(t *testing.T) {
	var es Engines
	require.NoError(t, es.UnmarshalText([]byte(`[{"name":"chatterbox","kind":"chatterbox","url":"http://cb:8004",
		"timeout":"90s"}]`)))
	require.Len(t, es, 1)
	require.NoError(t, es[0].Validate())
	assert.Equal(t, VariantOriginal, es[0].Variant)
	assert.Equal(t, 1, es[0].Concurrency)
	assert.Equal(t, Duration(90*time.Second), es[0].Timeout)
	assert.False(t, *es[0].Speed, "Chatterbox paces itself")

	for name, c := range map[string]EngineConfig{
		"name":    {Name: "Big", Kind: KindOpenAI, URL: "http://x"},
		"kind":    {Name: "x", Kind: "piper", URL: "http://x"},
		"url":     {Name: "x", Kind: KindOpenAI, URL: "x:80"},
		"variant": {Name: "x", Kind: KindChatterbox, URL: "http://x", Variant: "flash"},
		"voice":   {Name: "x", Kind: KindOpenAI, URL: "http://x", Voices: []string{"a b"}},
	} {
		assert.Error(t, c.Validate(), name)
	}
	assert.Error(t, es.UnmarshalText([]byte(`{`)))
}

func TestSplitVoice(t *testing.T) {
	e, v, ok := SplitVoice("chatterbox:Emily.wav")
	assert.True(t, ok)
	assert.Equal(t, "chatterbox", e)
	assert.Equal(t, "Emily.wav", v)
	for _, bad := range []string{"af_heart", "af_bella(2)+af_sky(1)", ":x", "x:", "X:y", "x:a b"} {
		_, _, ok := SplitVoice(bad)
		assert.False(t, ok, bad)
	}
}

// chatterboxServer imitates Chatterbox-TTS-Server (devnen) v2.
func chatterboxServer(t *testing.T, got *[]map[string]any) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get_predefined_voices":
			_, _ = w.Write([]byte(`[{"display_name":"Emily","filename":"Emily.wav"},{"display_name":"Bad","filename":"a b.wav"}]`))
		case "/get_reference_files":
			_, _ = w.Write([]byte(`["anna.wav"]`))
		case "/tts":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(raw, &body))
			mu.Lock()
			*got = append(*got, body)
			mu.Unlock()
			_, _ = w.Write(wav(SampleRate, 1, []int16{1, 2, 3}))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRouterChatterbox(t *testing.T) {
	var got []map[string]any
	srv := chatterboxServer(t, &got)
	defer srv.Close()
	kokoro := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/audio/speech", r.URL.Path)
		_, _ = w.Write([]byte("kokoro"))
	}))
	defer kokoro.Close()
	r, err := NewRouter(New(kokoro.URL, time.Second), []EngineConfig{
		{Name: "chatterbox", Kind: KindChatterbox, URL: srv.URL, Voices: []string{"extra.wav"}},
	})
	require.NoError(t, err)
	ctx := context.Background()

	assert.True(t, r.Expressive("chatterbox:Emily.wav"))
	assert.False(t, r.Expressive("af_heart"))
	assert.False(t, r.TakesSpeed("chatterbox:Emily.wav"))
	assert.True(t, r.TakesSpeed("af_heart"))

	es := r.Engines(ctx)
	require.Len(t, es, 1)
	assert.Equal(t, Engine{Name: "chatterbox", Kind: KindChatterbox, Emotions: true,
		Voices: []string{"Emily.wav", "anna.wav", "extra.wav"}}, es[0])

	pcm, err := r.Synthesize(ctx, Speech{Text: "Hello.", Voice: "chatterbox:Emily.wav", Speed: 1.2, Emotion: "angry"})
	require.NoError(t, err)
	assert.Equal(t, []int16{1, 2, 3}, samples16(t, pcm))
	_, err = r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "chatterbox:anna.wav", Mood: "calm"})
	require.NoError(t, err)
	_, _, err = r.SynthesizeTimed(ctx, Speech{Text: "Hi.", Voice: "chatterbox:anna.wav"})
	assert.ErrorIs(t, err, ErrNoTimestamps)
	require.Len(t, got, 2)
	assert.Equal(t, "predefined", got[0]["voice_mode"])
	assert.Equal(t, "Emily.wav", got[0]["predefined_voice_id"])
	assert.InDelta(t, 0.85, got[0]["exaggeration"], 1e-9)
	assert.InDelta(t, 0.35, got[0]["cfg_weight"], 1e-9)
	assert.Equal(t, "wav", got[0]["output_format"])
	assert.NotContains(t, got[0], "speed_factor", "no speed: Chatterbox paces itself")
	assert.Equal(t, "clone", got[1]["voice_mode"])
	assert.Equal(t, "anna.wav", got[1]["reference_audio_filename"])
	assert.InDelta(t, 0.4, got[1]["exaggeration"], 1e-9)
	assert.Equal(t, got[0]["seed"], float64(seedOf("Emily.wav")), "a fixed seed per voice")

	data, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "af_heart"})
	require.NoError(t, err)
	assert.Equal(t, "kokoro", string(data))

	_, err = r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "orpheus:tara"})
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.True(t, he.Permanent(), "an unconfigured engine is not retried")
}

func TestRouterTurboAndOpenAI(t *testing.T) {
	var got []map[string]any
	srv := chatterboxServer(t, &got)
	defer srv.Close()
	var openai map[string]any
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/audio/voices" {
			_, _ = w.Write([]byte(`{"voices":[{"id":"tara"},{"id":"leo"}]}`))
			return
		}
		assert.Equal(t, "/v1/audio/speech", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &openai))
		_, _ = w.Write(wav(SampleRate, 1, []int16{5}))
	}))
	defer other.Close()
	r, err := NewRouter(New("http://kokoro", time.Second), []EngineConfig{
		{Name: "turbo", Kind: KindChatterbox, URL: srv.URL, Variant: VariantTurbo},
		{Name: "orpheus", Kind: KindOpenAI, URL: other.URL, Model: "orpheus", Gain: 2},
	})
	require.NoError(t, err)
	ctx := context.Background()
	assert.False(t, r.Expressive("turbo:Emily.wav"), "Turbo ignores exaggeration")
	_, err = r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "turbo:Emily.wav", Emotion: "angry"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.NotContains(t, got[0], "exaggeration")

	assert.True(t, r.TakesSpeed("orpheus:tara"))
	pcm, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "orpheus:tara", Speed: 1.1})
	require.NoError(t, err)
	assert.Equal(t, []int16{10}, samples16(t, pcm), "its gain evens out loudness")
	assert.Equal(t, map[string]any{"model": "orpheus", "input": "Hi.", "voice": "tara", "response_format": "wav",
		"speed": 1.1}, openai)
	es := r.Engines(ctx)
	require.Len(t, es, 2)
	assert.Equal(t, []string{"leo", "tara"}, es[0].Voices)

	_, err = NewRouter(New("http://kokoro", time.Second), []EngineConfig{
		{Name: "a", Kind: KindOpenAI, URL: "http://a"}, {Name: "a", Kind: KindOpenAI, URL: "http://b"},
	})
	assert.Error(t, err, "twice")
}
