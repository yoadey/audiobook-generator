package analysis_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// setup imports the sample book: Copyright (excluded), Chapter 1 (Anna and
// Tom talk), Chapter 2 (Tom says bye), Chapter 3 (one long paragraph).
func setup(t *testing.T, s *store.Store) (*store.Book, []store.Chapter) {
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
	im := &importer.Importer{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}
	require.NoError(t, im.Import(ctx, b.ID, func(float64) {}))
	b, err = s.GetBook(ctx, b.ID)
	require.NoError(t, err)
	cs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
	require.NoError(t, err)
	require.Len(t, cs, 4)
	return b, cs
}

type enqueuer struct {
	s    *store.Store
	jobs []store.Job
}

func (e *enqueuer) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = analysis.Queue
	if err := e.s.EnqueueJob(ctx, j); err != nil {
		return err
	}
	e.jobs = append(e.jobs, *j)
	return nil
}

func newAnalyzer(t *testing.T, s *store.Store) (*analysis.Analyzer, *llmtest.Server) {
	t.Helper()
	srv := llmtest.New(t)
	p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: srv.BaseURL(), Model: "fake-small"}
	llm.Normalize(p)
	require.NoError(t, s.CreateLLMProvider(context.Background(), p))
	log := slog.New(slog.DiscardHandler)
	return &analysis.Analyzer{
		Store: s, LLM: llm.NewPool(s, nil, log), Jobs: &enqueuer{s: s}, Log: log,
	}, srv
}

type char struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Gender      string   `json:"gender"`
	Age         string   `json:"age"`
	Role        string   `json:"role"`
	Description string   `json:"description"`
	SpeechStyle string   `json:"speech_style"`
	Importance  int      `json:"importance"`
	Speaks      bool     `json:"speaks"`
	Chapters    []int    `json:"chapters"`
}

type chapterInfo struct {
	N         int    `json:"n"`
	Narration string `json:"narration"`
	POV       string `json:"pov"`
}

func answer(t *testing.T, cs []char, chs ...chapterInfo) llmtest.Reply {
	t.Helper()
	if cs == nil {
		cs = []char{}
	}
	for i := range cs {
		c := &cs[i]
		if c.Aliases == nil {
			c.Aliases = []string{}
		}
		if c.Chapters == nil {
			c.Chapters = []int{}
		}
		if c.Gender == "" {
			c.Gender = "unknown"
		}
		if c.Age == "" {
			c.Age = "unknown"
		}
		c.Importance = max(c.Importance, 1)
	}
	if chs == nil {
		chs = []chapterInfo{}
	}
	raw, err := json.Marshal(map[string]any{"characters": cs, "chapters": chs})
	require.NoError(t, err)
	return llmtest.Reply{Content: string(raw)}
}

func userMessage(r llmtest.Request) string {
	ms := r.Messages()
	c, _ := ms[len(ms)-1]["content"].(string)
	return c
}

func characters(t *testing.T, s *store.Store, bookID int64) map[string]store.Character {
	t.Helper()
	cs, _, err := s.ListCharacters(context.Background(), bookID, store.CharacterFilter{}, 0, 100)
	require.NoError(t, err)
	out := map[string]store.Character{}
	for _, c := range cs {
		out[c.Name] = c
	}
	return out
}

