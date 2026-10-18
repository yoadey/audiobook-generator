package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestExportImport(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		b := waitImported(t, c, created.Id)
		id := itoa(b.Id)
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/books/"+id+"/lexicon",
			map[string]any{"term": "Anna", "replacement": "Ah-na"}, nil))
		assert.True(t, b.CharacterVoices, "on by default")
		var patched api.Book
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"narratorVoice": "bm_george", "characterVoices": false}, &patched))
		assert.False(t, patched.CharacterVoices)

		res, err := http.Get(c.url + "/api/books/" + id + "/export")
		require.NoError(t, err)
		export, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "application/zip", res.Header.Get("Content-Type"))
		assert.Equal(t, `attachment; filename="The Sample.abg.zip"`, res.Header.Get("Content-Disposition"))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/books/999/export", nil, nil))

		// Not an export; an export.
		assert.Equal(t, http.StatusBadRequest, c.uploadTo("/api/import", "x.zip", data, nil))
		var restored api.Book
		require.Equal(t, http.StatusAccepted, c.uploadTo("/api/import", "x.abg.zip", export, &restored))
		assert.Equal(t, "The Sample", restored.Title)
		assert.NotEqual(t, b.Id, restored.Id)
		got := waitImported(t, c, restored.Id)
		assert.Equal(t, api.BookStatusImported, got.Status)
		assert.Equal(t, "bm_george", got.NarratorVoice)
		assert.False(t, got.CharacterVoices)
		assert.Equal(t, b.ChapterCount, got.ChapterCount)
		var lex []api.LexiconEntry
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+itoa(got.Id)+"/lexicon", nil, &lex))
		require.Len(t, lex, 1)
		assert.Equal(t, "Ah-na", lex[0].Replacement)
		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+itoa(got.Id)+"/chapters", nil, &chapters))
		assert.Len(t, chapters.Items, b.ChapterCount)

		// An export from before character voices could be turned off.
		older := withoutBookField(t, export, "CharacterVoices")
		require.Equal(t, http.StatusAccepted, c.uploadTo("/api/import", "y.abg.zip", older, &restored))
		got = waitImported(t, c, restored.Id)
		assert.True(t, got.CharacterVoices)
	})
}

// withoutBookField drops a field of the book from an export's header.
func withoutBookField(t *testing.T, export []byte, field string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(export), int64(len(export)))
	require.NoError(t, err)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		_ = rc.Close()
		if f.Name == "book.json" {
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &raw))
			book := map[string]any{}
			require.NoError(t, json.Unmarshal(raw["book"], &book))
			require.Contains(t, book, field)
			delete(book, field)
			raw["book"], err = json.Marshal(book)
			require.NoError(t, err)
			data, err = json.Marshal(raw)
			require.NoError(t, err)
		}
		w, err := zw.Create(f.Name)
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return out.Bytes()
}
