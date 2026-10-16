package render_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// fakeTTS returns 100 ms of silence per request and can fail on demand.
type fakeTTS struct {
	mu     sync.Mutex
	texts  []string
	voices []string
	failAt int32 // 1-based call number to fail (0: never)
	calls  atomic.Int32
	err    error
	onCall func(n int32) // runs before each answer
}

func (f *fakeTTS) Synthesize(_ context.Context, s tts.Speech) ([]byte, error) {
	n := f.calls.Add(1)
	if f.onCall != nil {
		f.onCall(n)
	}
	if f.failAt != 0 && n == f.failAt {
		return nil, f.err
	}
	f.mu.Lock()
	f.texts = append(f.texts, s.Text)
	f.voices = append(f.voices, render.VoiceKey(s.Voice, s.Speed))
	f.mu.Unlock()
	return make([]byte, 4800), nil // 100 ms
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

func ffmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	return p
}

func TestRenderChapter(t *testing.T) {
	ff := ffmpeg(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		ch1 := cs[1] // "Chapter 1"
		fake := &fakeTTS{}
		r := &render.Renderer{
			Store: s, TTS: fake, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: dir,
			Concurrency: 3, DefaultVoice: "af_heart", Log: slog.New(slog.DiscardHandler),
		}
		var progress []float64
		var mu sync.Mutex
		require.NoError(t, r.Render(ctx, ch1.ID, func(p float64) { mu.Lock(); progress = append(progress, p); mu.Unlock() }))

		// Quotes are spoken without their marks; the scene break adds no text.
		assert.ElementsMatch(t, []string{
			"Chapter 1", "Good morning,", "said Anna.", "Is it raining?", "Tom looked outside.",
			"It was raining earlier,", "but it has stopped now.", "Later that day, nothing happened.",
		}, fake.texts)
		assert.Equal(t, "af_heart@1.00", fake.voices[0])
		assert.NotEmpty(t, progress)

		got, err := s.GetChapter(ctx, ch1.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus)
		assert.True(t, strings.HasPrefix(got.AudioPath, filepath.Join(importer.BookDir(b.ID), "audio", strconv.FormatInt(ch1.ID, 10)+"-")),
			got.AudioPath)
		assert.Equal(t, "af_heart@1.00", got.AudioVoice)
		// 8 pieces × 100 ms plus lead-in and pauses.
		assert.Greater(t, got.AudioDurationMs, int64(3000))
		assert.Less(t, got.AudioDurationMs, int64(9000))
		st, err := os.Stat(filepath.Join(dir, got.AudioPath))
		require.NoError(t, err)
		assert.Equal(t, st.Size(), got.AudioSize)
		assert.NoDirExists(t, filepath.Join(dir, "cache", "render", strconv.FormatInt(ch1.ID, 10)), "cache removed")

		// A render again writes a new file and removes the previous one
		// (a listener may still stream it until the switch).
		first := got.AudioPath
		require.NoError(t, r.Render(ctx, ch1.ID, func(float64) {}))
		again, err := s.GetChapter(ctx, ch1.ID)
		require.NoError(t, err)
		assert.NotEqual(t, first, again.AudioPath)
		assert.FileExists(t, filepath.Join(dir, again.AudioPath))
		assert.NoFileExists(t, filepath.Join(dir, first))

		// Chapters without a heading paragraph get their title spoken first
		// (sequentially here, so the order of requests is the spoken order).
		fake.texts = nil
		r.Concurrency = 1
		require.NoError(t, r.Render(ctx, cs[0].ID, func(float64) {}))
		assert.Equal(t, "Copyright", fake.texts[0])
	})
}

