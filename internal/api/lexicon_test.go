package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestLexicon(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)
		ids, err := s.ChapterIDs(ctx, book.Id)
		require.NoError(t, err)
		before, err := s.GetChapter(ctx, ids[1])
		require.NoError(t, err)

		var e api.LexiconEntry
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/books/"+id+"/lexicon",
			map[string]any{"term": "  Anna ", "replacement": "Ah-na"}, &e))
		assert.Equal(t, "Anna", e.Term)
		assert.False(t, e.Phonemes)
		after, err := s.GetChapter(ctx, ids[1])
		require.NoError(t, err)
		assert.Greater(t, after.VoiceRev, before.VoiceRev, "chapters saying Anna are outdated")
		other, err := s.GetChapter(ctx, ids[3])
		require.NoError(t, err)
		assert.Zero(t, other.VoiceRev, "chapter 3 does not mention her")

		revBefore, err := s.GetChapter(ctx, ids[1])
		require.NoError(t, err)
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/books/"+id+"/lexicon",
			map[string]any{"term": "ANNA", "replacement": "x"}, nil))
		revAfter, err := s.GetChapter(ctx, ids[1])
		require.NoError(t, err)
		assert.Equal(t, revBefore.VoiceRev, revAfter.VoiceRev, "a refused entry outdates nothing")
		// Case is folded beyond ASCII on both databases.
		var emile api.LexiconEntry
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/books/"+id+"/lexicon",
			map[string]any{"term": "Émile", "replacement": "Ay-meel"}, &emile))
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/books/"+id+"/lexicon",
			map[string]any{"term": "éMILE", "replacement": "x"}, nil))
		require.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/lexicon/"+itoa(emile.Id), nil, nil))
		for _, bad := range []map[string]any{
			{"term": "!!", "replacement": "x"}, {"term": "Tom", "replacement": "[x]"},
			{"term": "Tom", "replacement": "t/o/m", "phonemes": true}, {"term": "Tom", "replacement": " "},
		} {
			assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/books/"+id+"/lexicon", bad, nil), bad)
		}
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/lexicon/"+itoa(e.Id),
			map[string]any{"replacement": "ˈænə", "phonemes": true}, &e))
		assert.True(t, e.Phonemes)
		var list []api.LexiconEntry
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/lexicon", nil, &list))
		require.Len(t, list, 1)
		assert.Equal(t, "ˈænə", list[0].Replacement)

		// Previews apply the lexicon of the given book.
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/voices/preview",
			map[string]any{"voice": "af_heart", "text": "Anna", "bookId": book.Id}, nil))

		require.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/lexicon/"+itoa(e.Id), nil, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodDelete, "/api/lexicon/"+itoa(e.Id), nil, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/books/999999/lexicon", nil, nil))
	})
}

func TestPreparationAndSuggestions(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)
		ids, err := s.ChapterIDs(ctx, book.Id)
		require.NoError(t, err)
		assert.False(t, book.PrepareChapters)

		// Not while the book does not ask for it.
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+itoa(ids[1])+"/preparation",
			nil, nil))

		// Turning the preparation on outdates all of the book's audio,
		// chapters without quotations too.
		revs := func() []int {
			var out []int
			for _, cid := range ids {
				ch, err := s.GetChapter(ctx, cid)
				require.NoError(t, err)
				out = append(out, ch.VoiceRev)
			}
			return out
		}
		before := revs()
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"prepareChapters": true}, &book))
		assert.True(t, book.PrepareChapters)
		for i, r := range revs() {
			assert.Greater(t, r, before[i], "chapter %d", i)
		}

		var prep api.ChapterPreparation
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/chapters/"+itoa(ids[1])+"/preparation",
			nil, &prep))
		assert.Equal(t, api.PrepStatusQueued, prep.Status)
		require.NotNil(t, prep.Job)
		// Without an LLM provider the job waits (deferred).
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+itoa(ids[1])+"/preparation",
			map[string]any{"force": true}, nil), "busy")
		var detail api.ChapterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(ids[1]), nil, &detail))
		require.NotNil(t, detail.Preparation)
		assert.Contains(t, []api.PrepStatus{api.PrepStatusQueued, api.PrepStatusRunning}, detail.PrepStatus)

		// A suggestion applies once accepted; deciding about it outdates
		// nothing until then.
		e := &store.LexiconEntry{BookID: book.Id, Term: "Anna", Replacement: "ah-nah", Status: store.LexiconSuggested,
			Note: "a name"}
		require.NoError(t, s.SaveLexiconEntry(ctx, e))
		var list []api.LexiconEntry
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/lexicon", nil, &list))
		require.Len(t, list, 1)
		assert.Equal(t, api.LexiconStatusSuggested, list[0].Status)
		assert.Equal(t, "a name", *list[0].Note)
		rev := func() int {
			ch, err := s.GetChapter(ctx, ids[1])
			require.NoError(t, err)
			return ch.VoiceRev
		}
		r0 := rev()
		var got api.LexiconEntry
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/lexicon/"+itoa(e.ID),
			map[string]any{"status": "dismissed"}, &got))
		assert.Equal(t, api.LexiconStatusDismissed, got.Status)
		assert.Equal(t, r0, rev(), "dismissing a suggestion changes nothing audible")
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/lexicon/"+itoa(e.ID),
			map[string]any{"status": "active"}, &got))
		assert.Equal(t, api.LexiconStatusActive, got.Status)
		assert.Greater(t, rev(), r0, "accepting it does")
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/lexicon/"+itoa(e.ID),
			map[string]any{"status": "maybe"}, nil))
	})
}
