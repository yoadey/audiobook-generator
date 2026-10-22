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

var paraRe = regexp.MustCompile(`(?m)^\[p(\d+)\]`)

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

func reply(t *testing.T, voices ...map[string]any) llmtest.Reply {
	t.Helper()
	if voices == nil {
		voices = []map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"voices": voices, "narration": []any{}})
	require.NoError(t, err)
	return llmtest.Reply{Content: string(raw)}
}

// v is a passage of paragraph p (numbered from 1, as in the prompt) given
// by its whole text.
func v(p int, text, speaker string, conf float64) map[string]any {
	return map[string]any{"p": p, "start": text, "end": text, "kind": "speech", "speaker": speaker,
		"emotion": "neutral", "direction": "", "confidence": conf}
}

// The passages of chapter 1 ([p1] is its heading): quotes 1 and 2 in
// [p2], 3 in [p3] and 4 in [p4] (the speech that runs over two
// paragraphs).
func chapter1(speakers ...string) []map[string]any {
	ps := []int{2, 2, 3, 4}
	texts := []string{"Good morning,", "Is it raining?", "It was raining earlier,", "but it has stopped now."}
	var out []map[string]any
	for i, sp := range speakers {
		out = append(out, v(ps[i], texts[i], sp, 0.9))
	}
	return out
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

// texts lists the chapter's segments as "kind:text".
func texts(t *testing.T, s *store.Store, chapterID int64) []string {
	t.Helper()
	_, segs, err := s.ChapterText(context.Background(), chapterID)
	require.NoError(t, err)
	var out []string
	for _, seg := range segs {
		out = append(out, string(seg.Kind)+":"+seg.Text)
	}
	return out
}

func TestAttributeChapter(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		_, before, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			msg := userMessage(r)
			assert.Contains(t, msg, "[p2] “Good morning,” said Anna. “Is it raining?”")
			assert.NotContains(t, msg, "[q1]", "quotes are not marked: the model finds them")
			assert.Contains(t, msg, ref(f.anna)+" | Anna Smith | Anna | female | unknown | ")
			assert.Len(t, paraRe.FindAllString(msg, -1), 4, "every text paragraph is numbered")
			vs := chapter1(ref(f.anna), ref(f.anna), ref(f.tom))
			vs = append(vs,
				map[string]any{"p": 4, "start": "\"but it has", "end": "stopped now.\"", "kind": "remote",
					"speaker": ref(f.tom), "emotion": "WHISPERING", "direction": "  barely above a whisper,\n tired. ",
					"confidence": 1.7},
				v(99, "Bye,", ref(f.tom), 1),           // not sent: ignored
				v(6, "not in the text", ref(f.tom), 1), // not found: ignored
			)
			raw, _ := json.Marshal(map[string]any{"voices": vs, "narration": []map[string]any{
				{"p": 6, "direction": "flat, a little bored"},
			}})
			return llmtest.Reply{Content: string(raw)}
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.anna.ID, 2: f.anna.ID, 3: f.tom.ID, 4: f.tom.ID}, speakers(t, s, ch.ID))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionDone, got.AttributionStatus)
		assert.Equal(t, "attribute_chapter@v4", got.AttributionPrompt)
		assert.Equal(t, 4, got.QuoteCount, "the multi-paragraph quote is a passage per paragraph")
		assert.Positive(t, got.VoiceRev)
		paras, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		// Split as on import: the segments keep their IDs.
		for i, seg := range segs {
			assert.Equal(t, before[i].ID, seg.ID)
			assert.Equal(t, before[i].Text, seg.Text)
		}
		for _, seg := range segs {
			switch seg.QuoteRef {
			case 1:
				assert.Equal(t, store.VoicingSpeech, seg.Voicing)
			case 4:
				assert.Equal(t, "whispering", seg.Emotion)
				assert.Equal(t, store.VoicingRemote, seg.Voicing)
				assert.Equal(t, "barely above a whisper, tired", seg.Direction)
				assert.InDelta(t, 1, seg.Confidence, 1e-9, "clamped")
			}
		}
		assert.Equal(t, "flat, a little bored", paras[5].Direction)
		st, err := s.QuoteStats(ctx, ch.ID, attribution.UnsureBelow)
		require.NoError(t, err)
		assert.Equal(t, store.AttributionCounts{Quotes: 4, Attributed: 4}, st)

		// Chapter 2 gets the last speakers of chapter 1 as context.
		ch2 := f.chapters[2]
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			assert.Contains(t, userMessage(r), "Speakers of the last lines before this text: Tom ("+ref(f.tom)+"), Anna Smith")
			return reply(t, v(2, "Bye,", ref(f.tom), 0.9))
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch2, false, 0))
		require.NoError(t, f.a.Run(ctx, ch2.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.tom.ID}, speakers(t, s, ch2.ID))
	})
}

