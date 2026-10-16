package render

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// ReconcileFiles forgets audio whose file is gone, e.g. after the database
// was restored from a backup onto a new volume (audio is not backed up) or
// files were removed by hand. Such chapters then count as not rendered, so
// the lookahead, renders and downloads produce them again.
func ReconcileFiles(ctx context.Context, st *store.Store, dataDir string, log *slog.Logger) error {
	missing := func(files []store.AudioFile) ([]int64, error) {
		var ids []int64
		for _, f := range files {
			_, err := os.Stat(filepath.Join(dataDir, f.Path))
			switch {
			case errors.Is(err, os.ErrNotExist):
				ids = append(ids, f.ID)
			case err != nil:
				return nil, err // e.g. the volume is not readable: change nothing
			}
		}
		return ids, nil
	}
	chapters, err := st.ChapterAudioFiles(ctx)
	if err != nil {
		return err
	}
	gone, err := missing(chapters)
	if err != nil {
		return err
	}
	for start := 0; start < len(gone); start += 500 {
		if _, err := st.ClearChapterAudio(ctx, gone[start:min(start+500, len(gone))]...); err != nil {
			return err
		}
	}
	downloads, err := st.DownloadFiles(ctx)
	if err != nil {
		return err
	}
	goneDownloads, err := missing(downloads)
	if err != nil {
		return err
	}
	if err := st.ForgetDownloadFiles(ctx, goneDownloads...); err != nil {
		return err
	}
	if len(gone)+len(goneDownloads) > 0 {
		log.Warn("audio files missing, they will be rendered again",
			"chapters", len(gone), "downloads", len(goneDownloads))
	}
	return nil
}
