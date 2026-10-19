package tts

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/quota"
)

// memUsage is a UsageStore in memory.
type memUsage struct {
	mu   sync.Mutex
	days map[string]Usage
}

func (m *memUsage) TTSUsage(_ context.Context, engine, day string) (Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.days[engine+"|"+day], nil
}

func (m *memUsage) AddTTSUsage(_ context.Context, engine, day string, u Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.days == nil {
		m.days = map[string]Usage{}
	}
	d := m.days[engine+"|"+day]
	d.Requests += u.Requests
	d.InputTokens += u.InputTokens
	d.OutputTokens += u.OutputTokens
	d.Errors += u.Errors
	d.RateLimited += u.RateLimited
	m.days[engine+"|"+day] = d
	return nil
}

func (m *memUsage) today(engine string) Usage {
	return m.days[engine+"|"+quota.DayOf(time.Now(), mustLoc(geminiTimezone))]
}

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// geminiRequest is what the fake server received.
type geminiRequest struct {
	Path, Key, Text, Voice string
}

// fakeGemini answers generateContent with answer (status 200) or the
// status and body of fail.
func fakeGemini(t *testing.T, answer func(req geminiRequest) (int, string)) (*httptest.Server, *[]geminiRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Contents []struct {
				Parts []struct{ Text string } `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				ResponseModalities []string `json:"responseModalities"`
				SpeechConfig       struct {
					VoiceConfig struct {
						PrebuiltVoiceConfig struct {
							VoiceName string `json:"voiceName"`
						} `json:"prebuiltVoiceConfig"`
					} `json:"voiceConfig"`
				} `json:"speechConfig"`
			} `json:"generationConfig"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, []string{"AUDIO"}, body.GenerationConfig.ResponseModalities)
		req := geminiRequest{Path: r.URL.Path, Key: r.Header.Get("x-goog-api-key"),
			Text: body.Contents[0].Parts[0].Text, Voice: body.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName}
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		status, out := answer(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// audioAnswer is a successful answer with the samples as L16 PCM.
func audioAnswer(rate int, samples []int16) string {
	pcm := make([]byte, 2*len(samples))
	_, _ = binary.Encode(pcm, binary.LittleEndian, samples)
	b, _ := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{
				"mimeType": "audio/L16;codec=pcm;rate=" + itoa(rate), "data": base64.StdEncoding.EncodeToString(pcm),
			}}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 7, "candidatesTokenCount": 75},
	})
	return string(b)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func geminiRouter(t *testing.T, url string, mutate func(*EngineConfig)) (*Router, *memUsage) {
	t.Helper()
	t.Setenv("TEST_GEMINI_KEY", "secret")
	c := EngineConfig{Name: "gemini", Kind: KindGemini, URL: url, APIKeyEnv: "TEST_GEMINI_KEY"}
	if mutate != nil {
		mutate(&c)
	}
	r, err := NewRouter(nil, []EngineConfig{c})
	require.NoError(t, err)
	u := &memUsage{}
	r.SetUsageStore(u)
	return r, u
}

