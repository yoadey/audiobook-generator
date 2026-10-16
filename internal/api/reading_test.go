package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// says returns what a voice preview spoke (the fake TTS echoes the text).
func (c client) says(in map[string]any) string {
	c.t.Helper()
	data, err := json.Marshal(in)
	require.NoError(c.t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.url+"/api/voices/preview",
		bytes.NewReader(data))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	require.Equal(c.t, http.StatusOK, resp.StatusCode, string(body))
	return strings.TrimPrefix(string(body), "ID3")
}

func TestPreviewTexts(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		b := waitImported(t, c, created.Id)
		id := itoa(b.Id)
		assert.Empty(t, b.PreviewText)

		// The built-in sentence until one is set.
		var st api.AppSettings
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/settings", nil, &st))
		assert.Empty(t, st.PreviewText)
		builtin := st.DefaultPreviewText
		require.NotEmpty(t, builtin)
		assert.Equal(t, builtin, c.says(map[string]any{"voice": "af_heart"}))

		// The instance's text, then the book's; a request's own text wins.
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/settings",
			map[string]any{"previewText": "  Call me Ishmael.  "}, &st))
		assert.Equal(t, "Call me Ishmael.", st.PreviewText)
		assert.Equal(t, "Call me Ishmael.", c.says(map[string]any{"voice": "af_heart"}))
		assert.Equal(t, "Call me Ishmael.", c.says(map[string]any{"voice": "af_heart", "bookId": b.Id}))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"previewText": "Anna waved."}, &b))
		assert.Equal(t, "Anna waved.", b.PreviewText)
		require.NoError(t, s.SaveLexiconEntry(context.Background(),
			&store.LexiconEntry{BookID: b.Id, Term: "Anna", Replacement: "Ah-na"}))
		assert.Equal(t, "Ah-na waved.", c.says(map[string]any{"voice": "af_heart", "bookId": b.Id}),
			"the book's lexicon applies")
		assert.Equal(t, "Call me Ishmael.", c.says(map[string]any{"voice": "af_heart"}), "other previews")
		assert.Equal(t, "Hi.", c.says(map[string]any{"voice": "af_heart", "bookId": b.Id, "text": "Hi."}))
		assert.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/voices/preview",
			map[string]any{"voice": "af_heart", "bookId": 999999}, nil), "an unknown book is no error")

		// Too long; empty resets to the next level.
		long := strings.Repeat("a", 501)
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/settings",
			map[string]any{"previewText": long}, nil))
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"previewText": long}, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"previewText": ""}, &b))
		assert.Equal(t, "Call me Ishmael.", c.says(map[string]any{"voice": "af_heart", "bookId": b.Id}))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/settings", map[string]any{"previewText": ""}, &st))
		assert.Equal(t, builtin, c.says(map[string]any{"voice": "af_heart"}))

		// A character's own sample text.
		anna := &store.Character{BookID: b.Id, Name: "Anna", Gender: store.GenderFemale, Importance: 3}
		require.NoError(t, s.CreateCharacter(context.Background(), anna))
		var ch api.CharacterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/characters/"+itoa(anna.ID),
			map[string]any{"previewText": " I never said that. "}, &ch))
		assert.Equal(t, "I never said that.", ch.PreviewText)
		assert.False(t, ch.UserEdited, "a sample text is no correction of the profile")
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/characters/"+itoa(anna.ID),
			map[string]any{"previewText": long}, nil))
		var page api.CharacterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/characters", nil, &page))
		require.Len(t, page.Items, 1)
		assert.Equal(t, "I never said that.", page.Items[0].PreviewText)
	})
}

func TestReadingAlong(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		b := waitImported(t, c, created.Id)
		id := itoa(b.Id)
		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters", nil, &chapters))
		ch1, ch2 := chapters.Items[1], chapters.Items[2]
		path := "/api/chapters/" + itoa(ch1.Id) + "/timings"
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, path, nil, nil), "no audio yet")

		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/render", nil, nil))
		d := waitAudio(t, c, ch1.Id, api.AudioStatusReady)
		var tm api.ChapterTimings
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, path, nil, &tm))
		assert.True(t, tm.Exact)
		assert.Equal(t, *d.AudioVersion, tm.Version)
		require.Len(t, tm.Cues, 8)
		var segIDs []int64
		for _, p := range d.Paragraphs {
			for _, sg := range p.Segments {
				segIDs = append(segIDs, sg.Id)
			}
		}
		for i, q := range tm.Cues {
			assert.Contains(t, segIDs, q.SegmentId)
			assert.Greater(t, q.EndMs, q.StartMs)
			if i > 0 {
				assert.Greater(t, q.StartMs, tm.Cues[i-1].StartMs)
			}
		}
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, path+"?v="+*d.AudioVersion, nil, &tm))
		assert.True(t, tm.Exact)

		// The reading position: a segment of the chapter.
		var p api.Progress
		require.Equal(t, http.StatusOK, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": ch1.Id, "positionMs": tm.Cues[3].StartMs, "segmentId": tm.Cues[3].SegmentId}, &p))
		require.NotNil(t, p.SegmentId)
		assert.Equal(t, tm.Cues[3].SegmentId, *p.SegmentId)
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/progress", nil, &p))
		assert.Equal(t, tm.Cues[3].SegmentId, *p.SegmentId)
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": ch2.Id, "positionMs": 0, "segmentId": tm.Cues[3].SegmentId}, nil),
			"a segment of another chapter")
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": ch1.Id, "positionMs": 0, "segmentId": 99999999}, nil))
		// Listening on without a segment clears it.
		var listened api.Progress
		require.Equal(t, http.StatusOK, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": ch1.Id, "positionMs": 10}, &listened))
		assert.Nil(t, listened.SegmentId)
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/progress", nil, &listened))
		assert.Nil(t, listened.SegmentId)
	})
}
