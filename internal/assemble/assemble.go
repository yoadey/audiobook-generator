// Package assemble joins a volume's rendered chapters into one M4B file
// with chapter markers (or the whole book, when it has no volumes). Missing
// or outdated chapters are rendered first, behind everything a listener
// waits for.
package assemble

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType assembles one download; the payload is the volume ID (0: the
// whole book).
const JobType = "assemble_download"

// Queue keeps assemblies from holding up rendering.
const Queue = "assemble"

// DefaultWaitInterval is how often a job waiting for chapters looks again.
const DefaultWaitInterval = time.Minute

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Assembler builds the downloads.
type Assembler struct {
	Store     *store.Store
	Jobs      Enqueuer
	Scheduler *render.Scheduler
	FFmpeg    string
	FFprobe   string
	DataDir   string
	Log       *slog.Logger
	// WaitInterval overrides DefaultWaitInterval.
	WaitInterval time.Duration

	mu sync.Mutex // makes "no job pending → enqueue one" atomic
}

// Part is the content of one download.
type Part struct {
	VolumeID int64 // 0: the whole book
	Title    string
	Chapters []store.Chapter // narrated chapters in reading order
}

// Parts splits a book into its downloads: one per volume, or one for the
// whole book without volumes. Chapters outside every volume (front matter)
// belong to the volume around them: the previous one, else the first.
func Parts(b *store.Book, chapters []store.Chapter, vols []store.VolumeStats) []Part {
	if len(vols) == 0 {
		p := Part{Title: b.Title}
		for _, c := range chapters {
			if !c.Excluded {
				p.Chapters = append(p.Chapters, c)
			}
		}
		return []Part{p}
	}
	parts := make([]Part, len(vols))
	index := make(map[int64]int, len(vols))
	for i, v := range vols {
		parts[i] = Part{VolumeID: v.ID, Title: v.Title}
		index[v.ID] = i
	}
	cur := -1
	var leading []store.Chapter
	for _, c := range chapters {
		if c.VolumeID != nil {
			if i, ok := index[*c.VolumeID]; ok {
				cur = i
			}
		}
		if c.Excluded {
			continue
		}
		switch {
		case cur >= 0:
			parts[cur].Chapters = append(parts[cur].Chapters, c)
		default:
			leading = append(leading, c)
		}
	}
	first := 0
	for i := range parts {
		if len(parts[i].Chapters) > 0 {
			first = i
			break
		}
	}
	parts[first].Chapters = append(leading, parts[first].Chapters...)
	return parts
}

// SourceKey identifies the audio a part would be assembled from.
func SourceKey(p *Part) string {
	h := sha256.New()
	for _, c := range p.Chapters {
		_, _ = fmt.Fprintf(h, "%d:%s\n", c.ID, c.AudioPath)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// ErrNoPart means the volume does not exist (any more) or has no narrated
// chapters.
var ErrNoPart = errors.New("no such volume or no narrated chapters")

// load returns the book's part for a volume.
func (a *Assembler) load(ctx context.Context, bookID, volumeID int64) (*store.Book, *Part, error) {
	b, err := a.Store.GetBook(ctx, bookID)
	if err != nil {
		return nil, nil, err
	}
	chapters, _, err := a.Store.ListChapters(ctx, bookID, store.ChapterFilter{}, 0, 1<<30)
	if err != nil {
		return nil, nil, err
	}
	vols, err := a.Store.ListVolumes(ctx, bookID)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range Parts(b, chapters, vols) {
		if p.VolumeID == volumeID && len(p.Chapters) > 0 {
			return b, &p, nil
		}
	}
	return b, nil, ErrNoPart
}

// Start queues the download of a volume unless it is already pending.
func (a *Assembler) Start(ctx context.Context, bookID, volumeID int64) error {
	if _, _, err := a.load(ctx, bookID, volumeID); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	pending, err := a.Pending(ctx, bookID)
	if err != nil {
		return err
	}
	if pending[volumeID] != nil {
		return nil
	}
	if err := a.Store.QueueDownload(ctx, bookID, volumeID); err != nil {
		return err
	}
	return a.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &bookID, Payload: strconv.FormatInt(volumeID, 10), MaxAttempts: 3,
	})
}

// Pending returns the queued or running assembly jobs of a book by volume.
func (a *Assembler) Pending(ctx context.Context, bookID int64) (map[int64]*store.Job, error) {
	js, err := a.Store.PendingBookJobs(ctx, JobType, bookID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*store.Job, len(js))
	for i := range js {
		if v, err := strconv.ParseInt(js[i].Payload, 10, 64); err == nil {
			out[v] = &js[i]
		}
	}
	return out, nil
}

// Handler is the job handler for JobType.
func (a *Assembler) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			volumeID, err := strconv.ParseInt(j.Payload, 10, 64)
			if err != nil || j.BookID == nil {
				return jobs.Permanent(fmt.Errorf("bad assembly job payload %q", j.Payload))
			}
			return a.run(ctx, *j.BookID, volumeID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			volumeID, perr := strconv.ParseInt(j.Payload, 10, 64)
			if perr != nil || j.BookID == nil {
				return
			}
			if serr := a.Store.SetDownloadStatus(ctx, *j.BookID, volumeID, store.DownloadFailed, err.Error()); serr != nil &&
				!errors.Is(serr, store.ErrNotFound) {
				a.Log.Error("mark download failed", "book", *j.BookID, "volume", volumeID, "err", serr)
			}
		},
	}
}