func TestAnalyzeBook(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, chs := setup(t, s)
		a, srv := newAnalyzer(t, s)

		est, err := a.EstimateBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, est.Batches)
		assert.Positive(t, est.Tokens)

		require.NoError(t, a.Start(ctx, b, false))
		require.NoError(t, a.Start(ctx, b, false), "starting twice queues one job")
		assert.Len(t, a.Jobs.(*enqueuer).jobs, 1)

		srv.Enqueue(answer(t, []char{
			{Name: "Anna", Gender: "female", Age: "adult", Role: "neighbour", Description: "Greets Tom.",
				SpeechStyle: "cheerful", Importance: 3, Speaks: true, Chapters: []int{1}},
			{Name: "Tom", Aliases: []string{"Tommy", "he"}, Gender: "male", Age: "adult", Importance: 4,
				Speaks: true, Chapters: []int{1, 2, 9}},
		}, chapterInfo{1, "third_person", "Anna"}, chapterInfo{2, "first_person", "Tom"}, chapterInfo{3, "unknown", ""}))
		var progress []float64
		require.NoError(t, a.Run(ctx, b.ID, func(p float64) { progress = append(progress, p) }))

		reqs := srv.Requests()
		require.Len(t, reqs, 1, "one batch, no merge candidates")
		msg := userMessage(reqs[0])
		assert.Contains(t, msg, "No characters are known yet.")
		assert.Contains(t, msg, "=== [1] Chapter 1 ===")
		assert.Contains(t, msg, "“Good morning,” said Anna.")
		assert.Contains(t, msg, "* * *")
		assert.Contains(t, msg, "=== [3] Chapter 3 ===")
		assert.NotContains(t, msg, "Copyright 2026", "excluded chapters are not analysed")
		assert.Equal(t, "json_schema", reqs[0].Body["response_format"].(map[string]any)["type"])
		assert.EqualValues(t, 8192, reqs[0].Body["max_tokens"])

		cs := characters(t, s, b.ID)
		require.Len(t, cs, 2)
		tom := cs["Tom"]
		assert.Equal(t, store.GenderMale, tom.Gender)
		assert.Equal(t, 2, tom.ChapterCount, "unknown chapter numbers are ignored")
		assert.Equal(t, chs[1].Idx, tom.FirstChapterIdx)
		assert.True(t, tom.Speaks)
		as, err := s.CharacterAliases(ctx, tom.ID)
		require.NoError(t, err)
		require.Len(t, as[tom.ID], 2)
		assert.Equal(t, "Tommy", as[tom.ID][0].Alias)
		assert.Equal(t, "cheerful", cs["Anna"].SpeechStyle)

		ch1, err := s.GetChapter(ctx, chs[1].ID)
		require.NoError(t, err)
		assert.Equal(t, store.NarrationThirdPerson, ch1.Narration)
		assert.Equal(t, cs["Anna"].ID, *ch1.POVCharacterID)
		ch3, err := s.GetChapter(ctx, chs[3].ID)
		require.NoError(t, err)
		assert.Equal(t, store.NarrationUnknown, ch3.Narration)
		assert.Nil(t, ch3.POVCharacterID)

		got, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisDone, got.AnalysisStatus)
		assert.NotNil(t, got.AnalysisStartedAt)
		assert.NotNil(t, got.AnalysisFinishedAt)
		st, err := s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, st.Done)
		assert.Positive(t, st.InputTokens)
		assert.NotEmpty(t, progress)
	})
}

func TestAnalysisReusesKnownCharactersAndMergesDuplicates(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, _ := setup(t, s)
		a, srv := newAnalyzer(t, s)
		a.BatchTokens = 40 // chapter 1 (~36 tokens) and 2 (~7) apart; chapter 3 split
		require.NoError(t, a.Start(ctx, b, false))

		tomRef := regexp.MustCompile(`(c\d+): Tom`)
		srv.Respond = func(r llmtest.Request) llmtest.Reply {
			msg := userMessage(r)
			switch {
			case strings.Contains(msg, "Pairs ("):
				return llmtest.Reply{Content: `{"decisions": [{"pair": "p1", "same": true}]}`}
			case strings.Contains(msg, "=== [1] Chapter 1 ==="):
				return answer(t, []char{
					{Name: "Anna", Gender: "female", Importance: 3, Speaks: true, Chapters: []int{1}},
					{Name: "Tom", Gender: "male", Importance: 3, Speaks: true, Chapters: []int{1}},
				})
			case strings.Contains(msg, "=== [1] Chapter 2 ==="):
				// The model gets Tom's id and uses it; it also reports
				// Anna under her full name, which the matching misses.
				m := tomRef.FindStringSubmatch(msg)
				require.NotNil(t, m, msg)
				return answer(t, []char{
					{Ref: m[1], Name: "Tom", Aliases: []string{"Thomas"}, Gender: "male",
						Importance: 4, Speaks: true, Chapters: []int{1}},
					{Name: "Anna Smith", Gender: "female", Role: "neighbour", Importance: 2, Chapters: []int{1}},
				})
			}
			return answer(t, nil)
		}
		require.NoError(t, a.Run(ctx, b.ID, func(float64) {}))

		reqs := srv.Requests()
		require.Len(t, reqs, 5, "four batches (chapter 3 in two parts) and one merge request")
		assert.Contains(t, userMessage(reqs[1]), ": Tom", "known characters the text mentions")
		assert.NotContains(t, userMessage(reqs[1]), "Anna", "Anna is not mentioned in chapter 2")
		merge := userMessage(reqs[4])
		assert.Contains(t, merge, "p1:")
		assert.Contains(t, merge, "Anna Smith")

		cs := characters(t, s, b.ID)
		require.Len(t, cs, 2)
		assert.Contains(t, cs, "Anna Smith", "the fuller name survives")
		assert.Equal(t, 4, cs["Tom"].Importance)
		as, err := s.CharacterAliases(ctx, cs["Tom"].ID, cs["Anna Smith"].ID)
		require.NoError(t, err)
		assert.Equal(t, "Thomas", as[cs["Tom"].ID][0].Alias)
		assert.Equal(t, "Anna", as[cs["Anna Smith"].ID][0].Alias)
		assert.Equal(t, 2, cs["Tom"].ChapterCount)
		assert.Equal(t, 2, cs["Anna Smith"].ChapterCount)
	})
}