func TestTheModelSplitsParagraphs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		// The model finds a thought without marks, leaves out a quote the
		// importer found and reads the rest of the paragraph as speech.
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t,
				map[string]any{"p": 3, "start": "Tom looked outside.", "end": "Tom looked outside.", "kind": "thought",
					"speaker": ref(f.tom), "emotion": "neutral", "direction": "wistful", "confidence": 0.8},
				v(2, "Is it raining?", ref(f.anna), 0.9),
				map[string]any{"p": 6, "start": "nothing happened", "end": "nothing happened.", "kind": "telepathy",
					"speaker": "unknown", "emotion": "neutral", "direction": "", "confidence": 0.4},
			)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, []string{
			"narration:Chapter 1",
			"narration:“Good morning,” said Anna.", "quote:“Is it raining?”",
			"thought:Tom looked outside.", "narration:“It was raining earlier,",
			"narration:“but it has stopped now.”",
			"narration:Later that day,", "thought:nothing happened.",
		}, texts(t, s, ch.ID))
		_, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		for i, seg := range segs {
			assert.Equal(t, i, seg.Idx, "numbered anew")
		}
		assert.Equal(t, map[int]int64{1: f.anna.ID, 2: f.tom.ID, 3: 0}, speakers(t, s, ch.ID))
		assert.Equal(t, store.VoicingTelepathy, segs[7].Voicing)
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, got.QuoteCount)
	})
}

func TestNewAndInvalidSpeakers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[1]
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			vs := chapter1("new:Old Gregor", "c999999", "unknown", "unknown")
			vs[0]["gender"], vs[0]["emotion"] = "male", "happy"
			return reply(t, vs...)
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
		assert.Equal(t, 3, st.Unsure, "unknown speakers are flagged")

		// Re-attributing keeps a speaker chosen by hand (its paragraph is
		// not split anew) and matches a new speaker by name instead of
		// creating it again.
		require.NoError(t, s.SetQuoteSpeaker(ctx, ch.ID, 3, &f.tom.ID, "angry"))
		f.srv.Respond = func(llmtest.Request) llmtest.Reply {
			return reply(t, chapter1("new:Old Gregor", ref(f.anna), ref(f.anna), "new:Anna")...)
		}
		finishJobs(t, s)
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		require.NoError(t, f.a.Start(ctx, f.book, got, true, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: gregor.ID, 2: f.anna.ID, 3: f.tom.ID, 4: f.anna.ID}, speakers(t, s, ch.ID))
		n, err := s.CountCharacters(ctx, f.book.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 3, n)
	})
}

