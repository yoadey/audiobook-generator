package assemble_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/assemble"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func tools(t *testing.T) (string, string) {
	t.Helper()
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	return ff, fp
}

// storeJobs enqueues into the store (the scheduler checks pending jobs there).
type storeJobs struct{ s *store.Store }

func (e storeJobs) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = "test-only" // never claimed by a runner
	return e.s.EnqueueJob(ctx, j)
}

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

// renderFake writes a tone of ms milliseconds as the chapter's current audio.
func renderFake(t *testing.T, s *store.Store, ff, dir string, b *store.Book, c store.Chapter, version int64, ms int) string {
	t.Helper()
	rel := render.AudioPath(b.ID, c.ID, version)
	out := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o750))
	cmd := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=24000:duration="+strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64),
		"-ac", "1", "-c:a", "aac", "-b:a", "64k", out)
	require.NoError(t, cmd.Run())
	st, err := os.Stat(out)
	require.NoError(t, err)
	require.NoError(t, s.SetChapterAudio(context.Background(), c.ID, store.ChapterAudio{
		Path: rel, DurationMs: int64(ms), Size: st.Size(),
		Voice: render.VoiceKey(render.NarratorVoice(b, "af_heart")), VoiceRev: c.VoiceRev,
	}))
	return rel
}