func TestAnalysisWaitsForQuotaAndResumes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, _ := setup(t, s)
		a, srv := newAnalyzer(t, s)
		require.NoError(t, a.Start(ctx, b, false))

		srv.Enqueue(llmtest.Reply{Status: 429, Header: map[string]string{"Retry-After": "3600"}, Body: `{"error":"quota"}`})
		err := a.Run(ctx, b.ID, func(float64) {})
		var d jobs.Deferrer
		require.ErrorAs(t, err, &d, "the job waits instead of failing")
		st, err := s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, st.Total)
		assert.Zero(t, st.Done)

		// Later (the provider's cooldown has passed for a new pool).
		a.LLM = llm.NewPool(s, nil, slog.New(slog.DiscardHandler))
		srv.Enqueue(answer(t, []char{{Name: "Anna", Gender: "female", Importance: 2, Chapters: []int{1}}}))
		require.NoError(t, a.Run(ctx, b.ID, func(float64) {}))
		assert.Len(t, characters(t, s, b.ID), 1)
	})
}

func TestAnalysisSplitsTruncatedBatches(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, _ := setup(t, s)
		a, srv := newAnalyzer(t, s)
		require.NoError(t, a.Start(ctx, b, false))

		srv.Enqueue(
			llmtest.Reply{Content: `{"characters": [{"name": "An`, FinishReason: "length"},
			answer(t, []char{{Name: "Anna", Gender: "female", Importance: 2, Chapters: []int{1}}}),
			answer(t, []char{{Name: "Tom", Gender: "male", Importance: 2, Chapters: []int{1, 2}}}),
		)
		require.NoError(t, a.Run(ctx, b.ID, func(float64) {}))
		st, err := s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, st.Total)
		assert.Equal(t, 2, st.Done)
		reqs := srv.Requests()
		require.Len(t, reqs, 3)
		assert.Contains(t, userMessage(reqs[1]), "Chapters (1)")
		assert.Contains(t, userMessage(reqs[2]), "Chapters (2)")
		assert.True(t, strings.Contains(userMessage(reqs[2]), "=== [1] Chapter 2 ==="))
		cs := characters(t, s, b.ID)
		assert.Equal(t, 2, cs["Tom"].ChapterCount)
	})
}

func TestAnalysisSplitsHugeChapters(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, chs := setup(t, s)
		a, srv := newAnalyzer(t, s)
		a.BatchTokens = 8 // smaller than every paragraph
		require.NoError(t, a.Start(ctx, b, false))
		for range 20 {
			srv.Enqueue(answer(t, nil, chapterInfo{1, "third_person", ""}))
		}
		require.NoError(t, a.Run(ctx, b.ID, func(float64) {}))
		st, err := s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Greater(t, st.Total, 3, "chapters are split at paragraphs")
		assert.Equal(t, st.Total, st.Done)
		found := false
		for _, r := range srv.Requests() {
			found = found || strings.Contains(userMessage(r), "(part: paragraphs")
		}
		assert.True(t, found)
		ch1, err := s.GetChapter(ctx, chs[1].ID)
		require.NoError(t, err)
		assert.Equal(t, store.NarrationThirdPerson, ch1.Narration, "set by the part with the chapter's beginning")
	})
}

func TestPauseAndRestart(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, _ := setup(t, s)
		a, srv := newAnalyzer(t, s)
		require.NoError(t, a.Start(ctx, b, false))
		b, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisQueued, b.AnalysisStatus)

		require.NoError(t, a.Pause(ctx, b))
		b, err = s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisPaused, b.AnalysisStatus)
		job, err := s.GetJob(ctx, a.Jobs.(*enqueuer).jobs[0].ID)
		require.NoError(t, err)
		assert.Equal(t, store.JobCanceled, job.Status)
		require.NoError(t, a.Run(ctx, b.ID, func(float64) {}), "a paused analysis does nothing")
		assert.Empty(t, srv.Requests())

		// Results so far survive a resume, not a restart.
		require.NoError(t, s.CreateCharacter(ctx, &store.Character{BookID: b.ID, Name: "Manual"}))
		require.NoError(t, a.Start(ctx, b, false))
		assert.Len(t, characters(t, s, b.ID), 1)
		require.ErrorIs(t, a.Start(ctx, b, true), analysis.ErrBusy, "not while a job is pending")
		require.NoError(t, a.Pause(ctx, b))
		require.NoError(t, a.Start(ctx, b, true))
		assert.Empty(t, characters(t, s, b.ID))

		importing := &store.Book{Title: "x", Status: store.BookImporting}
		require.NoError(t, s.CreateBook(ctx, importing))
		require.ErrorIs(t, a.Start(ctx, importing, false), store.ErrInvalid)
	})
}
