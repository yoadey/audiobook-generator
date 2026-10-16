package attribution_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

type fixture struct {
	s         *store.Store
	dir       string
	book      *store.Book
	chapters  []store.Chapter // Copyright, Chapter 1, Chapter 2, Chapter 3
	anna, tom *store.Character
	a         *attribution.Attributor
	srv       *llmtest.Server
	jobs      *enqueuer
}

type enqueuer struct {
	s    *store.Store
	jobs []store.Job
}

func (e *enqueuer) Enqueue(ctx context.Context, j *store.Job) error {
	if j.Queue == "" {
		j.Queue = attribution.Queue
	}
	if err := e.s.EnqueueJob(ctx, j); err != nil {
		return err
	}
	e.jobs = append(e.jobs, *j)
	return nil
}

func setup(t *testing.T, s *store.Store) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	data, err := epubtest.Sample()
	require.NoError(t, err)
	b := &store.Book{Title: "The Sample", Status: store.BookImporting}
	require.NoError(t, s.CreateBook(ctx, b))
	b.EpubPath = importer.SourcePath(b.ID)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, importer.BookDir(b.ID)), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, b.EpubPath), data, 0o600))
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"EpubPath": b.EpubPath}))
	log := slog.New(slog.DiscardHandler)
	im := &importer.Importer{Store: s, DataDir: dir, Log: log}
	require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"AnalysisStatus": store.AnalysisDone}))
	b, err = s.GetBook(ctx, b.ID)
	require.NoError(t, err)
	cs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
	require.NoError(t, err)
	require.Len(t, cs, 4)

	f := &fixture{s: s, dir: dir, book: b, chapters: cs}
	f.anna = &store.Character{BookID: b.ID, Name: "Anna Smith", Gender: store.GenderFemale, Speaks: true,
		Tier: store.TierMain, VoiceRecipe: "bf_emma"}
	f.tom = &store.Character{BookID: b.ID, Name: "Tom", Gender: store.GenderMale, Speaks: true}
	require.NoError(t, s.CreateCharacter(ctx, f.anna, "Anna"))
	require.NoError(t, s.CreateCharacter(ctx, f.tom))
	for _, c := range []*store.Character{f.anna, f.tom} {
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, c.ID, cs[1].ID, cs[2].ID))
	}
	f.srv = llmtest.New(t)
	p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: f.srv.BaseURL(), Model: "m"}
	llm.Normalize(p)
	require.NoError(t, s.CreateLLMProvider(ctx, p))
	f.jobs = &enqueuer{s: s}
	f.a = &attribution.Attributor{Store: s, LLM: llm.NewPool(s, nil, log), Jobs: f.jobs, Log: log}
	return f
}

var qRe = regexp.MustCompile(`\[q(\d+)\]`)

func finishJobs(t *testing.T, s *store.Store) {
	t.Helper()
	require.NoError(t, s.DB().Model(&store.Job{}).Where("status IN ?", []store.JobStatus{store.JobQueued, store.JobRunning}).
		Update("status", store.JobDone).Error)
}

func userMessage(r llmtest.Request) string {
	ms := r.Messages()
	c, _ := ms[len(ms)-1]["content"].(string)
	return c
}

func reply(t *testing.T, quotes ...map[string]any) llmtest.Reply {
	t.Helper()
	if quotes == nil {
		quotes = []map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"quotes": quotes})
	require.NoError(t, err)
	return llmtest.Reply{Content: string(raw)}
}

func q(n int, speaker string, conf float64) map[string]any {
	return map[string]any{"q": n, "speaker": speaker, "emotion": "neutral", "confidence": conf}
}

func ref(c *store.Character) string { return "c" + strconv.FormatInt(c.ID, 10) }

// speakers maps quote numbers to speaker IDs (0: none).
func speakers(t *testing.T, s *store.Store, chapterID int64) map[int]int64 {
	t.Helper()
	_, segs, err := s.ChapterText(context.Background(), chapterID)
	require.NoError(t, err)
	out := map[int]int64{}
	for _, seg := range segs {
		if seg.QuoteRef > 0 {
			out[seg.QuoteRef] = 0
			if seg.SpeakerCharacterID != nil {
				out[seg.QuoteRef] = *seg.SpeakerCharacterID
			}
		}
	}
	return out
}