func (a *Assembler) run(ctx context.Context, bookID, volumeID int64, progress func(float64)) error {
	b, part, err := a.load(ctx, bookID, volumeID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNoPart) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	waiting, err := a.ensureRendered(ctx, b, part)
	if err != nil {
		return err
	}
	if waiting > 0 {
		if err := a.Store.SetDownloadStatus(ctx, bookID, volumeID, store.DownloadQueued, ""); err != nil {
			return err
		}
		return jobs.Defer(time.Now().Add(cmp.Or(a.WaitInterval, DefaultWaitInterval)), fmt.Sprintf("waiting for %d chapters to be rendered", waiting))
	}
	if err := a.Store.SetDownloadStatus(ctx, bookID, volumeID, store.DownloadAssembling, ""); err != nil {
		return err
	}
	return a.assemble(ctx, b, part, progress)
}

// ensureRendered queues the part's missing and outdated chapters and
// returns how many are not ready yet. A chapter that cannot be rendered
// fails the download; outdated audio whose re-render failed is used.
func (a *Assembler) ensureRendered(ctx context.Context, b *store.Book, p *Part) (int, error) {
	pending, err := a.Store.PendingRenderChapters(ctx, render.JobType, b.ID)
	if err != nil {
		return 0, err
	}
	waiting := 0
	for i := range p.Chapters {
		c := &p.Chapters[i]
		switch {
		case pending[c.ID]:
			waiting++
		case c.AudioPath == "" && c.AudioStatus == store.AudioFailed:
			return 0, jobs.Permanent(fmt.Errorf("chapter %q could not be rendered: %s", c.Title, c.AudioError))
		case c.AudioPath == "" || (a.Scheduler.Stale(b, c) && c.AudioError == ""):
			if _, err := a.Scheduler.RenderBackground(ctx, b, c); err != nil {
				return 0, err
			}
			waiting++
		}
	}
	return waiting, nil
}

// Path of an assembled file version.
func Path(bookID, volumeID int64, version string) string {
	return filepath.Join(importer.BookDir(bookID), "downloads", fmt.Sprintf("%d-%s.m4b", volumeID, version))
}

