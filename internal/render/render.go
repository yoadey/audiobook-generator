// Package render turns a chapter's segments into an audio file: every
// segment is synthesized by the TTS server (in parallel, cached on disk so
// a retried job resumes), then the PCM is joined with pauses and encoded.
package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// JobType is the job type of a chapter render.
const JobType = "render_chapter"

// Queue is the job queue renders run on.
const Queue = "render"

// Synthesizer is the TTS server.
type Synthesizer interface {
	Synthesize(ctx context.Context, s tts.Speech) ([]byte, error)
}

// Renderer renders chapters.
type Renderer struct {
	Store   *store.Store
	TTS     Synthesizer
	Encoder audio.Encoder
	DataDir string
	// Concurrency is the number of parallel synthesis requests.
	Concurrency  int
	DefaultVoice string
	Metrics      *Metrics
	Log          *slog.Logger
}

// Pauses between pieces of speech.
const (
	leadIn         = 300 * time.Millisecond
	withinPara     = 150 * time.Millisecond
	betweenParas   = 450 * time.Millisecond
	afterHeading   = time.Second
	sceneBreak     = 1200 * time.Millisecond
	tail           = 1500 * time.Millisecond
	maxSynthErrLen = 300
)

// AudioPath is where a chapter's audio is stored, relative to the data dir.
func AudioPath(bookID, chapterID int64) string {
	return filepath.Join(importer.BookDir(bookID), "audio", strconv.FormatInt(chapterID, 10)+".m4a")
}

// CacheDir holds the synthesized pieces of a chapter while it renders
// (absolute; removed when the render finishes or fails for good).
func CacheDir(dataDir string, chapterID int64) string {
	return filepath.Join(dataDir, "cache", "render", strconv.FormatInt(chapterID, 10))
}

// VoiceKey identifies the narration settings a file was rendered with.
func VoiceKey(voice string, speed float64) string {
	return fmt.Sprintf("%s@%.2f", voice, speed)
}

// NarratorVoice returns the effective narrator voice and speed of a book.
func NarratorVoice(b *store.Book, defaultVoice string) (string, float64) {
	voice, speed := b.NarratorVoice, b.NarratorSpeed
	if voice == "" {
		voice = defaultVoice
	}
	if speed <= 0 {
		speed = 1
	}
	return voice, speed
}

// Handler is the job handler for JobType.
func (r *Renderer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.ChapterID == nil {
				return jobs.Permanent(errors.New("render job without chapter"))
			}
			return r.Render(ctx, *j.ChapterID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.ChapterID == nil {
				return
			}
			if serr := r.Store.SetAudioStatus(ctx, store.AudioFailed, truncate(err.Error(), maxSynthErrLen), *j.ChapterID); serr != nil {
				r.Log.Error("mark audio failed", "chapter", *j.ChapterID, "err", serr)
			}
			if rerr := os.RemoveAll(CacheDir(r.DataDir, *j.ChapterID)); rerr != nil {
				r.Log.Warn("remove render cache", "chapter", *j.ChapterID, "err", rerr)
			}
		},
	}
}

// item is one synthesis request plus the pause that follows it.
type item struct {
	text  string
	pause time.Duration
}

// plan lists what to say, in order.
func plan(ch *store.Chapter, paras []store.Paragraph, segs []store.Segment) []item {
	byPara := map[int64][]store.Segment{}
	for _, s := range segs {
		byPara[s.ParagraphID] = append(byPara[s.ParagraphID], s)
	}
	var items []item
	pauseLast := func(d time.Duration) {
		if len(items) > 0 {
			items[len(items)-1].pause = max(items[len(items)-1].pause, d)
		}
	}
	if len(paras) == 0 || paras[0].Kind != store.ParagraphHeading {
		if t := speakable(ch.Title); t != "" {
			items = append(items, item{t, afterHeading})
		}
	}
	for _, p := range paras {
		switch p.Kind {
		case store.ParagraphSceneBreak:
			pauseLast(sceneBreak)
			continue
		case store.ParagraphHeading:
			for _, s := range byPara[p.ID] {
				if t := speakable(s.Text); t != "" {
					items = append(items, item{t, withinPara})
				}
			}
			pauseLast(afterHeading)
		default:
			for _, s := range byPara[p.ID] {
				if t := speakable(s.Text); t != "" {
					items = append(items, item{t, withinPara})
				}
			}
			pauseLast(betweenParas)
		}
	}
	pauseLast(tail)
	return items
}

// speakable strips quotation marks around a segment (Kokoro would otherwise
// pause oddly) and drops text without anything to say.
func speakable(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "“”‘’\"'")
	s = strings.TrimSpace(s)
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		return ""
	}
	return s
}

