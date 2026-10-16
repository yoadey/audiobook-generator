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
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

type fakeTTS struct{}

func (fakeTTS) BaseURL() string { return "http://kokoro:8880" }
func (fakeTTS) Voices(context.Context) ([]tts.Voice, error) {
	var out []tts.Voice
	for _, id := range []string{"af_heart", "af_bella", "af_nicole", "bf_emma", "am_michael", "am_puck", "bm_george", "jf_alpha"} {
		out = append(out, tts.Voice{ID: id})
	}
	return out, nil
}
func (fakeTTS) Synthesize(_ context.Context, s tts.Speech) ([]byte, error) {
	if s.Format == "mp3" {
		return []byte("ID3fake"), nil
	}
	return make([]byte, 4800), nil
}

type client struct {
	t   *testing.T
	url string
	dir string // data directory
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
	ffmpeg, _ := exec.LookPath("ffmpeg")
	runner.Handle(render.JobType, (&render.Renderer{
		Store: s, TTS: fakeTTS{}, Encoder: audio.Encoder{FFmpeg: ffmpeg}, DataDir: dir,
		Concurrency: 2, DefaultVoice: "af_heart", Log: log,
	}).Handler())
	scheduler := &render.Scheduler{Store: s, Jobs: runner, DefaultVoice: "af_heart"}
	pool := llm.NewPool(s, nil, log)
	analyzer := &analysis.Analyzer{Store: s, LLM: pool, Jobs: runner, BatchTokens: analysis.DefaultBatchTokens, Log: log}
	runner.Handle(analysis.JobType, analyzer.Handler())
	voices := casting.NewCatalog(fakeTTS{})
	caster := &casting.Caster{Store: s, LLM: pool, Jobs: runner, Catalog: voices, DefaultVoice: "af_heart", Log: log}
	runner.Handle(casting.JobType, caster.Handler())
	analyzer.OnDone = func(ctx context.Context, bookID int64) {
		if b, err := s.GetBook(ctx, bookID); err == nil {
			_ = caster.Start(ctx, b, false)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runner.Run(ctx); close(done) }()

	mux := http.NewServeMux()
	api.NewServer(api.Options{
		Store: s, TTS: fakeTTS{}, Jobs: runner, Scheduler: scheduler, DefaultVoice: "af_heart",
		LLM: pool, Analyzer: analyzer, Caster: caster, Voices: voices, DataDir: dir, MaxUploadBytes: maxUpload, Log: log,
	}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-done
	})
	return client{t: t, url: srv.URL, dir: dir}
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

func waitAudio(t *testing.T, c client, chapterID int64, want api.AudioStatus) api.ChapterDetail {
	t.Helper()
	var d api.ChapterDetail
	require.Eventually(t, func() bool {
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(chapterID), nil, &d))
		return d.AudioStatus == want
	}, 20*time.Second, 20*time.Millisecond, "chapter %d never reached %s", chapterID, want)
	return d
}