func TestAttributeChapter(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			msg := userMessage(r)
			assert.Contains(t, msg, "[q1]“Good morning,”")
			assert.Contains(t, msg, ref(f.anna)+" | Anna Smith | Anna | female")
			assert.Len(t, qRe.FindAllString(msg, -1), 3, "multi-paragraph quotes are marked once")
			return reply(t,
				q(1, ref(f.anna), 0.95), q(2, ref(f.anna), 0.9),
				map[string]any{"q": 3, "speaker": ref(f.tom), "emotion": "WHISPERING", "confidence": 1.7},
				q(99, ref(f.tom), 1), // not sent: ignored
			)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.anna.ID, 2: f.anna.ID, 3: f.tom.ID}, speakers(t, s, ch.ID))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionDone, got.AttributionStatus)
		assert.Equal(t, "attribute_chapter@v1", got.AttributionPrompt)
		assert.Positive(t, got.VoiceRev)
		_, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		for _, seg := range segs {
			if seg.QuoteRef == 3 {
				assert.Equal(t, "whispering", seg.Emotion)
				assert.InDelta(t, 1, seg.Confidence, 1e-9, "clamped")
			}
		}
		st, err := s.QuoteStats(ctx, ch.ID, attribution.UnsureBelow)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionCounts{Quotes: 3, Attributed: 3}, st)

		// Chapter 2 gets the last speakers of chapter 1 as context.
		ch2 := f.chapters[2]
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			assert.Contains(t, userMessage(r), "Speakers of the last quotations before this text: Tom ("+ref(f.tom)+"), Anna Smith")
			return reply(t, q(1, ref(f.tom), 0.9))
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch2, false, 0))
		require.NoError(t, f.a.Run(ctx, ch2.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.tom.ID}, speakers(t, s, ch2.ID))
	})
}

func TestNewAndInvalidSpeakers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t,
				map[string]any{"q": 1, "speaker": "new:Old Gregor", "gender": "male", "emotion": "happy", "confidence": 0.7},
				q(2, "c999999", 0.9), // not offered
				// 3 missing
			)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		sp := speakers(t, s, ch.ID)
		assert.Zero(t, sp[2])
		assert.Zero(t, sp[3])
		gregor, err := s.GetCharacter(ctx, sp[1])
		require.NoError(t, err)
		assert.Equal(t, "Old Gregor", gregor.Name)
		assert.Equal(t, store.GenderMale, gregor.Gender)
		assert.Equal(t, store.TierMinor, gregor.Tier)
		assert.Equal(t, 1, gregor.ChapterCount)
		st, err := s.QuoteStats(ctx, ch.ID, attribution.UnsureBelow)
		require.NoError(t, err)
		assert.Equal(t, 2, st.Unsure, "unknown speakers are flagged")

		// Re-attributing keeps a speaker chosen by hand and matches a new
		// speaker by name instead of creating it again.
		require.NoError(t, s.SetQuoteSpeaker(ctx, ch.ID, 2, &f.tom.ID, "angry"))
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t, q(1, "new:Old Gregor", 0.8), q(2, ref(f.anna), 0.9), q(3, "new:Anna", 0.8))
		}
		finishJobs(t, s)
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		require.NoError(t, f.a.Start(ctx, f.book, got, true, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: gregor.ID, 2: f.tom.ID, 3: f.anna.ID}, speakers(t, s, ch.ID))
		n, err := s.CountCharacters(ctx, f.book.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 3, n)
	})
}

func TestLongChaptersInPartsAndResume(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		f.a.PartTokens = 10 // a part per quoted paragraph
		ch := f.chapters[1]
		var marked [][]string
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			var out []map[string]any
			var refs []string
			for _, m := range qRe.FindAllStringSubmatch(userMessage(r), -1) {
				n, _ := strconv.Atoi(m[1])
				refs = append(refs, m[1])
				out = append(out, q(n, ref(f.anna), 0.8))
			}
			marked = append(marked, refs)
			return reply(t, out...)
		}
		// A job that stopped after the first part (the heading and the
		// paragraph with quotes 1 and 2) resumes with the next paragraph.
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", ch.ID).Update("attribution_part", 2).Error)
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, [][]string{{"3"}}, marked, "only the remaining part, its quote marked once")
		assert.Equal(t, map[int]int64{1: 0, 2: 0, 3: f.anna.ID}, speakers(t, s, ch.ID))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, got.AttributionPart, "the continuation of quote 3 is no part of its own")

		// From the start: every part, later ones with context.
		marked = nil
		finishJobs(t, s)
		require.NoError(t, f.a.Start(ctx, f.book, got, true, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, [][]string{{"1", "2"}, {"3"}}, marked)
		reqs := f.srv.Requests()
		assert.Contains(t, userMessage(reqs[len(reqs)-1]), "--- context")
		for n, id := range speakers(t, s, ch.ID) {
			assert.Equal(t, f.anna.ID, id, "quote %d", n)
		}
	})
}