type probeOut struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Chapters []struct {
		StartTime string            `json:"start_time"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
	Streams []struct {
		CodecType   string         `json:"codec_type"`
		Disposition map[string]int `json:"disposition"`
	} `json:"streams"`
}

func probe(t *testing.T, fp, file string) probeOut {
	t.Helper()
	out, err := exec.Command(fp, "-v", "error", "-print_format", "json",
		"-show_format", "-show_chapters", "-show_streams", file).Output()
	require.NoError(t, err)
	var p probeOut
	require.NoError(t, json.Unmarshal(out, &p))
	return p
}

func TestParts(t *testing.T) {
	v1, v2 := int64(10), int64(20)
	b := &store.Book{Title: "Book"}
	ch := func(id int64, vol *int64, excluded bool) store.Chapter {
		return store.Chapter{ID: id, VolumeID: vol, Excluded: excluded}
	}
	ids := func(p assemble.Part) []int64 {
		var out []int64
		for _, c := range p.Chapters {
			out = append(out, c.ID)
		}
		return out
	}

	// Without volumes: the whole book, excluded chapters left out.
	parts := assemble.Parts(b, []store.Chapter{ch(1, nil, true), ch(2, nil, false), ch(3, nil, false)}, nil)
	require.Len(t, parts, 1)
	assert.Equal(t, int64(0), parts[0].VolumeID)
	assert.Equal(t, "Book", parts[0].Title)
	assert.Equal(t, []int64{2, 3}, ids(parts[0]))

	// Front matter joins the first volume, chapters between volumes the
	// previous one.
	vols := []store.VolumeStats{{Volume: store.Volume{ID: v1, Title: "One"}}, {Volume: store.Volume{ID: v2, Title: "Two"}}}
	parts = assemble.Parts(b, []store.Chapter{
		ch(1, nil, false), ch(2, &v1, false), ch(3, nil, false), ch(4, &v2, false), ch(5, &v2, true),
	}, vols)
	require.Len(t, parts, 2)
	assert.Equal(t, []int64{1, 2, 3}, ids(parts[0]))
	assert.Equal(t, []int64{4}, ids(parts[1]))
	assert.Equal(t, "Two", parts[1].Title)
}

func TestAssemble(t *testing.T) {
	ff, fp := tools(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		// A real cover image becomes the artwork.
		cover := filepath.Join(dir, b.CoverPath)
		require.NoError(t, exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
			"-i", "color=c=red:s=32x32", "-frames:v", "1", "-f", "image2", "-c:v", "png", cover).Run())
		sched := &render.Scheduler{Store: s, Jobs: storeJobs{s}, DefaultVoice: "af_heart"}
		a := &assemble.Assembler{
			Store: s, Jobs: storeJobs{s}, Scheduler: sched, FFmpeg: ff, FFprobe: fp, DataDir: dir,
			Log: slog.New(slog.DiscardHandler),
		}
		vols, err := s.ListVolumes(ctx, b.ID)
		require.NoError(t, err)
		vol := vols[0].ID // "Book One": Chapter 1 and 2 (Copyright is excluded)

		require.NoError(t, a.Start(ctx, b.ID, vol))
		require.NoError(t, a.Start(ctx, b.ID, vol)) // no second job
		pending, err := a.Pending(ctx, b.ID)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		job := pending[vol]
		require.NotNil(t, job)
		assert.ErrorIs(t, a.Start(ctx, b.ID, 999), assemble.ErrNoPart)

		run := func() error {
			return a.Handler().Run(ctx, job, func(float64) {})
		}

		// Nothing rendered yet: the chapters are queued behind listening.
		err = run()
		var d jobs.Deferrer
		require.ErrorAs(t, err, &d)
		assert.Contains(t, err.Error(), "waiting for 2 chapters")
		for _, c := range cs[1:3] {
			j, err := s.PendingRenderJob(ctx, render.JobType, c.ID)
			require.NoError(t, err)
			require.NotNil(t, j, c.Title)
			assert.Less(t, j.Priority, 50)
		}
		dl, err := s.GetDownload(ctx, b.ID, vol)
		require.NoError(t, err)
		assert.Equal(t, store.DownloadQueued, dl.Status)

		// Rendered: one file with a marker per chapter.
		for _, c := range cs[1:3] {
			j, _ := s.PendingRenderJob(ctx, render.JobType, c.ID)
			require.NoError(t, s.FinishJob(ctx, j.ID))
			renderFake(t, s, ff, dir, b, c, 1, 1500)
		}
		require.NoError(t, run())
		dl, err = s.GetDownload(ctx, b.ID, vol)
		require.NoError(t, err)
		assert.Equal(t, store.DownloadReady, dl.Status)
		assert.InDelta(t, 3000, dl.DurationMs, 150)
		first := dl.Path
		p := probe(t, fp, filepath.Join(dir, first))
		require.Len(t, p.Chapters, 2)
		assert.Equal(t, "Chapter 1", p.Chapters[0].Tags["title"])
		assert.Equal(t, "Chapter 2", p.Chapters[1].Tags["title"])
		start, _ := strconv.ParseFloat(p.Chapters[1].StartTime, 64)
		assert.InDelta(t, 1.5, start, 0.1)
		assert.Equal(t, "Book One", p.Format.Tags["title"])
		assert.Equal(t, "The Sample", p.Format.Tags["album"])
		var art bool
		for _, st := range p.Streams {
			art = art || st.CodecType == "video" && st.Disposition["attached_pic"] == 1
		}
		assert.True(t, art, "cover as artwork")

		chapters, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		vs, err := s.ListVolumes(ctx, b.ID)
		require.NoError(t, err)
		parts := assemble.Parts(b, chapters, vs)
		assert.Equal(t, assemble.SourceKey(&parts[0]), dl.SourceKey)

		// A new rendering outdates it; assembling again replaces the file.
		renderFake(t, s, ff, dir, b, cs[2], 2, 1000)
		require.NoError(t, run())
		dl, err = s.GetDownload(ctx, b.ID, vol)
		require.NoError(t, err)
		assert.NotEqual(t, first, dl.Path)
		assert.NoFileExists(t, filepath.Join(dir, first))
		assert.InDelta(t, 2500, dl.DurationMs, 150)
	})
}

func TestAssembleFailures(t *testing.T) {
	ff, fp := tools(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		sched := &render.Scheduler{Store: s, Jobs: storeJobs{s}, DefaultVoice: "af_heart"}
		a := &assemble.Assembler{
			Store: s, Jobs: storeJobs{s}, Scheduler: sched, FFmpeg: ff, FFprobe: fp, DataDir: dir,
			Log: slog.New(slog.DiscardHandler),
		}
		vols, err := s.ListVolumes(ctx, b.ID)
		require.NoError(t, err)
		vol := vols[1].ID // "Book Two": Chapter 3
		require.NoError(t, a.Start(ctx, b.ID, vol))
		pending, err := a.Pending(ctx, b.ID)
		require.NoError(t, err)
		job := pending[vol]

		// A chapter that failed to render fails the download for good.
		require.NoError(t, s.SetAudioStatus(ctx, store.AudioFailed, "TTS down", cs[3].ID))
		err = a.Handler().Run(ctx, job, func(float64) {})
		var perm jobs.PermanentError
		require.ErrorAs(t, err, &perm)
		assert.Contains(t, err.Error(), "TTS down")
		a.Handler().OnFail(ctx, job, err)
		dl, err := s.GetDownload(ctx, b.ID, vol)
		require.NoError(t, err)
		assert.Equal(t, store.DownloadFailed, dl.Status)
		assert.Contains(t, dl.Error, "TTS down")

		// Outdated audio whose re-render failed is used as it is.
		renderFake(t, s, ff, dir, b, cs[3], 1, 800)
		require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"NarratorVoice": "am_adam"}))
		require.NoError(t, s.SetAudioStatus(ctx, store.AudioReady, "TTS down", cs[3].ID))
		b, err = s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		err = a.Handler().Run(ctx, job, func(float64) {})
		require.NoError(t, err)

		// A volume that disappeared (re-import) fails for good.
		require.NoError(t, s.ResetBookContent(ctx, b.ID))
		err = a.Handler().Run(ctx, job, func(float64) {})
		require.True(t, errors.As(err, &perm), err)
	})
}