func (a *Assembler) assemble(ctx context.Context, b *store.Book, p *Part, progress func(float64)) error {
	tmpDir, err := os.MkdirTemp("", "abg-assemble-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// The concat demuxer starts each file where the previous one's
	// container duration ends, so the markers use the probed durations.
	var list, meta strings.Builder
	meta.WriteString(";FFMETADATA1\n")
	writeMeta(&meta, "title", p.Title)
	writeMeta(&meta, "album", b.Title)
	writeMeta(&meta, "artist", b.Author)
	writeMeta(&meta, "genre", "Audiobook")
	var startMs int64
	for i := range p.Chapters {
		c := &p.Chapters[i]
		file := filepath.Join(a.DataDir, c.AudioPath)
		d, err := a.probe(ctx, file)
		if err != nil {
			return fmt.Errorf("chapter %q: %w", c.Title, err)
		}
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(file, "'", `'\''`))
		end := startMs + d.Milliseconds()
		fmt.Fprintf(&meta, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\n", startMs, end)
		writeMeta(&meta, "title", c.Title)
		startMs = end
		progress(0.2 * float64(i+1) / float64(len(p.Chapters)))
	}
	listFile := filepath.Join(tmpDir, "list.txt")
	metaFile := filepath.Join(tmpDir, "metadata.txt")
	if err := os.WriteFile(listFile, []byte(list.String()), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(metaFile, []byte(meta.String()), 0o600); err != nil {
		return err
	}

	rel := Path(b.ID, p.VolumeID, strconv.FormatInt(time.Now().UnixNano(), 36))
	out := filepath.Join(a.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	// Written next to the target (the files are too large for /tmp).
	tmp := out + ".tmp"
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-progress", "pipe:1",
		"-f", "concat", "-safe", "0", "-i", listFile,
		"-i", metaFile,
	}
	// The cover as artwork, if ffmpeg can read it (a broken cover must not
	// fail the download).
	cover := ""
	if b.CoverPath != "" && (b.CoverMediaType == "image/jpeg" || b.CoverMediaType == "image/png") {
		if f := filepath.Join(a.DataDir, b.CoverPath); a.readable(ctx, f) {
			cover = f
			args = append(args, "-i", cover)
		}
	}
	args = append(args, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1", "-c:a", "copy")
	if cover != "" {
		args = append(args, "-map", "2:v", "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", tmp)
	if err := a.ffmpeg(ctx, args, startMs, func(f float64) { progress(0.2 + 0.8*f) }); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	old, err := a.Store.FinishDownload(ctx, &store.Download{
		BookID: b.ID, VolumeID: p.VolumeID, Path: rel, Size: st.Size(), DurationMs: startMs, SourceKey: SourceKey(p),
	})
	if err != nil {
		_ = os.Remove(out)
		if errors.Is(err, store.ErrNotFound) {
			return jobs.Permanent(err)
		}
		return err
	}
	if old != "" {
		if err := os.Remove(filepath.Join(a.DataDir, old)); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.Log.Warn("remove previous download", "path", old, "err", err)
		}
	}
	return nil
}

// writeMeta writes an ffmetadata key, escaping its value.
func writeMeta(w *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	r := strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", `\`+"\n")
	w.WriteString(key + "=" + r.Replace(value) + "\n")
}

// probe returns a file's container duration.
func (a *Assembler) probe(ctx context.Context, file string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, a.FFprobe, //nolint:gosec // fixed binary, arguments are not shell-interpreted
		"-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("ffprobe: no duration in %q", bytes.TrimSpace(out))
	}
	return time.Duration(sec * float64(time.Second)), nil
}

// readable reports whether ffprobe finds a picture with dimensions in a
// file (a broken image probes without error but with 0x0).
func (a *Assembler) readable(ctx context.Context, file string) bool {
	out, err := exec.CommandContext(ctx, a.FFprobe, //nolint:gosec // fixed binary, arguments are not shell-interpreted
		"-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", file).Output()
	if err != nil {
		return false
	}
	w, h, ok := strings.Cut(strings.TrimSpace(string(out)), ",")
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	return ok && err1 == nil && err2 == nil && wi > 0 && hi > 0
}

// ffmpeg runs ffmpeg and reports its progress against totalMs.
func (a *Assembler) ffmpeg(ctx context.Context, args []string, totalMs int64, progress func(float64)) error {
	cmd := exec.CommandContext(ctx, a.FFmpeg, args...) //nolint:gosec // fixed binary, arguments are not shell-interpreted
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "out_time_us="); ok && totalMs > 0 {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil {
				progress(min(max(float64(us/1000)/float64(totalMs), 0), 1))
			}
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}