func TestRenderResumesFromCache(t *testing.T) {
	ff := ffmpeg(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, _, cs := setup(t, s)
		fake := &fakeTTS{failAt: 4, err: errors.New("kokoro restarting")}
		r := &render.Renderer{
			Store: s, TTS: fake, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: dir,
			Concurrency: 1, DefaultVoice: "af_heart", Log: slog.New(slog.DiscardHandler),
		}
		err := r.Render(ctx, cs[1].ID, func(float64) {})
		require.ErrorContains(t, err, "kokoro restarting")
		var perm jobs.PermanentError
		assert.False(t, errors.As(err, &perm), "connection problems are retried")

		first := fake.calls.Load()
		require.NoError(t, r.Render(ctx, cs[1].ID, func(float64) {}))
		// 8 pieces in total; 3 were cached by the first attempt.
		assert.EqualValues(t, 8+1, fake.calls.Load(), "only missing pieces are synthesized again (first run: %d calls)", first)
	})
}

func TestRenderRejectsBadVoice(t *testing.T) {
	ff := ffmpeg(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		dir, _, cs := setup(t, s)
		fake := &fakeTTS{failAt: 1, err: &tts.HTTPError{Status: 400, Body: "unknown voice"}}
		r := &render.Renderer{
			Store: s, TTS: fake, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: dir,
			Concurrency: 1, DefaultVoice: "xx_nope", Log: slog.New(slog.DiscardHandler),
		}
		err := r.Render(context.Background(), cs[1].ID, func(float64) {})
		var perm jobs.PermanentError
		assert.True(t, errors.As(err, &perm))
	})
}

func TestRenderNoticesTextChanges(t *testing.T) {
	ff := ffmpeg(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		ch := cs[1]
		fake := &fakeTTS{onCall: func(n int32) {
			if n == 1 { // the user merges the next chapter in while it renders
				_, err := s.MergeChapterWithNext(ctx, ch.ID)
				require.NoError(t, err)
			}
		}}
		r := &render.Renderer{
			Store: s, TTS: fake, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: dir,
			Concurrency: 1, DefaultVoice: "af_heart", Log: slog.New(slog.DiscardHandler),
		}
		err := r.Render(ctx, ch.ID, func(float64) {})
		require.ErrorContains(t, err, "text changed")
		var perm jobs.PermanentError
		assert.False(t, errors.As(err, &perm), "rendered again by the next attempt")
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.NotEqual(t, store.AudioReady, got.AudioStatus)
		files, _ := filepath.Glob(filepath.Join(dir, importer.BookDir(b.ID), "audio", strconv.FormatInt(ch.ID, 10)+"-*"))
		assert.Empty(t, files)

		require.NoError(t, r.Render(ctx, ch.ID, func(float64) {}))
		got, err = s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus)
	})
}

func TestRenderOfDeletedBook(t *testing.T) {
	ff := ffmpeg(t)
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		fake := &fakeTTS{onCall: func(n int32) {
			if n == 1 {
				require.NoError(t, s.DeleteBook(ctx, b.ID))
			}
		}}
		r := &render.Renderer{
			Store: s, TTS: fake, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: dir,
			Concurrency: 1, DefaultVoice: "af_heart", Log: slog.New(slog.DiscardHandler),
		}
		err := r.Render(ctx, cs[1].ID, func(float64) {})
		var perm jobs.PermanentError
		require.ErrorAs(t, err, &perm)
		files, _ := filepath.Glob(filepath.Join(dir, importer.BookDir(b.ID), "audio", strconv.FormatInt(cs[1].ID, 10)+"-*"))
		assert.Empty(t, files)
	})
}

func TestFailedRenderCleansUp(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, _, cs := setup(t, s)
		r := &render.Renderer{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}
		cache := render.CacheDir(dir, cs[1].ID)
		require.NoError(t, os.MkdirAll(cache, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(cache, "00000-x.pcm"), make([]byte, 100), 0o600))

		// A long message of multi-byte characters is cut between runes:
		// PostgreSQL rejects invalid UTF-8.
		msg := strings.Repeat("“", 400)
		r.Handler().OnFail(ctx, &store.Job{ChapterID: &cs[1].ID}, errors.New(msg))

		got, err := s.GetChapter(ctx, cs[1].ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioFailed, got.AudioStatus)
		assert.True(t, utf8.ValidString(got.AudioError))
		assert.True(t, strings.HasPrefix(msg, strings.TrimSuffix(got.AudioError, "…")))
		assert.NoDirExists(t, cache)
	})
}
