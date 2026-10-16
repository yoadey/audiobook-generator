package feed_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/feed"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

var secret = []byte("0123456789abcdef-secret")

func setup(t *testing.T, s *store.Store) (string, *store.Book, []store.Chapter) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	data, err := epubtest.Sample()
	require.NoError(t, err)
	b := &store.Book{Title: "The Sample", Author: "A. Writer", Status: store.BookImporting}
	require.NoError(t, s.CreateBook(ctx, b))
	b.EpubPath = importer.SourcePath(b.ID)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, importer.BookDir(b.ID)), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, b.EpubPath), data, 0o600))
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"EpubPath": b.EpubPath}))
	im := &importer.Importer{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}
	require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
	b, err = s.GetBook(ctx, b.ID)
	require.NoError(t, err)
	cs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
	require.NoError(t, err)
	return dir, b, cs
}

// writeAudio stores a fake rendered version of a chapter.
func writeAudio(t *testing.T, s *store.Store, dir string, c store.Chapter, version int64, body string) string {
	t.Helper()
	rel := render.AudioPath(c.BookID, c.ID, version)
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600))
	require.NoError(t, s.SetChapterAudio(context.Background(), c.ID, store.ChapterAudio{
		Path: rel, DurationMs: 61_400, Size: int64(len(body)),
	}))
	return rel
}

type rssDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title     string `xml:"title"`
			GUID      string `xml:"guid"`
			Duration  string `xml:"duration"`
			Episode   int    `xml:"episode"`
			Season    int    `xml:"season"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Transcript struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"transcript"`
		} `xml:"item"`
	} `xml:"channel"`
}

func get(t *testing.T, h http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func TestFeed(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		dir, b, cs := setup(t, s)
		writeAudio(t, s, dir, cs[1], 1, "first")
		mux := http.NewServeMux()
		(&feed.Handler{Store: s, Secret: secret, DataDir: dir, Log: slog.New(slog.DiscardHandler)}).Register(mux)

		path := feed.Path(secret, b.ID)
		require.NotEmpty(t, path)
		w := get(t, mux, "https://audio.example.org"+path)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Header().Get("Content-Type"), "rss+xml")

		var doc rssDoc
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &doc))
		assert.Equal(t, "The Sample", doc.Channel.Title)
		// Only chapters with audio are episodes.
		require.Len(t, doc.Channel.Items, 1)
		it := doc.Channel.Items[0]
		assert.Equal(t, cs[1].Title, it.Title)
		assert.Equal(t, "61", it.Duration)
		assert.Equal(t, cs[1].Idx+1, it.Episode)
		assert.Equal(t, 1, it.Season) // the first volume
		assert.Equal(t, int64(5), it.Enclosure.Length)
		assert.True(t, strings.HasPrefix(it.Enclosure.URL, "https://audio.example.org/feed/"), it.Enclosure.URL)

		// The enclosure works without a session.
		enc := strings.TrimPrefix(it.Enclosure.URL, "https://audio.example.org")
		w = get(t, mux, enc)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "first", w.Body.String())
		assert.Equal(t, "audio/mp4", w.Header().Get("Content-Type"))

		// After a re-render the old URL serves the kept version, the feed
		// lists the new one under the same GUID.
		writeAudio(t, s, dir, cs[1], 2, "second")
		w = get(t, mux, enc)
		assert.Equal(t, "first", w.Body.String())
		w = get(t, mux, path)
		var doc2 rssDoc
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &doc2))
		require.Len(t, doc2.Channel.Items, 1)
		assert.Equal(t, it.GUID, doc2.Channel.Items[0].GUID)
		assert.NotEqual(t, it.Enclosure.URL, doc2.Channel.Items[0].Enclosure.URL)
		// A version that is gone falls back to the current audio.
		require.NoError(t, os.Remove(filepath.Join(dir, render.AudioPath(b.ID, cs[1].ID, 1))))
		w = get(t, mux, enc)
		assert.Equal(t, "second", w.Body.String())

		// Range requests (podcast apps resume downloads).
		req := httptest.NewRequest(http.MethodGet, enc, nil)
		req.Header.Set("Range", "bytes=1-2")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		assert.Equal(t, http.StatusPartialContent, rw.Code)
		body, _ := io.ReadAll(rw.Body)
		assert.Equal(t, "ec", string(body))
	})
}

