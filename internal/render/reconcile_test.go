package render_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

func TestReconcileFiles(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir, b, cs := setup(t, s)
		kept, gone := cs[1], cs[2]
		for _, c := range []store.Chapter{kept, gone} {
			rel := render.AudioPath(b.ID, c.ID, 1)
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o600))
			require.NoError(t, s.SetChapterAudio(ctx, c.ID, store.ChapterAudio{Path: rel, Size: 1}))
		}
		require.NoError(t, os.Remove(filepath.Join(dir, render.AudioPath(b.ID, gone.ID, 1))))
		require.NoError(t, s.QueueDownload(ctx, b.ID, 0))
		_, err := s.FinishDownload(ctx, &store.Download{BookID: b.ID, Path: "books/x/missing.m4b", Size: 5})
		require.NoError(t, err)

		require.NoError(t, render.ReconcileFiles(ctx, s, dir, slog.New(slog.DiscardHandler)))

		c, err := s.GetChapter(ctx, kept.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, c.AudioStatus)
		c, err = s.GetChapter(ctx, gone.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioNone, c.AudioStatus)
		assert.Empty(t, c.AudioPath)
		d, err := s.GetDownload(ctx, b.ID, 0)
		require.NoError(t, err)
		assert.Empty(t, d.Path)
	})
}
