package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

type fakeTTS struct{}

func (fakeTTS) BaseURL() string                             { return "http://kokoro:8880" }
func (fakeTTS) Voices(context.Context) ([]tts.Voice, error) { return nil, nil }

type client struct {
	t   *testing.T
	url string
}

func (c client) do(method, path string, body io.Reader, contentType string, out any) int {
	c.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, c.url+path, body)
	require.NoError(c.t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	if out != nil && len(data) > 0 {
		require.NoError(c.t, json.Unmarshal(data, out), string(data))
	}
	return resp.StatusCode
}

func (c client) json(method, path string, in, out any) int {
	c.t.Helper()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		require.NoError(c.t, err)
		body = bytes.NewReader(data)
	}
	return c.do(method, path, body, "application/json", out)
}

func (c client) upload(name string, data []byte, out any) int {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	require.NoError(c.t, err)
	_, _ = fw.Write(data)
	require.NoError(c.t, mw.Close())
	return c.do(http.MethodPost, "/api/books", &buf, mw.FormDataContentType(), out)
}

func newAPI(t *testing.T, s *store.Store, maxUpload int64) client {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	dir := t.TempDir()
	runner := jobs.New(s, log)
	runner.PollInterval = 20 * time.Millisecond
	runner.Handle(importer.JobType, (&importer.Importer{Store: s, DataDir: dir, Log: log}).Handler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runner.Run(ctx); close(done) }()

	mux := http.NewServeMux()
	api.NewServer(api.Options{
		Store: s, TTS: fakeTTS{}, Jobs: runner, DataDir: dir, MaxUploadBytes: maxUpload, Log: log,
	}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-done
	})
	return client{t: t, url: srv.URL}
}

func waitImported(t *testing.T, c client, id int64) api.Book {
	t.Helper()
	var b api.Book
	require.Eventually(t, func() bool {
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+itoa(id), nil, &b))
		return b.Status != api.BookStatusImporting
	}, 10*time.Second, 20*time.Millisecond)
	return b
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestBookLifecycle(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)

		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		assert.Equal(t, "The Sample", created.Title)
		assert.Equal(t, "A. Writer", created.Author)
		assert.Equal(t, api.BookStatusImporting, created.Status)
		id := itoa(created.Id)

		b := waitImported(t, c, created.Id)
		require.Equal(t, api.BookStatusImported, b.Status, b.Error)
		assert.Equal(t, 4, b.ChapterCount)
		assert.True(t, b.HasCover)

		var page api.BookPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books", nil, &page))
		assert.EqualValues(t, 1, page.Total)

		var jobsPage api.JobPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/jobs?bookId="+id+"&status=done", nil, &jobsPage))
		require.Len(t, jobsPage.Items, 1)
		assert.Equal(t, importer.JobType, jobsPage.Items[0].Type)

		code := c.do(http.MethodGet, "/api/books/"+id+"/cover", nil, "", nil)
		assert.Equal(t, http.StatusOK, code)

		var vols []api.Volume
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/volumes", nil, &vols))
		require.Len(t, vols, 2)

		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters?volumeId="+itoa(vols[0].Id), nil, &chapters))
		require.Len(t, chapters.Items, 2)
		ch1 := chapters.Items[0]
		assert.Equal(t, "Chapter 1", ch1.Title)

		var detail api.ChapterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(ch1.Id), nil, &detail))
		require.NotNil(t, detail.PrevChapterId)
		require.NotNil(t, detail.NextChapterId)
		assert.Equal(t, api.ParagraphKindHeading, detail.Paragraphs[0].Kind)
		assert.Equal(t, "“Good morning,”", detail.Paragraphs[1].Segments[0].Text)
		assert.Equal(t, api.SegmentKindQuote, detail.Paragraphs[1].Segments[0].Kind)

		// Rename and exclude.
		var updated api.Chapter
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/chapters/"+itoa(ch1.Id),
			map[string]any{"title": "  The Morning  ", "excluded": true}, &updated))
		assert.Equal(t, "The Morning", updated.Title)
		assert.True(t, updated.Excluded)
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/chapters/"+itoa(ch1.Id),
			map[string]any{"title": " "}, nil))

		// Merge with Chapter 2, then split it off again.
		var merged api.Chapter
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/merge-next", nil, &merged))
		assert.Equal(t, 4, merged.QuoteCount)
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(ch1.Id), nil, &detail))
		var splitAt int64
		for _, p := range detail.Paragraphs {
			if p.Text == "Chapter 2" {
				splitAt = p.Id
			}
		}
		var split api.Chapter
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/split",
			map[string]any{"paragraphId": splitAt, "title": "Chapter 2"}, &split))
		assert.Equal(t, ch1.Idx+1, split.Idx)
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/split",
			map[string]any{"paragraphId": detail.Paragraphs[0].Id, "title": "x"}, nil))

		var last api.Chapter
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters", nil, &chapters))
		last = chapters.Items[len(chapters.Items)-1]
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+itoa(last.Id)+"/merge-next", nil, nil))

		// Delete.
		assert.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/books/"+id, nil, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/books/"+id, nil, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/chapters/"+itoa(ch1.Id), nil, nil))
	})
}

func TestUploadErrors(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 4096)

		var e api.Error
		assert.Equal(t, http.StatusBadRequest, c.upload("x.epub", []byte("plain text"), &e))
		assert.Contains(t, e.Message, "not a valid EPUB")

		assert.Equal(t, http.StatusRequestEntityTooLarge, c.upload("big.epub", bytes.Repeat([]byte("x"), 8192), &e))
		assert.Contains(t, e.Message, "larger than the limit")

		assert.Equal(t, http.StatusBadRequest,
			c.do(http.MethodPost, "/api/books", bytes.NewReader(nil), "multipart/form-data; boundary=x", nil))

		var page api.BookPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books", nil, &page))
		assert.Zero(t, page.Total, "rejected uploads leave nothing behind")
	})
}

func TestImportFailureMarksBook(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		// A valid package without any text.
		data, err := epubtest.Build(epubtest.Options{Title: "Empty", Docs: []epubtest.Doc{{Name: "a.xhtml"}}})
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("empty.epub", data, &created))
		b := waitImported(t, c, created.Id)
		assert.Equal(t, api.BookStatusFailed, b.Status)
		require.NotNil(t, b.Error)
		assert.Contains(t, *b.Error, "no readable text")
	})
}