func TestAudioFlow(t *testing.T) {
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
		assert.Equal(t, "af_heart", b.NarratorVoice)
		assert.InDelta(t, 1.0, b.NarratorSpeed, 0.001)
		assert.Equal(t, 3, b.Lookahead)
		id := itoa(b.Id)

		// Voices and previews.
		var voices []api.Voice
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/voices", nil, &voices))
		byID := map[string]api.Voice{}
		for _, v := range voices {
			byID[v.Id] = v
		}
		assert.Equal(t, api.Voice{Id: "af_heart", Language: "en-US", Gender: api.VoiceGenderFemale, Grade: ptr("A"),
			Traits: "warm, expressive, young adult", Available: true, InCatalog: true}, byID["af_heart"])
		assert.Equal(t, "en-GB", byID["bm_george"].Language)
		assert.Equal(t, api.VoiceGenderMale, byID["bm_george"].Gender)
		assert.False(t, byID["am_adam"].Available, "in the catalogue, not on the server")
		assert.Equal(t, api.Voice{Id: "jf_alpha", Language: "ja", Gender: api.VoiceGenderFemale, Available: true}, byID["jf_alpha"])
		assert.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/voices/preview", map[string]any{"voice": "bm_george"}, nil))
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/voices/preview", map[string]any{"voice": "x; rm -rf"}, nil))

		// Narrator settings.
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/books/"+id, map[string]any{"narratorVoice": "nope"}, nil))
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/books/"+id, map[string]any{"narratorSpeed": 3}, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id,
			map[string]any{"narratorVoice": "bf_emma(2)+af_bella(1)", "narratorSpeed": 1.1, "lookahead": 1}, &b))
		assert.Equal(t, "bf_emma(2)+af_bella(1)", b.NarratorVoice)
		assert.Equal(t, 1, b.Lookahead)

		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters", nil, &chapters))
		ch1, ch2 := chapters.Items[1], chapters.Items[2]
		assert.Equal(t, api.AudioStatusNone, ch1.AudioStatus)
		assert.Equal(t, http.StatusNotFound, c.do(http.MethodGet, "/api/chapters/"+itoa(ch1.Id)+"/audio", nil, "", nil))

		// Render one chapter explicitly.
		var queued api.Chapter
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/render", nil, &queued))
		assert.Contains(t, []api.AudioStatus{api.AudioStatusQueued, api.AudioStatusRendering, api.AudioStatusReady}, queued.AudioStatus)
		d := waitAudio(t, c, ch1.Id, api.AudioStatusReady)
		require.NotNil(t, d.AudioDurationMs)
		assert.Positive(t, *d.AudioDurationMs)
		assert.False(t, d.AudioStale)

		// The audio supports range requests.
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.url+"/api/chapters/"+itoa(ch1.Id)+"/audio", nil)
		req.Header.Set("Range", "bytes=0-99")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Equal(t, "audio/mp4", resp.Header.Get("Content-Type"))
		assert.Len(t, body, 100)

		// A narrator change makes the audio stale.
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/books/"+id, map[string]any{"narratorVoice": "bm_george"}, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(ch1.Id), nil, &d))
		assert.True(t, d.AudioStale)

		// Listening to chapter 2 keeps the lookahead window (1) rendered.
		var p api.Progress
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/progress", nil, &p))
		assert.Nil(t, p.ChapterId)
		require.Equal(t, http.StatusOK, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": ch2.Id, "positionMs": 1234}, &p))
		assert.EqualValues(t, 1234, p.PositionMs)
		waitAudio(t, c, ch2.Id, api.AudioStatusReady)
		assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPut, "/api/books/"+id+"/progress",
			map[string]any{"chapterId": 999999, "positionMs": 0}, nil))

		// Bulk render from a chapter.
		var res api.RenderResult
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/render",
			map[string]any{"fromChapterId": ch1.Id, "count": 10}, &res))
		assert.Equal(t, 2, res.Queued, "stale chapter 1 and chapter 3; chapter 2 is current")

		var jobsPage api.JobPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/jobs?bookId="+id, nil, &jobsPage))
		assert.GreaterOrEqual(t, jobsPage.Total, int64(4))
		assert.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/jobs/"+itoa(jobsPage.Items[len(jobsPage.Items)-1].Id)+"/retry", nil, nil),
			"a finished import cannot be retried")

		// Merging drops the audio of both chapters.
		waitAudio(t, c, ch1.Id, api.AudioStatusReady)
		var merged api.Chapter
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/merge-next", nil, &merged))
		assert.Equal(t, api.AudioStatusNone, merged.AudioStatus)
	})
}

func ptr[T any](v T) *T { return &v }

func TestCancelRenderKeepsAudioAndDeleteCleansUp(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		waitImported(t, c, created.Id)
		ids, err := s.ChapterIDs(ctx, created.Id)
		require.NoError(t, err)
		ch := ids[1]

		// Ready audio, then a forced re-render that waits in the queue.
		require.NoError(t, s.SetChapterAudio(ctx, ch, store.ChapterAudio{Path: "x.m4a", Voice: "af_heart@1.00"}))
		require.NoError(t, s.SetAudioStatus(ctx, store.AudioQueued, "", ch))
		j := &store.Job{Type: render.JobType, Queue: render.Queue, BookID: &created.Id, ChapterID: &ch,
			NotBefore: time.Now().Add(time.Hour)}
		require.NoError(t, s.EnqueueJob(ctx, j))
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/jobs/"+itoa(j.ID)+"/cancel", nil, nil))
		got, err := s.GetChapter(ctx, ch)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus, "the existing audio stays playable")

		// Deleting the book removes the render cache of its chapters too.
		cache := render.CacheDir(c.dir, ch)
		require.NoError(t, os.MkdirAll(cache, 0o750))
		require.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/books/"+itoa(created.Id), nil, nil))
		assert.NoDirExists(t, cache)
	})
}
