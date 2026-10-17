package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/audio/voices", r.URL.Path)
		// Shape of Kokoro-FastAPI v0.9 (api/src/routers/openai_compatible.py).
		_, _ = w.Write([]byte(`{"voices":[
			{"id":"af_heart","name":"af_heart","target_quality":"A","training_duration":null,"overall_grade":"A"},
			{"id":"ef_dora","name":"ef_dora"}
		],"default_voice":"af_heart"}`))
	}))
	defer srv.Close()

	voices, err := New(srv.URL+"/", time.Second).Voices(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Voice{
		{ID: "af_heart", Name: "af_heart", OverallGrade: "A", TargetQuality: "A"},
		{ID: "ef_dora", Name: "ef_dora"},
	}, voices)
}

func TestVoicesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := New(srv.URL, time.Second).Voices(context.Background())
	require.ErrorContains(t, err, "503")
	require.ErrorContains(t, err, "model not loaded")
}

func TestSynthesizeTimed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/dev/captioned_speech", r.URL.Path)
		var req map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "pcm", req["response_format"])
		assert.Equal(t, false, req["stream"])
		assert.Equal(t, true, req["return_timestamps"])
		// Shape of Kokoro-FastAPI v0.9 (CaptionedSpeechResponse).
		_, _ = w.Write([]byte(`{"audio":"AAECAw==","audio_format":"audio/pcm","timestamps":[
			{"word":"Hello","start_time":0.1,"end_time":0.4},{"word":",","start_time":0.4,"end_time":0.45}]}`))
	}))
	defer srv.Close()

	pcm, words, err := New(srv.URL, time.Second).SynthesizeTimed(context.Background(), Speech{Text: "Hello,", Voice: "af_heart"})
	require.NoError(t, err)
	assert.Equal(t, []byte{0, 1, 2, 3}, pcm)
	assert.Equal(t, []Word{{"Hello", 0.1, 0.4}, {",", 0.4, 0.45}}, words)
}

func TestSynthesizeTimedUnsupported(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	for range 2 {
		_, _, err := c.SynthesizeTimed(context.Background(), Speech{Text: "Hi", Voice: "af_heart"})
		require.ErrorIs(t, err, ErrNoTimestamps)
	}
	assert.Equal(t, 1, calls, "asked once, then remembered")
}
