// Package render turns a chapter's segments into an audio file: every
// segment is synthesized by the TTS server (in parallel, cached on disk so
// a retried job resumes), then the PCM is joined with pauses and encoded.
package render

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"

	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/prepare"
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

// TimedSynthesizer also reports when each word is spoken (optional: a TTS
// without it gives timings per segment only).
type TimedSynthesizer interface {
	SynthesizeTimed(ctx context.Context, s tts.Speech) ([]byte, []tts.Word, error)
}

// Engines tells what a voice's engine does itself (optional: without it
// every voice is Kokoro's).
type Engines interface {
	// Expressive: the engine speaks emotions and moods itself, so they
	// are not given by pace (only loudness).
	Expressive(voice string) bool
	// TakesSpeed: the engine takes a speed.
	TakesSpeed(voice string) bool
	// Fingerprint identifies the engine's settings ("" for Kokoro's
	// voices); it bounds its own concurrency.
	Fingerprint(voice string) string
	// JoinLimit is the longest text the engine takes in one request (0:
	// pieces are never joined, Kokoro's).
	JoinLimit(voice string) int
	// JoinParagraphs: the engine reads consecutive paragraphs in one
	// request (up to JoinLimit), not only those that flow on.
	JoinParagraphs(voice string) bool
}

// VoiceCatalog lists the voices pool voices are chosen from: those of the
// engine of a voice (the narrator's).
type VoiceCatalog interface {
	VoicesFor(ctx context.Context, voice string) ([]casting.BaseVoice, error)
}

// Attributor queues speaker attributions (or chapter preparations).
type Attributor interface {
	Start(ctx context.Context, b *store.Book, c *store.Chapter, force bool, priority int) error
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
	// Voices and Attribution enable character voices (both optional:
	// without them everything is narrated).
	Voices      VoiceCatalog
	Attribution Attributor
	// Preparation prepares chapters of books that ask for it (optional).
	Preparation Attributor
	Metrics     *Metrics
	Log         *slog.Logger
}

// AttributionPriority orders attributions a render waits for before those
// queued ahead of time.
const AttributionPriority = 150

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

// AudioPath is where a render of a chapter's audio is stored, relative to
// the data dir. Every render gets its own file (version): a listener may
// still be streaming the previous one.
func AudioPath(bookID, chapterID, version int64) string {
	return VersionPath(bookID, chapterID, strconv.FormatInt(version, 36))
}

// VersionPath is the audio file of a version (as AudioVersion returns it).
func VersionPath(bookID, chapterID int64, version string) string {
	return filepath.Join(importer.BookDir(bookID), "audio", strconv.FormatInt(chapterID, 10)+"-"+version+".m4a")
}

// AudioVersion returns the version part of an audio path ("" for none).
func AudioVersion(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".m4a")
	if _, v, ok := strings.Cut(base, "-"); ok {
		return v
	}
	return ""
}

// keepVersions removes a chapter's older audio files and their timings
// (also leftovers of either), keeping the current version and the one it
// replaced (a listener may still stream that).
func (r *Renderer) keepVersions(bookID, chapterID int64, keep ...string) {
	dir := filepath.Join(r.DataDir, importer.BookDir(bookID), "audio")
	files, _ := filepath.Glob(filepath.Join(dir, strconv.FormatInt(chapterID, 10)+"-*"))
	for _, f := range files {
		rel, err := filepath.Rel(r.DataDir, f)
		if err != nil {
			continue
		}
		// The audio file a file belongs to: x.m4a, x.json, x.json.tmp.
		base, _, _ := strings.Cut(filepath.Base(rel), ".")
		if slices.Contains(keep, filepath.Join(filepath.Dir(rel), base+".m4a")) {
			continue
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.Log.Warn("remove old audio", "chapter", chapterID, "err", err)
		}
	}
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

// pieceDoneKey: a context value, called when a piece was synthesized.
type pieceDoneKey struct{}

// Handler is the job handler for JobType.
func (r *Renderer) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.ChapterID == nil {
				return jobs.Permanent(errors.New("render job without chapter"))
			}
			// Attempts count failures without progress: a long chapter
			// whose pieces mostly succeed is not given up on.
			var once sync.Once
			ctx = context.WithValue(ctx, pieceDoneKey{}, func(ctx context.Context) {
				once.Do(func() {
					if j.Attempts <= 1 {
						return
					}
					j.Attempts = 1
					if err := r.Store.ResetJobAttempts(ctx, j.ID); err != nil {
						r.Log.Warn("reset job attempts", "job", j.ID, "err", err)
					}
				})
			})
			return r.Render(ctx, *j.ChapterID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.ChapterID == nil {
				return
			}
			// A failed re-render keeps the previous audio playable.
			status := store.AudioFailed
			if c, gerr := r.Store.GetChapter(ctx, *j.ChapterID); gerr == nil && c.AudioPath != "" {
				status = store.AudioReady
			}
			if serr := r.Store.SetAudioStatus(ctx, status, truncate(err.Error(), maxSynthErrLen), *j.ChapterID); serr != nil {
				r.Log.Error("mark audio failed", "chapter", *j.ChapterID, "err", serr)
			}
			// The pieces spoken so far (perhaps against an engine's quota)
			// stay cached for the next render; the cache goes once the
			// chapter is rendered or its text changes.
		},
	}
}

