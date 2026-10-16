package api_test

import (
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestDownloads(t *testing.T) {
	_, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var created api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &created))
		b := waitImported(t, c, created.Id)
		id := itoa(b.Id)

		var ds []api.Download
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/downloads", nil, &ds))
		require.Len(t, ds, 2) // one per volume
		assert.Equal(t, "Book One", ds[0].Title)
		assert.Equal(t, 2, ds[0].ChapterCount)
		assert.Equal(t, 0, ds[0].RenderedCount)
		assert.Equal(t, api.DownloadStatusNone, ds[0].Status)
		assert.False(t, ds[0].HasFile)
		vol := itoa(ds[0].VolumeId)

		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/books/"+id+"/downloads/"+vol+"/file", nil, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/books/"+id+"/downloads/999", nil, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/books/999/downloads/0", nil, nil))
		assert.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/books/999/downloads", nil, nil))

		var d api.Download
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/downloads/"+vol, nil, &d))
		assert.Equal(t, api.DownloadStatusQueued, d.Status)
		require.NotNil(t, d.Job)

		// The missing chapters are rendered, then the file is assembled.
		deadline := time.Now().Add(60 * time.Second)
		for {
			var fresh []api.Download
			require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/downloads", nil, &fresh))
			d = fresh[0]
			if d.Status == api.DownloadStatusReady || d.Status == api.DownloadStatusFailed || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		require.Equal(t, api.DownloadStatusReady, d.Status, "error: %v", d.Error)
		assert.True(t, d.HasFile)
		assert.False(t, d.Outdated)
		assert.Equal(t, 2, d.RenderedCount)
		require.NotNil(t, d.SizeBytes)
		assert.Positive(t, *d.SizeBytes)

		res, err := http.Get(c.url + "/api/books/" + id + "/downloads/" + vol + "/file")
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "audio/mp4", res.Header.Get("Content-Type"))
		assert.Equal(t, `attachment; filename="The Sample - Book One.m4b"`, res.Header.Get("Content-Disposition"))
		body, _ := io.ReadAll(res.Body)
		assert.EqualValues(t, *d.SizeBytes, len(body))

		// Re-rendering a chapter outdates the file.
		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters", nil, &chapters))
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/chapters/"+itoa(chapters.Items[1].Id)+"/render?force=true", nil, nil))
		require.Eventually(t, func() bool {
			var fresh []api.Download
			c.json(http.MethodGet, "/api/books/"+id+"/downloads", nil, &fresh)
			return len(fresh) > 0 && fresh[0].Outdated
		}, 30*time.Second, 50*time.Millisecond)
	})
}