func TestGeminiConfig(t *testing.T) {
	c := EngineConfig{Name: "gemini", Kind: KindGemini, APIKeyEnv: "GEMINI_KEY"}
	require.NoError(t, c.Validate())
	assert.Equal(t, geminiURL, c.URL)
	assert.Equal(t, DefaultGeminiModel, c.Model)
	assert.Equal(t, "America/Los_Angeles", c.QuotaTimezone)
	assert.False(t, *c.Speed, "Gemini takes no speed")
	assert.False(t, *c.Emotions)
	assert.Equal(t, 4000, c.MaxChars)
	assert.True(t, *c.JoinParagraphs, "reads across paragraphs: the quota counts requests")
	assert.Equal(t, 1, c.Concurrency)

	c = EngineConfig{Name: "gemini", Kind: KindGemini, APIKeyEnv: "GEMINI_KEY", Model: "models/gemini-x-tts"}
	require.NoError(t, c.Validate())
	assert.Equal(t, "gemini-x-tts", c.Model, "the models/ prefix is dropped")

	speed := true
	for name, bad := range map[string]EngineConfig{
		"no key":     {Name: "gemini", Kind: KindGemini},
		"speed":      {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", Speed: &speed},
		"limits":     {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", Limits: EngineLimits{RPD: -1}},
		"timezone":   {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", QuotaTimezone: "Mars/Olympus"},
		"model":      {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", Model: "a/b"},
		"voice":      {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", DefaultVoice: "kore"},
		"too long":   {Name: "gemini", Kind: KindGemini, APIKeyEnv: "K", MaxChars: 20000},
		"key on tts": {Name: "orpheus", Kind: KindOpenAI, URL: "http://o", APIKeyEnv: "K"},
	} {
		assert.Error(t, bad.Validate(), name)
	}

	// As the Helm chart renders tts.gemini.
	var es Engines
	require.NoError(t, es.UnmarshalText([]byte(`[{"apiKeyEnv":"ABG_TTS_GEMINI_API_KEY","concurrency":1,
		"defaultVoice":"Kore","emotions":false,"gain":1,"joinParagraphs":true,"kind":"gemini",
		"limits":{"rpd":100,"rpm":0,"tpd":0,"tpm":0},"maxChars":4000,"model":"gemini-3.1-flash-tts-preview","name":"gemini","quotaTimezone":"America/Los_Angeles",
		"timeout":"5m"}]`)))
	require.NoError(t, es[0].Validate())
	assert.Equal(t, EngineLimits{RPD: 100}, es[0].Limits)
	assert.Equal(t, Duration(5*time.Minute), es[0].Timeout)
	assert.True(t, *es[0].JoinParagraphs)

	t.Setenv("EMPTY_KEY", "")
	_, err := NewRouter(nil, []EngineConfig{{Name: "gemini", Kind: KindGemini, APIKeyEnv: "EMPTY_KEY"}})
	assert.ErrorContains(t, err, "EMPTY_KEY is empty")
}

func TestGeminiSynthesize(t *testing.T) {
	srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
		return http.StatusOK, audioAnswer(48000, []int16{100, 300, 500, 700})
	})
	r, usage := geminiRouter(t, srv.URL, func(c *EngineConfig) { c.Model = "tts-model"; c.Gain = 2 })

	pcm, err := r.Synthesize(context.Background(), Speech{Text: "Hello there.", Voice: "gemini:Puck", Format: "pcm"})
	require.NoError(t, err)
	assert.Equal(t, []int16{200, 1000}, samples16(t, pcm), "resampled to 24 kHz, with the gain")
	require.Len(t, *reqs, 1)
	got := (*reqs)[0]
	assert.Equal(t, "/models/tts-model:generateContent", got.Path)
	assert.Equal(t, "secret", got.Key)
	assert.Equal(t, "Hello there.", got.Text, "the text is sent unchanged")
	assert.Equal(t, "Puck", got.Voice)
	assert.Equal(t, Usage{Requests: 1, InputTokens: 7, OutputTokens: 75}, usage.today("gemini"))

	// Without emotions, a quote's emotion is not asked for.
	_, err = r.Synthesize(context.Background(), Speech{Text: "No!", Voice: "gemini:Puck", Emotion: "angry"})
	require.NoError(t, err)
	assert.Equal(t, "No!", (*reqs)[1].Text)

	// Previews are encoded as MP3.
	_, err = r.Synthesize(context.Background(), Speech{Text: "Hi.", Voice: "gemini:Puck", Format: "mp3"})
	assert.ErrorContains(t, err, "no MP3 encoder")
	r.SetMP3Encoder(func(_ context.Context, pcm []byte) ([]byte, error) { return append([]byte("ID3"), pcm...), nil })
	mp3, err := r.Synthesize(context.Background(), Speech{Text: "Hi.", Voice: "gemini:Puck", Format: "mp3"})
	require.NoError(t, err)
	assert.Equal(t, "ID3", string(mp3[:3]))

	engines := r.Engines(context.Background())
	require.Len(t, engines, 1)
	e := engines[0]
	assert.Equal(t, KindGemini, e.Kind)
	assert.Contains(t, e.Voices, "Kore")
	assert.Len(t, e.Voices, 30)
	assert.Equal(t, "gemini:Kore", e.DefaultVoice)
	assert.False(t, e.Speed)
	require.NotNil(t, e.Quota)
	assert.Equal(t, QuotaReady, e.Quota.State)
	assert.EqualValues(t, 3, e.Quota.Today.Requests, "the preview without an encoder sent nothing")
	assert.Equal(t, "America/Los_Angeles", e.Quota.Timezone)
	assert.False(t, e.Quota.Learned)
	assert.Equal(t, RequestOptions{MaxChars: 4000, JoinParagraphs: true}, e.Requests)

	// The request options can be changed while running, and reset.
	assert.True(t, r.JoinParagraphs("gemini:Kore"))
	chars, off := 1500, false
	require.NoError(t, r.SetRequestOverride("gemini", &RequestOverride{MaxChars: &chars, JoinParagraphs: &off}))
	assert.Equal(t, 1500, r.JoinLimit("gemini:Kore"))
	assert.False(t, r.JoinParagraphs("gemini:Kore"))
	e = r.Engines(context.Background())[0]
	assert.Equal(t, RequestOptions{MaxChars: 1500}, e.Requests)
	assert.Equal(t, RequestOptions{MaxChars: 4000, JoinParagraphs: true}, e.Defaults)
	assert.Equal(t, 5000, e.MaxChars)
	// Only what is set is overridden.
	require.NoError(t, r.SetRequestOverride("gemini", &RequestOverride{JoinParagraphs: &off}))
	assert.Equal(t, 4000, r.JoinLimit("gemini:Kore"))
	for _, bad := range []int{50, 6000} {
		assert.Error(t, r.CheckRequestOverride("gemini", &RequestOverride{MaxChars: &bad}), "%d", bad)
	}
	assert.ErrorIs(t, r.SetRequestOverride("nope", nil), ErrUnknownEngine)
	require.NoError(t, r.SetRequestOverride("gemini", nil))
	assert.Equal(t, 4000, r.JoinLimit("gemini:Kore"))
	assert.True(t, r.JoinParagraphs("gemini:Kore"))
	assert.False(t, r.JoinParagraphs("af_heart"), "Kokoro's pieces are never joined")
}

func TestGeminiEmotions(t *testing.T) {
	srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
		return http.StatusOK, audioAnswer(SampleRate, []int16{1})
	})
	r, _ := geminiRouter(t, srv.URL, func(c *EngineConfig) { on := true; c.Emotions = &on })
	assert.True(t, r.Expressive("gemini:Kore"))
	ctx := context.Background()
	for _, s := range []Speech{
		{Text: "No!", Emotion: "angry"},
		{Text: "Perhaps.", Thought: true},
		{Text: "It was late.", Mood: "tense"},
	} {
		s.Voice = "gemini:Kore"
		_, err := r.Synthesize(ctx, s)
		require.NoError(t, err)
	}
	assert.Equal(t, "Say angrily: No!", (*reqs)[0].Text)
	assert.Equal(t, "Say softly, as an inner thought: Perhaps.", (*reqs)[1].Text)
	assert.Equal(t, "It was late.", (*reqs)[2].Text, "narration is sent as it is")
}

func TestGeminiDailyQuota(t *testing.T) {
	answered := 0
	srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
		answered++
		return http.StatusTooManyRequests, `{"error":{"code":429,"message":"You exceeded your current quota.",
			"status":"RESOURCE_EXHAUSTED","details":[
			{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{
				"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests",
				"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier","quotaValue":"15"}]},
			{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"36s"}]}}`
	})
	r, usage := geminiRouter(t, srv.URL, nil)
	ctx := context.Background()

	_, err := r.Synthesize(ctx, Speech{Text: "Hello.", Voice: "gemini:Kore"})
	var ue *UnavailableError
	require.ErrorAs(t, err, &ue)
	loc := mustLoc(geminiTimezone)
	assert.Equal(t, quota.NextMidnight(time.Now(), loc), ue.DeferUntil(), "waits for the daily reset")
	assert.Equal(t, Usage{Errors: 1, RateLimited: 1}, usage.today("gemini"), "a refused request is not counted")

	// Until then, nothing is sent.
	_, err = r.Synthesize(ctx, Speech{Text: "Hello.", Voice: "gemini:Kore"})
	require.ErrorAs(t, err, &ue)
	assert.Len(t, *reqs, 1)

	q := r.Engines(ctx)[0].Quota
	require.NotNil(t, q)
	assert.Equal(t, QuotaExhausted, q.State)
	assert.Equal(t, 15, q.Limits.RPD, "the limit the engine named")
	assert.True(t, q.Learned)
	assert.Contains(t, q.LastError, "exceeded your current quota")
}

