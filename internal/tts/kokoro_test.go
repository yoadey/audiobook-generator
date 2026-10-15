package tts

import (
	"context"
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