func TestFeedTranscript(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		rel := writeAudio(t, s, dir, cs[1], 1, "first")
		_, segs, err := s.ChapterText(ctx, cs[1].ID)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, render.TimingsPath(rel)), []byte(fmt.Sprintf(
			`{"cues":[{"s":%d,"a":300,"b":1250},{"s":%d,"a":3723400,"b":3724000}]}`, segs[0].ID, segs[1].ID)), 0o600))
		mux := http.NewServeMux()
		(&feed.Handler{Store: s, Secret: secret, DataDir: dir, Log: slog.New(slog.DiscardHandler)}).Register(mux)

		w := get(t, mux, "https://audio.example.org"+feed.Path(secret, b.ID))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `xmlns:podcast="https://podcastindex.org/namespace/1.0"`)
		var doc rssDoc
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &doc))
		require.Len(t, doc.Channel.Items, 1)
		tr := doc.Channel.Items[0].Transcript
		assert.Equal(t, "text/vtt", tr.Type)
		assert.Equal(t, strings.TrimSuffix(doc.Channel.Items[0].Enclosure.URL, ".m4a")+".vtt", tr.URL)

		w = get(t, mux, strings.TrimPrefix(tr.URL, "https://audio.example.org"))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/vtt; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Equal(t, "WEBVTT\n\n1\n00:00:00.300 --> 00:00:01.250\n"+segs[0].Text+
			"\n\n2\n01:02:03.400 --> 01:02:04.000\n"+segs[1].Text+"\n", w.Body.String())

		// Without recorded timings the transcript is estimated; a wrong
		// token gets nothing.
		require.NoError(t, os.Remove(filepath.Join(dir, render.TimingsPath(rel))))
		w = get(t, mux, strings.TrimPrefix(tr.URL, "https://audio.example.org"))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 8, strings.Count(w.Body.String(), " --> "), w.Body.String())
		w = get(t, mux, fmt.Sprintf("/feed/%s/%d/%d/1.vtt", feed.Token([]byte("other"), b.ID), b.ID, cs[1].ID))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestWebVTTEscapes(t *testing.T) {
	c := &store.Chapter{Title: "One <b>"}
	segs := []store.Segment{{ID: 4, Text: "Fish & chips -->\n\nnext"}}
	out := feed.WebVTT(c, segs, &render.Timings{Cues: []render.Cue{
		{SegmentID: 0, StartMs: 0, EndMs: 500}, {SegmentID: 4, StartMs: 500, EndMs: 900},
		{SegmentID: 9, StartMs: 900, EndMs: 950}, // unknown segment: skipped
	}})
	assert.Equal(t, "WEBVTT\n\n1\n00:00:00.000 --> 00:00:00.500\nOne &lt;b&gt;\n\n"+
		"2\n00:00:00.500 --> 00:00:00.900\nFish &amp; chips --&gt; next\n", string(out))
}

func TestFeedAuthorization(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		dir, b, cs := setup(t, s)
		writeAudio(t, s, dir, cs[1], 1, "first")
		other := &store.Book{Title: "Other", Status: store.BookImported}
		require.NoError(t, s.CreateBook(context.Background(), other))
		mux := http.NewServeMux()
		(&feed.Handler{Store: s, Secret: secret, DataDir: dir, Log: slog.New(slog.DiscardHandler)}).Register(mux)

		token := feed.Token(secret, b.ID)
		otherToken := feed.Token(secret, other.ID)
		ch := cs[1].ID
		for _, url := range []string{
			"/feed/wrong/" + itoa(b.ID) + ".xml",
			"/feed/" + otherToken + "/" + itoa(b.ID) + ".xml",
			"/feed/" + token + "/" + itoa(b.ID) + ".rss",
			// A token only opens its own book's chapters.
			"/feed/" + otherToken + "/" + itoa(other.ID) + "/" + itoa(ch) + "/1.m4a",
			"/feed/" + token + "/" + itoa(b.ID) + "/" + itoa(cs[2].ID) + "/1.m4a", // no audio
			"/feed/" + token + "/" + itoa(b.ID) + "/" + itoa(ch) + "/..%2f..%2fx.m4a",
			"/feed/" + otherToken + "/" + itoa(b.ID) + "/cover",
			"/feed/" + otherToken + "/" + itoa(other.ID) + "/cover", // no cover
		} {
			assert.Equal(t, http.StatusNotFound, get(t, mux, url).Code, url)
		}
		assert.Equal(t, http.StatusOK, get(t, mux, "/feed/"+token+"/"+itoa(b.ID)+"/"+itoa(ch)+"/1.m4a").Code)
		assert.Equal(t, http.StatusOK, get(t, mux, "/feed/"+token+"/"+itoa(b.ID)+"/cover").Code)

		// Without a secret, feeds are off.
		assert.Empty(t, feed.Path(nil, b.ID))
		off := http.NewServeMux()
		(&feed.Handler{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}).Register(off)
		assert.Equal(t, http.StatusNotFound, get(t, off, "/feed/"+feed.Token(nil, b.ID)+"/"+itoa(b.ID)+".xml").Code)
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