func TestGeminiConfiguredLimit(t *testing.T) {
	srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
		return http.StatusOK, audioAnswer(SampleRate, []int16{1})
	})
	r, usage := geminiRouter(t, srv.URL, func(c *EngineConfig) { c.Limits.RPD = 3 })
	ctx := context.Background()
	// Two requests were made earlier today (before a restart).
	require.NoError(t, usage.AddTTSUsage(ctx, "gemini", quota.DayOf(time.Now(), mustLoc(geminiTimezone)), Usage{Requests: 2}))

	_, err := r.Synthesize(ctx, Speech{Text: "One.", Voice: "gemini:Kore"})
	require.NoError(t, err)
	_, err = r.Synthesize(ctx, Speech{Text: "Two.", Voice: "gemini:Kore"})
	var ue *UnavailableError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Reason, "daily limit of 3 requests")
	assert.Len(t, *reqs, 1)

	q := r.Engines(ctx)[0].Quota
	assert.Equal(t, QuotaExhausted, q.State)
	assert.EqualValues(t, 3, q.Today.Requests)
	assert.Equal(t, 3, q.Limits.RPD)
	assert.False(t, q.Learned)
}

func TestGeminiErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid key", func(t *testing.T) {
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusBadRequest, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.",
				"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		var ue *UnavailableError
		require.ErrorAs(t, err, &ue, "waits for the key to be fixed instead of failing")
		assert.Contains(t, ue.Reason, "API key not valid")
		assert.WithinDuration(t, time.Now().Add(geminiKeyWait), ue.Until, time.Minute)
	})
	t.Run("refused text", func(t *testing.T) {
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusOK, `{"candidates":[{"finishReason":"PROHIBITED_CONTENT"}]}`
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		var he *HTTPError
		require.ErrorAs(t, err, &he)
		assert.True(t, he.Permanent())
		assert.Contains(t, he.Body, "PROHIBITED_CONTENT")
	})
	t.Run("no audio", func(t *testing.T) {
		srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"Sure!"}]},"finishReason":"OTHER"}]}`
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		require.ErrorIs(t, err, errNoAudio)
		var he *HTTPError
		assert.False(t, errors.As(err, &he), "asking again may help")
		assert.Len(t, *reqs, 2, "asked once more right away")
		assert.Contains(t, r.Engines(ctx)[0].Quota.LastError, "no audio")
	})
	t.Run("no audio once", func(t *testing.T) {
		n := 0
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			if n++; n == 1 {
				return http.StatusOK, `{"candidates":[{"finishReason":"OTHER"}]}`
			}
			return http.StatusOK, audioAnswer(SampleRate, []int16{7})
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		pcm, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		require.NoError(t, err)
		assert.Equal(t, []int16{7}, samples16(t, pcm))
	})
	t.Run("server error", func(t *testing.T) {
		srv, reqs := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusServiceUnavailable, `{"error":{"code":503,"message":"The model is overloaded."}}`
		})
		r, usage := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		var ue *UnavailableError
		require.ErrorAs(t, err, &ue, "the job waits a little instead of using up an attempt")
		assert.Equal(t, "The model is overloaded.", ue.Reason)
		assert.WithinDuration(t, time.Now().Add(geminiRetry), ue.Until, time.Minute)
		// Meanwhile nothing is sent, and nothing was counted.
		_, err = r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		require.ErrorAs(t, err, &ue)
		assert.Len(t, *reqs, 1)
		assert.Zero(t, usage.today("gemini").Requests)
	})
	t.Run("internal error", func(t *testing.T) {
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusInternalServerError, `{"error":{"code":500,"message":"An internal error has occurred."}}`
		})
		r, usage := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		var he *HTTPError
		require.ErrorAs(t, err, &he, "it uses up an attempt: an error that stays must end")
		assert.False(t, he.Permanent())
		assert.EqualValues(t, 1, usage.today("gemini").Requests)
	})
	t.Run("answer too long", func(t *testing.T) {
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusOK, strings.Repeat(" ", geminiMaxAudio/3*4+(2<<20))
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		var he *HTTPError
		require.ErrorAs(t, err, &he)
		assert.True(t, he.Permanent(), "the same text would be as long again")
	})
	t.Run("audio in parts", func(t *testing.T) {
		pcm := func(v int16) string {
			b := make([]byte, 2)
			_, _ = binary.Encode(b, binary.LittleEndian, v)
			return base64.StdEncoding.EncodeToString(b)
		}
		srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
			return http.StatusOK, `{"candidates":[{"content":{"parts":[
				{"inlineData":{"mimeType":"audio/L16;rate=24000","data":"` + pcm(1) + `"}},
				{"inlineData":{"mimeType":"audio/L16;rate=24000","data":"` + pcm(2) + `"}}]}}]}`
		})
		r, _ := geminiRouter(t, srv.URL, nil)
		out, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore"})
		require.NoError(t, err)
		assert.Equal(t, []int16{1, 2}, samples16(t, out))
	})
}

func TestParseGeminiError(t *testing.T) {
	e := parseGeminiError([]byte(`{"error":{"message":"m","details":[
		{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[
			{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier","quotaValue":"3"},
			{"quotaId":"GenerateContentInputTokensPerModelPerMinute-FreeTier","quotaValue":"10000"}]},
		{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"12.5s"}]}}`))
	assert.Equal(t, "m", e.Message)
	assert.False(t, e.daily)
	assert.Equal(t, EngineLimits{RPM: 3, TPM: 10000}, e.limits)
	assert.Equal(t, 12500*time.Millisecond, e.retryDelay)

	assert.True(t, parseGeminiError([]byte("quota GenerateRequestsPerDay exceeded")).daily)
}

func TestGeminiPCM(t *testing.T) {
	pcm, err := geminiPCM("audio/wav", wav(SampleRate, 1, []int16{5, 6}), 1)
	require.NoError(t, err)
	assert.Equal(t, []int16{5, 6}, samples16(t, pcm))
	pcm, err = geminiPCM("audio/pcm", []byte{1, 0, 2, 0}, 1)
	require.NoError(t, err)
	assert.Equal(t, []int16{1, 2}, samples16(t, pcm), "24 kHz without a rate")
	_, err = geminiPCM("audio/mpeg", nil, 1)
	assert.Error(t, err)
	_, err = geminiPCM("audio/L16;rate=1", []byte{1, 0}, 1)
	assert.Error(t, err, "a rate that would blow up the resampling")
}

func TestGeminiPreviewNotQueued(t *testing.T) {
	srv, _ := fakeGemini(t, func(geminiRequest) (int, string) {
		return http.StatusOK, audioAnswer(SampleRate, []int16{1})
	})
	r, _ := geminiRouter(t, srv.URL, nil)
	r.SetMP3Encoder(func(_ context.Context, pcm []byte) ([]byte, error) { return pcm, nil })
	// A chapter's request holds the engine's only slot.
	r.engines["gemini"].sem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.Synthesize(ctx, Speech{Text: "Hi.", Voice: "gemini:Kore", Format: "mp3"})
	require.NoError(t, err, "a preview does not wait for the slot")

	short, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	_, err = r.Synthesize(short, Speech{Text: "Hi.", Voice: "gemini:Kore", Format: "pcm"})
	assert.ErrorIs(t, err, context.DeadlineExceeded, "a chapter's piece waits for it")
}