// item is one synthesis request plus the pause that follows it.
type item struct {
	text  string
	voice string
	speed float64
	gain  float64
	gap   gap
	pause time.Duration
	// segmentID is the segment spoken (0: the chapter title); a segment
	// may be spoken in several items (phrases).
	segmentID int64
	// What the pause after it depends on; extra is a pause the chapter
	// preparation asks for after it.
	quote, thought, dialogue bool
	emotion                  string
	extra                    time.Duration
	// style is how an expressive engine is told to speak it (nil: Kokoro
	// gets pace and loudness instead).
	style *style
	// engine identifies the settings of the voice's engine ("": Kokoro).
	engine string
	// parts are the segments a joined item speaks, in order, with their
	// length in runes (nil: only segmentID).
	parts []part
	// plain: the pause after it is the usual one between segments or
	// paragraphs (see pauses); flows: the next paragraph goes on from
	// this one (chapter preparation). An engine may read on across both.
	plain, flows bool
}

// part is a segment spoken in a joined item.
type part struct {
	segmentID int64
	runes     int
}

// style is how to speak, for an engine that acts itself.
type style struct {
	emotion, mood string
	thought       bool
}

func (s *style) key() string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("|%s|%s|%t", s.emotion, s.mood, s.thought)
}

// same tells whether two styles are spoken alike (a neutral emotion is
// none).
func (s *style) same(o *style) bool {
	norm := func(st *style) style {
		if st == nil {
			return style{}
		}
		n := *st
		if n.emotion == "neutral" {
			n.emotion = ""
		}
		return n
	}
	return (s == nil) == (o == nil) && norm(s) == norm(o)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// speech is the TTS request of an item.
func (it item) speech() tts.Speech {
	sp := tts.Speech{Text: it.text, Voice: it.voice, Speed: it.speed, Format: "pcm"}
	if it.style != nil {
		sp.Emotion, sp.Mood, sp.Thought = it.style.emotion, it.style.mood, it.style.thought
	}
	return sp
}

// file is where an item's audio is cached.
func (it item) file(dir string, i int) string {
	return filepath.Join(dir, fmt.Sprintf("%05d-%s.pcm", i, cacheKey(VoiceKey(it.voice, it.speed)+it.style.key()+it.engine, it.text)))
}

// voiceFunc returns the voice of a segment.
type voiceFunc func(s *store.Segment) (string, float64)

// plan lists what to say, in order, with the book's pronunciations applied
// (engines may be nil: all voices are Kokoro's).
func plan(ch *store.Chapter, paras []store.Paragraph, segs []store.Segment, voiceOf voiceFunc,
	lex *lexicon.Lexicon, engines Engines,
) []item {
	byPara := map[int64][]store.Segment{}
	for _, s := range segs {
		byPara[s.ParagraphID] = append(byPara[s.ParagraphID], s)
	}
	var items []item
	gapLast := func(g gap) {
		if len(items) > 0 {
			items[len(items)-1].gap = max(items[len(items)-1].gap, g)
		}
	}
	narrator, narratorSpeed := voiceOf(nil)
	say := func(s *store.Segment, text, voice string, speed float64, dialogue bool, mood string) {
		id, pr := int64(0), prosodyOf(s)
		lead := 0
		var st *style
		if s != nil {
			id = s.ID
			if i := strings.Index(s.Text, text); i > 0 {
				lead = utf8.RuneCountInString(s.Text[:i])
			}
			// Speech in the narrator's voice without an emotion of its own
			// (no character voices, an unknown speaker, a character cast
			// with the narrator's voice) is read like the narration around
			// it; a thought stays softer.
			byNarrator := s.QuoteRef == 0 || voice == narrator && (s.Emotion == "" || s.Emotion == "neutral")
			if m, ok := moods[mood]; ok && byNarrator {
				pr = m // narration in the paragraph's mood
				if s.Kind == store.SegmentThought {
					pr.speed *= thoughtProsody.speed
					pr.gain *= thoughtProsody.gain
				}
			}
			if engines != nil && engines.Expressive(voice) {
				// The engine acts itself; only loudness is ours.
				st, pr.speed = &style{thought: s.Kind == store.SegmentThought}, 1
				if byNarrator {
					st.mood = mood
				} else {
					st.emotion = s.Emotion
				}
			}
		}
		engine := ""
		if engines != nil {
			if !engines.TakesSpeed(voice) {
				speed, pr.speed = 1, 1
			}
			if engine = engines.Fingerprint(voice); engine != "" {
				engine = "|" + engine
			}
		}
		d := direct(s, text, lead)
		for i, ph := range d.phrases {
			// The book's pronunciations first (entries may name "AT&T"),
			// then the cleanup for the TTS, which leaves Kokoro's syntax
			// alone.
			t := spoken(lex.Apply(ph))
			if speakable(t) == "" {
				continue
			}
			it := item{
				text: t, voice: voice, speed: clampSpeed(speed * pr.speed), gain: pr.gain,
				gap: gapPhrase, pause: d.pauses[i], segmentID: id, dialogue: dialogue, style: st,
				engine: engine,
			}
			if s != nil {
				it.quote, it.thought, it.emotion = s.QuoteRef > 0, s.Kind == store.SegmentThought, s.Emotion
			}
			items = append(items, it)
		}
		gapLast(gapSegment)
		if n := len(items); n > 0 && items[n-1].segmentID == id {
			items[n-1].extra = max(items[n-1].extra, d.extra)
		}
	}
	if len(paras) == 0 || paras[0].Kind != store.ParagraphHeading {
		if t := speakable(ch.Title); t != "" {
			say(nil, t, narrator, narratorSpeed, false, "")
			gapLast(gapHeading)
		}
	}
	// spoken: the index of the last paragraph that was spoken (a flowing
	// paragraph reads on only from the paragraph right before it).
	spoken := -1
	for pi, p := range paras {
		switch p.Kind {
		case store.ParagraphSceneBreak:
			gapLast(gapScene)
			continue
		case store.ParagraphHeading:
			for i := range byPara[p.ID] {
				s := &byPara[p.ID][i]
				if t := speakable(s.Text); t != "" {
					say(s, t, narrator, narratorSpeed, false, "")
				}
			}
			gapLast(gapHeading)
		default:
			start := len(items)
			// A paragraph of only speech: an exchange of lines.
			dialogue := len(byPara[p.ID]) > 0
			for _, s := range byPara[p.ID] {
				if s.QuoteRef == 0 && speakable(s.Text) != "" {
					dialogue = false
				}
			}
			for i := range byPara[p.ID] {
				s := &byPara[p.ID][i]
				if t := speakable(s.Text); t != "" {
					voice, speed := voiceOf(s)
					say(s, t, voice, speed, dialogue, p.Mood)
				}
			}
			if len(items) > start {
				if p.Flows && start > 0 && spoken == pi-1 {
					items[start-1].flows = true
				}
				spoken = pi
			}
			gapLast(gapPara)
		}
	}
	gapLast(gapTail)
	pauses(items)
	if engines != nil {
		items = join(items, engines.JoinLimit, engines.JoinParagraphs)
	}
	return items
}

// join speaks the pieces of a paragraph in one request where an engine
// that acts itself would otherwise get them one by one (a sentence, a
// dialogue tag): consecutive items of one voice and style, separated only
// by the plain pause between segments, up to the engine's limit; across
// paragraphs too where the chapter preparation found one flows on from
// the other, or every paragraph where the engine reads on across them (a
// quota that counts requests); those keep a blank line between them, for
// the engine's own pause. Pauses that mean something (after a thought, a
// trailing line, one the chapter preparation asks for, a heading, a
// scene break) still split them.
func join(items []item, limitOf func(voice string) int, acrossParas func(voice string) bool) []item {
	out := items[:0:0]
	for _, it := range items {
		n := len(out)
		if n == 0 {
			out = append(out, it)
			continue
		}
		prev := &out[n-1]
		limit := limitOf(it.voice)
		sep := " "
		readsOn := prev.plain && (prev.gap == gapSegment || prev.gap == gapPara && prev.flows)
		if !readsOn && prev.plain && prev.gap == gapPara && acrossParas(it.voice) {
			readsOn, sep = true, "\n\n"
		}
		if prev.engine == "" || prev.engine != it.engine || prev.voice != it.voice || prev.speed != it.speed ||
			prev.gain != it.gain || !prev.style.same(it.style) || !readsOn || limit <= 0 ||
			utf8.RuneCountInString(prev.text)+len(sep)+utf8.RuneCountInString(it.text) > limit {
			out = append(out, it)
			continue
		}
		if prev.parts == nil {
			prev.parts = []part{{prev.segmentID, utf8.RuneCountInString(prev.text)}}
		}
		if it.parts == nil {
			it.parts = []part{{it.segmentID, utf8.RuneCountInString(it.text)}}
		}
		prev.parts = append(prev.parts, it.parts...)
		// The joined item ends where the last one did.
		joined := it
		joined.text = prev.text + sep + it.text
		joined.segmentID, joined.parts = prev.segmentID, prev.parts
		*prev = joined
	}
	return out
}

// narratorOnly lets the narrator read every quotation like narration (the
// book has no character voices).
func narratorOnly(segs []store.Segment) {
	for i := range segs {
		segs[i].SpeakerCharacterID, segs[i].Emotion = nil, ""
	}
}

// withoutPreparation drops what a chapter preparation found (the book
// no longer asks for it).
func withoutPreparation(paras []store.Paragraph, segs []store.Segment) {
	for i := range paras {
		paras[i].Mood, paras[i].Flows = "", false
	}
	for i := range segs {
		segs[i].Pauses, segs[i].SpokenAs = "", ""
	}
}

// clampSpeed keeps a speed within what Kokoro takes.
func clampSpeed(v float64) float64 {
	return math.Round(min(max(v, 0.5), 2)*100) / 100
}

// speakable strips quotation marks around a segment (Kokoro would otherwise
// pause oddly) and drops text without anything to say.
func speakable(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "“”‘’\"'«»‹›„")
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
	if wait, err := r.waitForPreparation(ctx, book, ch); err != nil || wait {
		if err == nil {
			err = prepare.WaitError()
		}
		return err
	}
	if wait, err := r.waitForVoices(ctx, book, ch); err != nil || wait {
		if err == nil {
			err = attribution.WaitError()
		}
		return err
	}
	paras, segs, err := r.Store.ChapterText(ctx, ch.ID)
	if err != nil {
		return err
	}
	if !book.CharacterVoices {
		narratorOnly(segs)
	}
	if ch.AudioPath == "" {
		if err := r.Store.SetAudioStatus(ctx, store.AudioRendering, "", ch.ID); err != nil {
			return err
		}
	}
	voice, speed := NarratorVoice(book, r.DefaultVoice)
	key := VoiceKey(voice, speed)
	voiceOf, err := r.speakerVoices(ctx, book, segs, voice, speed)
	if err != nil {
		return err
	}
	entries, err := r.Store.ListLexicon(ctx, book.ID)
	if err != nil {
		return err
	}
	if !book.PrepareChapters {
		withoutPreparation(paras, segs)
	}
	engines, _ := r.TTS.(Engines)
	items := plan(ch, paras, segs, voiceOf, lexicon.Compile(entries), engines)
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
	// Kokoro's requests are bounded here; other engines bound their own
	// (so a slow engine does not hold Kokoro's slots).
	kokoro := make(chan struct{}, max(r.Concurrency, 1))
	progressCh := make(chan struct{}, len(items))
	var todo []int
	for i, it := range items {
		files[i] = it.file(cacheDir, i)
		if _, err := os.Stat(files[i]); err == nil {
			progressCh <- struct{}{}
		} else {
			todo = append(todo, i)
		}
	}
	// Other engines' pieces first (they wait for their engine, not for
	// Kokoro), then Kokoro's in order.
	slices.SortStableFunc(todo, func(a, b int) int {
		return cmp.Compare(boolInt(items[a].engine == ""), boolInt(items[b].engine == ""))
	})
	for _, i := range todo {
		it := items[i]
		// In order: a Kokoro slot is taken before the request starts.
		if it.engine == "" {
			select {
			case kokoro <- struct{}{}:
			case <-gctx.Done(): // a request failed
			}
		}
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			if it.engine == "" {
				defer func() { <-kokoro }()
			}
			pcm, words, err := r.synthesize(gctx, it.speech())
			if err != nil {
				var he *tts.HTTPError
				if errors.As(err, &he) && he.Permanent() {
					return jobs.Permanent(fmt.Errorf("synthesize %q: %w", truncate(it.text, 60), err))
				}
				return fmt.Errorf("synthesize %q: %w", truncate(it.text, 60), err)
			}
			// The words first: the audio marks the item as done.
			if words != nil {
				data, err := json.Marshal(words)
				if err != nil {
					return err
				}
				if err := writeAtomic(files[i]+".words", data); err != nil {
					return err
				}
			}
			if err := writeAtomic(files[i], pcm); err != nil {
				return err
			}
			if done, ok := ctx.Value(pieceDoneKey{}).(func(context.Context)); ok {
				done(ctx)
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
	if err := ctx.Err(); err != nil { // stopped before every piece was asked for
		return err
	}

	// 2. Join with pauses and encode.
	rel := AudioPath(book.ID, ch.ID, time.Now().UnixNano())
	out := filepath.Join(r.DataDir, rel)
	pr, pw := io.Pipe()
	written := make(chan int64, 1)
	pieces := make([]int64, len(files))
	go func() {
		n, err := writePCM(pw, files, items, pieces)
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
	// The cues are written before the audio is published: a player that
	// sees the new version also finds its timings.
	texts := map[int64]string{0: speakable(ch.Title)}
	for _, s := range segs {
		texts[s.ID] = s.Text
	}
	cueData, err := json.Marshal(Timings{Cues: cues(items, pieces, readWords(files), func(id int64) string { return texts[id] })})
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(r.DataDir, TimingsPath(rel)), cueData); err != nil {
		_ = RemoveAudio(r.DataDir, rel)
		return err
	}
	// 3. The chapter may have been merged, split or deleted meanwhile:
	// never mark audio of the old text as ready.
	if err := r.checkUnchanged(ctx, ch.ID, text); err != nil {
		if rerr := RemoveAudio(r.DataDir, rel); rerr != nil {
			r.Log.Warn("remove outdated audio", "chapter", ch.ID, "err", rerr)
		}
		return err
	}
	duration := audio.Duration(pcmBytes)
	previous := ""
	if cur, err := r.Store.GetChapter(ctx, ch.ID); err == nil {
		previous = cur.AudioPath
	}
	if err := r.Store.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{
		Path: rel, DurationMs: duration.Milliseconds(), Size: st.Size(), Voice: key, VoiceRev: ch.VoiceRev,
	}); err != nil {
		return err
	}
	r.keepVersions(book.ID, ch.ID, rel, previous)
	if err := os.RemoveAll(cacheDir); err != nil {
		r.Log.Warn("remove render cache", "chapter", ch.ID, "err", err)
	}
	elapsed := time.Since(start)
	r.Metrics.observe(duration, elapsed)
	r.Log.Info("chapter rendered", "chapter", ch.ID, "audio", duration.Round(time.Second),
		"took", elapsed.Round(time.Second), "realtime_factor", fmt.Sprintf("%.2f", duration.Seconds()/elapsed.Seconds()))
	return nil
}

// waitFor is how far a job may be deferred and still be waited for:
// beyond it (out of LLM quota, no provider) the chapter is rendered with
// the voices it has, and rendered again once they change.
const waitFor = 2 * time.Minute

// waitForVoices reports whether a render should wait: for the speaker
// attribution the chapter needs (queued if missing), and for a pending
// casting when the chapter has speakers — unless that job cannot run soon.
func (r *Renderer) waitForVoices(ctx context.Context, b *store.Book, ch *store.Chapter) (bool, error) {
	if r.Attribution == nil || !b.CharacterVoices {
		return false, nil
	}
	soon := func(j *store.Job) bool {
		return j != nil && j.NotBefore.Before(time.Now().Add(waitFor))
	}
	if attribution.Needed(b, ch) {
		// Start does nothing while a job is pending, and queues one again
		// if it was lost or canceled.
		err := r.Attribution.Start(ctx, b, ch, false, AttributionPriority)
		if err != nil && !errors.Is(err, attribution.ErrNotReady) {
			return false, err
		}
		j, err := r.Store.PendingChapterJob(ctx, attribution.JobType, ch.ID)
		if err != nil || soon(j) {
			return err == nil, err
		}
	}
	speaks, err := r.Store.HasSpeakers(ctx, ch.ID)
	if err != nil || !speaks {
		return false, err
	}
	j, err := r.Store.PendingBookJob(ctx, casting.JobType, b.ID)
	return err == nil && soon(j), err
}

// waitForPreparation reports whether a render should wait for the
// preparation of its chapter (queued if missing), unless that job cannot
// run soon.
func (r *Renderer) waitForPreparation(ctx context.Context, b *store.Book, ch *store.Chapter) (bool, error) {
	if r.Preparation == nil || !prepare.Needed(b, ch) {
		return false, nil
	}
	if err := r.Preparation.Start(ctx, b, ch, false, AttributionPriority); err != nil {
		return false, err
	}
	j, err := r.Store.PendingChapterJob(ctx, prepare.JobType, ch.ID)
	return err == nil && j != nil && j.NotBefore.Before(time.Now().Add(waitFor)), err
}

// speakerVoices resolves the voices of a chapter's speakers: their own
// recipe or a pool voice (casting.Effective); narration, unknown speakers
// and quotes not attributed yet are narrated.
func (r *Renderer) speakerVoices(ctx context.Context, b *store.Book, segs []store.Segment, narrator string,
	narratorSpeed float64,
) (voiceFunc, error) {
	var ids []int64
	seen := map[int64]bool{}
	for _, s := range segs {
		if id := s.SpeakerCharacterID; s.QuoteRef > 0 && id != nil && !seen[*id] {
			seen[*id] = true
			ids = append(ids, *id)
		}
	}
	chars, err := r.Store.GetCharacters(ctx, ids...)
	if err != nil {
		return nil, err
	}
	var base []casting.BaseVoice
	if r.Voices != nil && len(chars) > 0 {
		// The characters speak with the narrator's engine (Kokoro's
		// catalogue is assumed when the server is down).
		base, _ = r.Voices.VoicesFor(ctx, narrator)
	}
	configured, _ := r.TTS.(interface{ Configured(voice string) bool })
	voices := map[int64]casting.Voice{}
	for i := range chars {
		c := chars[i]
		if configured != nil && c.VoiceRecipe != "" && !configured.Configured(c.VoiceRecipe) {
			// A voice of an engine this deployment lacks (chosen before it
			// changed): a pool voice instead.
			c.VoiceRecipe = ""
		}
		voices[c.ID] = casting.Effective(&c, base, narrator, narratorSpeed)
	}
	return func(s *store.Segment) (string, float64) {
		// Quotations and thoughts are spoken by their speaker.
		if s != nil && s.QuoteRef > 0 && s.SpeakerCharacterID != nil {
			if v, ok := voices[*s.SpeakerCharacterID]; ok {
				return v.Recipe, v.Speed
			}
		}
		return narrator, narratorSpeed
	}, nil
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

// synthesize renders a piece, with the time of its words when the TTS
// reports them (words nil otherwise).
func (r *Renderer) synthesize(ctx context.Context, s tts.Speech) ([]byte, []tts.Word, error) {
	if t, ok := r.TTS.(TimedSynthesizer); ok {
		pcm, words, err := t.SynthesizeTimed(ctx, s)
		if !errors.Is(err, tts.ErrNoTimestamps) {
			if words == nil && err == nil {
				words = []tts.Word{}
			}
			return pcm, words, err
		}
	}
	pcm, err := r.TTS.Synthesize(ctx, s)
	return pcm, nil, err
}

// readWords loads the words of the cached pieces (nil where none).
func readWords(files []string) [][]tts.Word {
	out := make([][]tts.Word, len(files))
	for i, f := range files {
		data, err := os.ReadFile(f + ".words") //nolint:gosec // path built from the data dir
		if err != nil {
			continue
		}
		_ = json.Unmarshal(data, &out[i])
	}
	return out
}

// writePCM streams the lead-in, every cached piece (at its loudness) and
// its pause, and records the length of each piece in pieces.
func writePCM(w io.Writer, files []string, items []item, pieces []int64) (int64, error) {
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
		data = data[:len(data)&^1]
		pieces[i] = int64(len(data))
		applyGain(data, items[i].gain)
		if err := write(data); err != nil {
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