// fingerprint identifies the text a render was made from, to notice a
// merge or split of the chapter while it renders.
func fingerprint(paras []store.Paragraph, segs []store.Segment) string {
	h := sha256.New()
	for _, p := range paras {
		_, _ = fmt.Fprintf(h, "p%d:%s:%s\x00", p.ID, p.Kind, p.Text)
	}
	for _, s := range segs {
		_, _ = fmt.Fprintf(h, "s%d:%d:%s\x00", s.ID, s.ParagraphID, s.Text)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cacheKey(voiceKey, text string) string {
	h := sha256.Sum256([]byte(voiceKey + "\x00" + text))
	return hex.EncodeToString(h[:8])
}

// Render synthesizes and encodes one chapter.
func (r *Renderer) Render(ctx context.Context, chapterID int64, progress func(float64)) error {
	ch, err := r.Store.GetChapter(ctx, chapterID)
	if err != nil {
		return jobs.Permanent(err)
	}
	book, err := r.Store.GetBook(ctx, ch.BookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	paras, segs, err := r.Store.ChapterText(ctx, ch.ID)
	if err != nil {
		return err
	}
	if err := r.Store.SetAudioStatus(ctx, store.AudioRendering, "", ch.ID); err != nil {
		return err
	}
	voice, speed := NarratorVoice(book, r.DefaultVoice)
	key := VoiceKey(voice, speed)
	items := plan(ch, paras, segs)
	text := fingerprint(paras, segs)
	start := time.Now()

	// 1. Synthesize into the cache (skipping pieces a previous attempt
	// already finished).
	cacheDir := CacheDir(r.DataDir, ch.ID)
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return err
	}
	files := make([]string, len(items))
	var done int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(r.Concurrency, 1))
	progressCh := make(chan struct{}, len(items))
	for i, it := range items {
		files[i] = filepath.Join(cacheDir, fmt.Sprintf("%05d-%s.pcm", i, cacheKey(key, it.text)))
		if _, err := os.Stat(files[i]); err == nil {
			progressCh <- struct{}{}
			continue
		}
		g.Go(func() error {
			pcm, err := r.TTS.Synthesize(gctx, tts.Speech{Text: it.text, Voice: voice, Speed: speed, Format: "pcm"})
			if err != nil {
				var he *tts.HTTPError
				if errors.As(err, &he) && he.Permanent() {
					return jobs.Permanent(fmt.Errorf("synthesize %q: %w", truncate(it.text, 60), err))
				}
				return fmt.Errorf("synthesize %q: %w", truncate(it.text, 60), err)
			}
			if err := writeAtomic(files[i], pcm); err != nil {
				return err
			}
			progressCh <- struct{}{}
			return nil
		})
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- g.Wait(); close(progressCh) }()
	for range progressCh {
		done++
		progress(0.9 * float64(done) / float64(max(len(items), 1)))
	}
	if err := <-waitErr; err != nil {
		return err
	}

	// 2. Join with pauses and encode.
	rel := AudioPath(book.ID, ch.ID)
	out := filepath.Join(r.DataDir, rel)
	pr, pw := io.Pipe()
	written := make(chan int64, 1)
	go func() {
		n, err := writePCM(pw, files, items)
		written <- n
		pw.CloseWithError(err)
	}()
	err = r.Encoder.Encode(ctx, pr, out, audio.Metadata{
		Title: ch.Title, Album: book.Title, Artist: book.Author, Track: ch.Idx + 1,
	})
	_ = pr.Close() // unblocks the writer if ffmpeg failed early
	pcmBytes := <-written
	if err != nil {
		return err
	}
	st, err := os.Stat(out)
	if err != nil {
		return err
	}
	// 3. The chapter may have been merged, split or deleted meanwhile:
	// never mark audio of the old text as ready.
	if err := r.checkUnchanged(ctx, ch.ID, text); err != nil {
		if rerr := os.Remove(out); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			r.Log.Warn("remove outdated audio", "chapter", ch.ID, "err", rerr)
		}
		return err
	}
	duration := audio.Duration(pcmBytes)
	if err := r.Store.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{
		Path: rel, DurationMs: duration.Milliseconds(), Size: st.Size(), Voice: key,
	}); err != nil {
		return err
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		r.Log.Warn("remove render cache", "chapter", ch.ID, "err", err)
	}
	elapsed := time.Since(start)
	r.Metrics.observe(duration, elapsed)
	r.Log.Info("chapter rendered", "chapter", ch.ID, "audio", duration.Round(time.Second),
		"took", elapsed.Round(time.Second), "realtime_factor", fmt.Sprintf("%.2f", duration.Seconds()/elapsed.Seconds()))
	return nil
}

// checkUnchanged fails when the chapter is gone (permanently) or its text
// differs from the fingerprint (to be rendered again).
func (r *Renderer) checkUnchanged(ctx context.Context, chapterID int64, want string) error {
	if _, err := r.Store.GetChapter(ctx, chapterID); errors.Is(err, store.ErrNotFound) {
		return jobs.Permanent(errors.New("the chapter was deleted while it was rendered"))
	} else if err != nil {
		return err
	}
	paras, segs, err := r.Store.ChapterText(ctx, chapterID)
	if err != nil {
		return err
	}
	if fingerprint(paras, segs) != want {
		return errors.New("the chapter text changed while it was rendered; rendering again")
	}
	return nil
}

// writePCM streams the lead-in, every cached piece and its pause.
func writePCM(w io.Writer, files []string, items []item) (int64, error) {
	var total int64
	write := func(b []byte) error {
		n, err := w.Write(b)
		total += int64(n)
		return err
	}
	if err := write(audio.Silence(leadIn)); err != nil {
		return total, err
	}
	for i, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // path built from the data dir
		if err != nil {
			return total, err
		}
		// Kokoro's PCM is 16-bit; never write half a sample.
		if err := write(data[:len(data)&^1]); err != nil {
			return total, err
		}
		if err := write(audio.Silence(items[i].pause)); err != nil {
			return total, err
		}
	}
	return total, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
