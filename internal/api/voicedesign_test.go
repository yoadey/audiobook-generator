package api_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
)

type jobQueue struct{ s *store.Store }

func (q jobQueue) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = voicedesign.Queue
	return q.s.EnqueueJob(ctx, j)
}

func TestVoiceDesign(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		kokoro := &store.Book{Title: "K", Status: store.BookReady}
		cb := &store.Book{Title: "C", Status: store.BookReady, NarratorVoice: "cb:Emily.wav"}
		require.NoError(t, s.CreateBook(ctx, kokoro))
		require.NoError(t, s.CreateBook(ctx, cb))
		anna := &store.Character{BookID: cb.ID, Name: "Anna", Gender: store.GenderFemale, AgeGroup: store.AgeAdult,
			Tier: store.TierMain, Role: "innkeeper"}
		tom := &store.Character{BookID: cb.ID, Name: "Tom", Gender: store.GenderMale, Tier: store.TierMain,
			VoiceRecipe: "cb:Adrian.wav", VoiceManual: true}
		minor := &store.Character{BookID: cb.ID, Name: "Guard", Tier: store.TierMinor}
		ken := &store.Character{BookID: kokoro.ID, Name: "Ken", Tier: store.TierMain}
		for _, c := range []*store.Character{anna, tom, minor, ken} {
			require.NoError(t, s.CreateCharacter(ctx, c))
		}
		log := slog.New(slog.DiscardHandler)

		// Without a designer: nothing to design with.
		plain := serve(t, api.Options{Store: s, TTS: fakeTTS{}, DefaultVoice: "af_heart", Log: log})
		var info api.SystemInfo
		require.Equal(t, http.StatusOK, plain.json(http.MethodGet, "/api/system", nil, &info))
		assert.False(t, info.Tts.VoiceDesign)
		assert.Nil(t, info.Tts.VoiceDesignEngine)
		var detail api.CharacterDetail
		require.Equal(t, http.StatusOK, plain.json(http.MethodGet, "/api/characters/"+itoa(anna.ID), nil, &detail))
		assert.Equal(t, api.VoiceDesignStatusNone, detail.VoiceDesign.Status)
		assert.Equal(t, "The voice of an adult woman. An innkeeper. Clear, close recording, natural English.",
			detail.VoiceDesign.SuggestedDescription)
		assert.Equal(t, http.StatusConflict, plain.json(http.MethodPost,
			"/api/characters/"+itoa(anna.ID)+"/voice-design", map[string]any{"description": "A deep voice."}, nil))

		d := &voicedesign.Designer{Store: s, Jobs: jobQueue{s}, Engine: "cb", Idle: time.Hour, Log: log}
		c := serve(t, api.Options{Store: s, TTS: fakeTTS{}, DefaultVoice: "af_heart", Designer: d, Log: log})
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/system", nil, &info))
		assert.True(t, info.Tts.VoiceDesign)
		assert.Equal(t, ptr("cb"), info.Tts.VoiceDesignEngine)

		path := "/api/characters/" + itoa(anna.ID) + "/voice-design"
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, path, map[string]any{"description": " "}, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/characters/999999/voice-design",
			map[string]any{"description": "A deep voice."}, nil))
		var vd api.VoiceDesign
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, path,
			map[string]any{"description": " A deep\nvoice. "}, &vd))
		assert.Equal(t, api.VoiceDesignStatusQueued, vd.Status)
		assert.Equal(t, "A deep voice.", vd.Description)
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, path,
			map[string]any{"description": "Again."}, nil), "already queued")

		// A book of another engine designs none (its voices are another engine's).
		var e api.Error
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/characters/"+itoa(ken.ID)+"/voice-design",
			map[string]any{"description": "A deep voice."}, &e))
		assert.Contains(t, e.Message, "switch it to cb")
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/books/"+itoa(kokoro.ID)+"/voice-design", nil, nil))

		// The whole book: main characters not chosen by hand nor already queued.
		var batch api.VoiceDesignBatch
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+itoa(cb.ID)+"/voice-design", nil, &batch))
		assert.Equal(t, 0, batch.Queued, "Anna is queued, Tom chosen, the guard minor")
		require.NoError(t, s.DB().Model(&store.Character{}).Where("id = ?", anna.ID).
			Update("design_status", store.DesignFailed).Error)
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+itoa(cb.ID)+"/voice-design", nil, &batch))
		assert.Equal(t, 1, batch.Queued)
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/books/999999/voice-design", nil, nil))
	})
}

func serve(t *testing.T, o api.Options) client {
	t.Helper()
	o.MaxUploadBytes = 10 << 20
	o.Voices = casting.NewCatalog(fakeTTS{})
	o.Voices.Engines = fakeTTS{}
	mux := http.NewServeMux()
	api.NewServer(o).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client{t: t, url: srv.URL, dir: t.TempDir()}
}