func TestTruncatedAnswersSplitTheRequest(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		first := true
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			if first {
				first = false
				return llmtest.Reply{Content: `{"quotes": [{"q": 1`, FinishReason: "length"}
			}
			var out []map[string]any
			for _, m := range qRe.FindAllStringSubmatch(userMessage(r), -1) {
				n, _ := strconv.Atoi(m[1])
				out = append(out, q(n, ref(f.tom), 0.8))
			}
			return reply(t, out...)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.tom.ID, 2: f.tom.ID, 3: f.tom.ID}, speakers(t, s, ch.ID))
		assert.Len(t, f.srv.Requests(), 2, "the cut-off request, then its half with the quotes")
	})
}

func TestStartRules(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		notDone := *f.book
		notDone.AnalysisStatus = store.AnalysisRunning
		require.ErrorIs(t, f.a.Start(ctx, &notDone, &ch, false, 0), attribution.ErrNotReady)
		assert.False(t, attribution.Needed(&notDone, &ch))
		assert.True(t, attribution.Needed(f.book, &ch))
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		assert.Len(t, f.jobs.jobs, 1, "one job per chapter")
		require.ErrorIs(t, f.a.Start(ctx, f.book, &ch, true, 0), attribution.ErrBusy)

		j := f.jobs.jobs[0]
		f.a.Handler().OnFail(ctx, &j, errors.New("gave up"))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionFailed, got.AttributionStatus)
		assert.False(t, attribution.Needed(f.book, got), "a failed chapter is narrated")
	})
}

type fakeTTS struct {
	mu     sync.Mutex
	voices map[string]string // text → voice
}

func (f *fakeTTS) Synthesize(_ context.Context, sp tts.Speech) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.voices[sp.Text] = render.VoiceKey(sp.Voice, sp.Speed)
	return make([]byte, 4800), nil
}

func TestRenderWaitsForSpeakersAndUsesTheirVoices(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		synth := &fakeTTS{voices: map[string]string{}}
		r := &render.Renderer{
			Store: s, TTS: synth, Encoder: audio.Encoder{FFmpeg: ff}, DataDir: f.dir, Concurrency: 2,
			DefaultVoice: "af_heart", Attribution: f.a, Log: slog.New(slog.DiscardHandler),
		}
		err := r.Render(ctx, ch.ID, func(float64) {})
		var d jobs.Deferrer
		require.ErrorAs(t, err, &d, "the render waits for the attribution")
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionQueued, got.AttributionStatus, "and queued it")
		assert.Empty(t, synth.voices)

		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t, q(1, ref(f.anna), 0.9), q(2, ref(f.anna), 0.9), q(3, "unknown", 0.2))
		}
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		require.NoError(t, r.Render(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, "bf_emma@1.00", synth.voices["Good morning,"])
		assert.Equal(t, "bf_emma@1.00", synth.voices["Is it raining?"])
		assert.Equal(t, "af_heart@1.00", synth.voices["said Anna."], "narration")
		assert.Equal(t, "af_heart@1.00", synth.voices["It was raining earlier,"], "unknown speaker")
		got, err = s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, got.VoiceRev, got.AudioVoiceRev)

		// A voice change makes the audio outdated.
		sched := &render.Scheduler{Store: s, DefaultVoice: "af_heart"}
		assert.False(t, sched.Stale(f.book, got))
		require.NoError(t, s.TouchCharacterVoices(ctx, f.anna.ID))
		got, err = s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.True(t, sched.Stale(f.book, got))
	})
}

