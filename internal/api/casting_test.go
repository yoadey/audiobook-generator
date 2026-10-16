package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

var refRe = regexp.MustCompile(`(?m)^(c\d+) \| (\w+)`)

// castingAnswer gives Anna and Tom blends; other requests are analyses.
func castingAnswer(r llmtest.Request) llmtest.Reply {
	ms := r.Messages()
	msg, _ := ms[len(ms)-1]["content"].(string)
	if !strings.Contains(msg, "Base voices") {
		return analysisAnswer(r)
	}
	var vs []map[string]any
	for _, m := range refRe.FindAllStringSubmatch(msg, -1) {
		parts := []map[string]any{{"voice": "bm_george", "weight": 0.6}, {"voice": "am_michael", "weight": 0.4}}
		if m[2] == "Anna" {
			parts = []map[string]any{{"voice": "bf_emma", "weight": 0.7}, {"voice": "af_nicole", "weight": 0.3}}
		}
		vs = append(vs, map[string]any{"ref": m[1], "parts": parts, "speed": 1.0, "reason": "fits " + m[2]})
	}
	raw, _ := json.Marshal(map[string]any{"voices": vs})
	return llmtest.Reply{Content: string(raw)}
}

func waitCasting(t *testing.T, c client, id string) api.Casting {
	t.Helper()
	var cs api.Casting
	require.Eventually(t, func() bool {
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/casting", nil, &cs))
		return cs.Status == api.CastingStatusDone || cs.Status == api.CastingStatusFailed
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, api.CastingStatusDone, cs.Status, cs.Error)
	return cs
}

func TestCastingFlow(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		fake := llmtest.New(t)
		fake.Respond = castingAnswer
		p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: fake.BaseURL(), Model: "m"}
		llm.Normalize(p)
		require.NoError(t, s.CreateLLMProvider(ctx, p))

		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)

		var e api.Error
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/books/"+id+"/casting", nil, &e))
		assert.Contains(t, e.Message, "analysis must finish")

		// The finished analysis starts the casting.
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/analysis", nil, nil))
		cs := waitCasting(t, c, id)
		assert.EqualValues(t, 2, cs.MainCount)
		assert.EqualValues(t, 2, cs.VoicedCount)
		assert.Equal(t, "af_heart", cs.NarratorVoice)
		assert.Equal(t, "cast_voices@v1", cs.Prompt)
		assert.Empty(t, cs.Warnings)

		var chars api.CharacterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/characters?tier=main", nil, &chars))
		require.Len(t, chars.Items, 2)
		tom, anna := chars.Items[0], chars.Items[1]
		assert.Equal(t, "bm_george(0.6)+am_michael(0.4)", tom.Voice)
		assert.Equal(t, api.CharacterVoiceSourceOwn, tom.VoiceSource)
		assert.Equal(t, "fits Tom", tom.VoiceNote)
		assert.Equal(t, "bf_emma(0.7)+af_nicole(0.3)", anna.Voice)
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodGet, "/api/books/"+id+"/characters?tier=x", nil, nil))

		// Demoted: a pool voice. Chosen by hand: kept.
		var d api.CharacterDetail
		tomURL := "/api/characters/" + itoa(tom.Id)
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, tomURL, map[string]any{"tier": "minor"}, &d))
		assert.Equal(t, api.TierMinor, d.Tier)
		assert.True(t, d.TierManual)
		assert.Equal(t, api.CharacterDetailVoiceSourcePool, d.VoiceSource)
		assert.False(t, d.UserEdited, "voice changes do not lock the profile")
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, tomURL, map[string]any{"voiceSpeed": 1.1}, &e))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, tomURL,
			map[string]any{"voice": "am_puck(1)+bm_george(3)", "voiceSpeed": 0.95}, &d))
		assert.Equal(t, "bm_george(0.75)+am_puck(0.25)", d.Voice)
		assert.InDelta(t, 0.95, d.VoiceSpeed, 0.001)
		assert.True(t, d.VoiceManual)
		assert.Equal(t, api.CharacterDetailVoiceSourceOwn, d.VoiceSource)
		for _, bad := range []map[string]any{{"voice": "nope"}, {"tier": "hero"}, {"voiceSpeed": 3}} {
			require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, tomURL, bad, nil), bad)
		}

		// Back to automatic as a main character: cast again.
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, tomURL, map[string]any{"tier": "main", "voice": ""}, &d))
		assert.False(t, d.VoiceManual)
		waitCasting(t, c, id)
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, tomURL, nil, &d))
		assert.Equal(t, "bm_george(0.6)+am_michael(0.4)", d.Voice)

		// A narrator like Anna's voice is flagged.
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"narratorVoice": "bf_emma"}, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/casting", nil, &cs))
		require.Len(t, cs.Warnings, 1)
		assert.Equal(t, api.CastingWarningKindNarrator, cs.Warnings[0].Kind)
		assert.Equal(t, anna.Id, cs.Warnings[0].CharacterId)

		// A restart recasts what was not chosen by hand.
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/casting",
			map[string]any{"restart": true}, &cs))
		cs = waitCasting(t, c, id)
		assert.EqualValues(t, 2, cs.VoicedCount)

		var quotes []api.Quote
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/characters/"+itoa(anna.Id)+"/quotes", nil, &quotes))
		require.Len(t, quotes, 1)
		assert.Contains(t, quotes[0].Text, "Is it raining?")
		assert.Equal(t, "Chapter 1", quotes[0].ChapterTitle)
		require.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/characters/999999/quotes", nil, nil))

		// Previews are cached.
		for range 2 {
			require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/voices/preview",
				map[string]any{"voice": anna.Voice, "text": quotes[0].Text}, nil))
		}
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/voices/preview",
			map[string]any{"voice": "af_heart", "text": strings.Repeat("x", 501)}, nil))
	})
}