func TestLongChaptersInPartsAndResume(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		f.a.PartTokens = 10 // a part per paragraph
		ch := f.chapters[1]
		var numbered [][]string
		f.srv.Respond = func(r llmtest.Request) llmtest.Reply {
			var ps []string
			for _, m := range paraRe.FindAllStringSubmatch(userMessage(r), -1) {
				ps = append(ps, m[1])
			}
			numbered = append(numbered, ps)
			return reply(t, chapter1(ref(f.anna), ref(f.anna), ref(f.anna), ref(f.anna))...)
		}
		// A job that stopped after the first part (the heading and the
		// paragraph with quotes 1 and 2) resumes with the next paragraph.
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", ch.ID).Update("attribution_part", 2).Error)
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, [][]string{{"3"}, {"4"}, {"6"}}, numbered, "only the remaining parts")
		assert.Equal(t, map[int]int64{1: 0, 2: 0, 3: f.anna.ID, 4: f.anna.ID}, speakers(t, s, ch.ID),
			"each part takes only its own paragraphs' passages")
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, 6, got.AttributionPart)

		// From the start: every part, later ones with context.
		numbered = nil
		finishJobs(t, s)
		require.NoError(t, f.a.Start(ctx, f.book, got, true, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, [][]string{{"2"}, {"3"}, {"4"}, {"6"}}, numbered)
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
				return llmtest.Reply{Content: `{"voices": [{"p": 2`, FinishReason: "length"}
			}
			return reply(t, chapter1(ref(f.tom), ref(f.tom), ref(f.tom), ref(f.tom))...)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, map[int]int64{1: f.tom.ID, 2: f.tom.ID, 3: f.tom.ID, 4: f.tom.ID}, speakers(t, s, ch.ID))
		assert.Len(t, f.srv.Requests(), 3, "the cut-off request, then its halves")
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
			return reply(t, chapter1(ref(f.anna), ref(f.anna), "unknown")...)
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
			return reply(t, chapter1("new:Ghost", ref(f.anna), ref(f.tom))...)
		}
		require.NoError(t, f.a.Start(ctx, f.book, &ch, false, 0))
		require.NoError(t, f.a.Run(ctx, ch.ID, func(float64) {}), "a reset answer is dropped quietly")
		assert.Equal(t, map[int]int64{1: 0, 2: 0, 3: 0}, speakers(t, s, ch.ID), "the split from the import stays")
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
			return reply(t, chapter1(ref(f.anna), ref(f.anna), ref(f.tom))...)
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
			return reply(t, chapter1(ref(f.anna), ref(f.anna), ref(f.tom))...)
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
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", f.chapters[3].ID).
			Update("attribution_status", store.AttributionDone).Error)
		require.NoError(t, s.EnqueueJob(ctx, &store.Job{Type: "cast_book", Queue: "test-only", BookID: &f.book.ID}))
		require.NoError(t, r.Render(ctx, f.chapters[3].ID, func(float64) {}))
	})
}

func TestFailedRerendersBackOff(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		f := setup(t, s)
		ch := f.chapters[3] // no quotes
		require.NoError(t, s.SetChapterAudio(ctx, ch.ID, store.ChapterAudio{Path: "old.m4a", Voice: "af_heart@1.00"}))
		require.NoError(t, s.UpdateBook(ctx, f.book.ID, map[string]any{"NarratorVoice": "bf_emma", "Lookahead": 5}))
		book, err := s.GetBook(ctx, f.book.ID)
		require.NoError(t, err)
		sched := &render.Scheduler{Store: s, Jobs: f.jobs, DefaultVoice: "af_heart"}
		n, err := sched.RenderAhead(ctx, book, 3, 1)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "outdated audio is rendered again")
		// The re-render fails: the old audio stays, with its error, and the
		// lookahead leaves the chapter alone for a while.
		job := f.jobs.jobs[len(f.jobs.jobs)-1]
		require.NoError(t, s.FailJob(ctx, job.ID, "tts down"))
		r := &render.Renderer{Store: s, DataDir: f.dir, Log: slog.New(slog.DiscardHandler)}
		r.Handler().OnFail(ctx, &job, errors.New("tts down"))
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AudioReady, got.AudioStatus)
		assert.Equal(t, "tts down", got.AudioError)
		n, err = sched.RenderAhead(ctx, book, 3, 1)
		require.NoError(t, err)
		assert.Zero(t, n)
	})
}