func TestResetWhileAttributing(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			// The analysis is restarted while the request is answered.
			require.NoError(t, s.ResetAnalysis(ctx, f.book.ID))
			require.NoError(t, s.UpdateBook(ctx, f.book.ID, map[string]any{"AnalysisStatus": store.AnalysisDone}))
			return reply(t, q(1, "new:Ghost", 0.9), q(2, ref(f.anna), 0.9), q(3, ref(f.tom), 0.9))
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}), "a reset answer is dropped quietly")
		assert.Equal(t, map[int]int64{1: 0, 2: 0, 3: 0}, speakers(t, s, ch.ID))
		n, err := s.CountCharacters(ctx, f.book.ID)
		require.NoError(t, err)
		assert.Zero(t, n, "no character from the dropped answer")
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionNone, got.AttributionStatus)
		assert.Zero(t, got.AttributionPart)
	})
}

func TestSpeakerDeletedWhileAttributing(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			require.NoError(t, s.DeleteCharacter(ctx, f.tom.ID))
			return reply(t, q(1, ref(f.anna), 0.9), q(2, ref(f.anna), 0.9), q(3, ref(f.tom), 0.9))
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.anna.ID, 2: f.anna.ID, 3: 0}, speakers(t, s, ch.ID))
	})
}

func TestAttributionWaitsForAnAnalysisRunningAgain(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, s.UpdateBook(ctx, f.book.ID, map[string]any{"AnalysisStatus": store.AnalysisQueued}))
		var d jobs.Deferrer
		require.ErrorAs(t, f.a.Run(ctx, ch.ID, func(float64) {}), &d)
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionQueued, got.AttributionStatus)
	})
}

func TestRenderRequeuesALostAttributionAndWaitsForTheCasting(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		r := &render.Renderer{Store: s, TTS: &fakeTTS{voices: map[string]string{}}, DataDir: f.dir,
			DefaultVoice: "af_heart", Attribution: f.a, Log: slog.New(slog.DiscardHandler)}
		// "queued" without a job (canceled, or lost).
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", ch.ID).
			Update("attribution_status", store.AttributionQueued).Error)
		var d jobs.Deferrer
		require.ErrorAs(t, r.Render(ctx, ch.ID, func(float64) {}), &d)
		assert.Len(t, f.jobs.jobs, 1, "queued again")

		// Attributed, but a casting is pending: wait for it.
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t, q(1, ref(f.anna), 0.9), q(2, ref(f.anna), 0.9), q(3, ref(f.tom), 0.9))
		}
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		require.NoError(t, s.EnqueueJob(ctx, &store.Job{Type: "cast_book", Queue: "test-only", BookID: &f.book.ID}))
		require.ErrorAs(t, r.Render(ctx, ch.ID, func(float64) {}), &d)
	})
}

func TestOutdatedAudioStaysPlayable(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		require.NoError(t, s.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{Path: "x.m4a", Voice: "af_heart@1.00"}))
		// The analysis finished: narrated audio is outdated …
		require.NoError(t, s.TouchBookVoices(ctx, f.book.ID))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		sched := &render.Scheduler{Store: s, Jobs: f.jobs, DefaultVoice: "af_heart"}
		assert.True(t, sched.Stale(f.book, got))
		// … and rendered again, but stays playable meanwhile.
		ok, err := sched.RenderChapter(ctx, f.book, got, false)
		require.NoError(t, err)
		assert.True(t, ok)
		got, err = s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus)
	})
}

func TestRenderDoesNotWaitForJobsThatCannotRun(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		r := &render.Renderer{Store: s, TTS: &fakeTTS{voices: map[string]string{}}, Encoder: audio.Encoder{FFmpeg: ff},
			DataDir: f.dir, DefaultVoice: "af_heart", Attribution: f.a, Log: slog.New(slog.DiscardHandler)}
		// The attribution waits for LLM quota until tomorrow: render the
		// chapter narrated now (it is rendered again once attributed).
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, s.DB().Model(&store.Job{}).Where("type = ?", attribution.JobType).
			Update("not_before", time.Now().Add(24*time.Hour)).Error)
		require.NoError(t, r.Render(ctx, ch.ID, func(float64) {}))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus)
		sched := &render.Scheduler{Store: s, DefaultVoice: "af_heart"}
		assert.False(t, sched.Stale(f.book, got), "not rendered again until the speakers change")

		// A chapter without speakers does not wait for the casting.
		require.NoError(t, s.EnqueueJob(ctx, &store.Job{Type: "cast_book", Queue: "test-only", BookID: &f.book.ID}))
		require.NoError(t, r.Render(ctx, f.chapters[3].ID, func(float64) {}))
	})
}
