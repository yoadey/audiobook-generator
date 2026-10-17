package render_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestEstimateTimings(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		_, _, cs := setup(t, s)
		ch := cs[1]
		paras, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)

		tm := render.EstimateTimings(&ch, paras, segs, 60*time.Second)
		require.Len(t, tm.Cues, 8)
		assert.Equal(t, int64(300), tm.Cues[0].StartMs)
		for i, q := range tm.Cues {
			assert.Greater(t, q.EndMs, q.StartMs)
			if i > 0 {
				assert.GreaterOrEqual(t, q.StartMs, tm.Cues[i-1].EndMs)
			}
		}
		// Speech and pauses fill the audio up to its trailing pause.
		last := tm.Cues[len(tm.Cues)-1].EndMs
		assert.InDelta(t, 60000-1500, last, 5)
		// Longer text, longer cue.
		long, short := tm.Cues[7], tm.Cues[0] // "Later that day, nothing happened." vs "Chapter 1"
		assert.Greater(t, long.EndMs-long.StartMs, short.EndMs-short.StartMs)

		// Audio shorter than its pauses still yields ordered cues inside it.
		tm = render.EstimateTimings(&ch, paras, segs, time.Second)
		for i, q := range tm.Cues {
			assert.LessOrEqual(t, q.EndMs, int64(1000))
			if i > 0 {
				assert.GreaterOrEqual(t, q.StartMs, tm.Cues[i-1].EndMs)
			}
		}
	})
}

func TestLoadTimings(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		ch := cs[1]
		cur := render.AudioPath(b.ID, ch.ID, 2)
		old := render.AudioPath(b.ID, ch.ID, 1)
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, cur)), 0o750))
		for _, p := range []string{cur, old} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, p), []byte("audio"), 0o600))
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, render.TimingsPath(old)),
			[]byte(`{"cues":[{"s":7,"a":300,"b":900}]}`), 0o600))
		require.NoError(t, s.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{Path: cur, DurationMs: 30000}))
		c, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)

		// The kept version's recorded timings.
		v := render.AudioVersion(old)
		tm, path, exact, err := render.LoadTimings(ctx, s, dir, c, &v)
		require.NoError(t, err)
		assert.True(t, exact)
		assert.Equal(t, old, path)
		assert.Equal(t, []render.Cue{{SegmentID: 7, StartMs: 300, EndMs: 900}}, tm.Cues)

		// The current render has none: estimated; unknown versions fall
		// back to the current one.
		for _, v := range []*string{nil, ptr("zzz"), ptr("../x")} {
			tm, path, exact, err = render.LoadTimings(ctx, s, dir, c, v)
			require.NoError(t, err)
			assert.False(t, exact)
			assert.Equal(t, cur, path)
			assert.Len(t, tm.Cues, 8)
		}

		// An older render without timings gets the current render's
		// (its own duration is unknown), named by the path.
		older := render.AudioPath(b.ID, ch.ID, 0)
		require.NoError(t, os.WriteFile(filepath.Join(dir, older), []byte("audio"), 0o600))
		v0 := render.AudioVersion(older)
		_, path, exact, err = render.LoadTimings(ctx, s, dir, c, &v0)
		require.NoError(t, err)
		assert.False(t, exact)
		assert.Equal(t, cur, path)

		// All versions of a chapter go at once (not those of chapter 1x).
		other := render.AudioPath(b.ID, ch.ID*10+1, 1)
		require.NoError(t, os.WriteFile(filepath.Join(dir, other), []byte("audio"), 0o600))
		require.NoError(t, render.RemoveVersions(dir, cur))
		for _, p := range []string{cur, old, older, render.TimingsPath(old)} {
			assert.NoFileExists(t, filepath.Join(dir, p))
		}
		assert.FileExists(t, filepath.Join(dir, other))
		require.NoError(t, os.WriteFile(filepath.Join(dir, old), []byte("audio"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, render.TimingsPath(old)), []byte("{}"), 0o600))

		require.NoError(t, render.RemoveAudio(dir, old))
		assert.NoFileExists(t, filepath.Join(dir, old))
		assert.NoFileExists(t, filepath.Join(dir, render.TimingsPath(old)))
		require.NoError(t, render.RemoveAudio(dir, old), "gone files are no error")
	})
}

func ptr[T any](v T) *T { return &v }
