package prepare_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/prepare"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

type enqueuer struct{ s *store.Store }

func (e *enqueuer) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = prepare.Queue
	return e.s.EnqueueJob(ctx, j)
}

func setup(t *testing.T, s *store.Store) (*prepare.Preparer, *llmtest.Server, *store.Book, []store.Chapter) {
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
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"EpubPath": b.EpubPath, "PrepareChapters": true}))
	log := slog.New(slog.DiscardHandler)
	require.NoError(t, (&importer.Importer{Store: s, DataDir: dir, Log: log}).Import(ctx, b.ID, func(float64) {}))
	b, err = s.GetBook(ctx, b.ID)
	require.NoError(t, err)
	cs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 100)
	require.NoError(t, err)
	srv := llmtest.New(t)
	p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: srv.BaseURL(), Model: "m"}
	llm.Normalize(p)
	require.NoError(t, s.CreateLLMProvider(ctx, p))
	return &prepare.Preparer{Store: s, LLM: llm.NewPool(s, nil, log), Jobs: &enqueuer{s}, Log: log}, srv, b, cs
}

// finishJobs marks the queued jobs done (Run is called directly).
func finishJobs(t *testing.T, s *store.Store) {
	t.Helper()
	require.NoError(t, s.DB().Model(&store.Job{}).Where("status IN ?", []store.JobStatus{store.JobQueued, store.JobRunning}).
		Update("status", store.JobDone).Error)
}

func reply(t *testing.T, v map[string]any) llmtest.Reply {
	t.Helper()
	for _, k := range []string{"moods", "pauses", "spoken", "pronunciations", "flows"} {
		if v[k] == nil {
			v[k] = []any{}
		}
	}
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return llmtest.Reply{Content: string(raw)}
}

func TestPrepareChapter(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		pr, srv, b, cs := setup(t, s)
		ch := cs[1] // Chapter 1
		require.NoError(t, s.SaveLexiconEntry(ctx, &store.LexiconEntry{BookID: b.ID, Term: "Tom", Replacement: "Tom"}))
		srv.Respond = func(r llmtest.Request) llmtest.Reply {
			ms := r.Messages()
			msg, _ := ms[len(ms)-1]["content"].(string)
			assert.Contains(t, msg, "[p2] “Good morning,” said Anna. “Is it raining?”")
			assert.Contains(t, msg, "Pronunciations already known: Tom")
			assert.NotContains(t, msg, "* * *", "scene breaks are left out")
			return reply(t, map[string]any{
				"moods":  []any{map[string]any{"p": 6, "mood": "calm"}},
				"pauses": []any{map[string]any{"p": 2, "after": "said Anna.", "length": "long"}},
				"flows":  []any{3},
				"pronunciations": []any{
					map[string]any{"term": "Anna", "say": "ah-nah", "note": "a name"},
					map[string]any{"term": "Tom", "say": "tom", "note": "known"},
				},
			})
		}
		require.True(t, prepare.Needed(b, &ch))
		require.NoError(t, pr.Start(ctx, b, &ch, false, 0))
		require.NoError(t, pr.Run(ctx, ch.ID, func(float64) {}))

		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepDone, got.PrepStatus)
		assert.Equal(t, "prepare_chapter@v2", got.PrepPrompt)
		assert.Greater(t, got.VoiceRev, ch.VoiceRev)
		assert.False(t, prepare.Needed(b, got))
		paras, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, "calm", paras[5].Mood)
		assert.True(t, paras[2].Flows, "goes on from the paragraph before")
		assert.False(t, paras[1].Flows)
		var paused []string
		for _, sg := range segs {
			if sg.Pauses != "" {
				paused = append(paused, sg.Text+" "+sg.Pauses)
			}
		}
		assert.Equal(t, []string{"said Anna. [[10,1000]]"}, paused)
		st, err := s.PrepStats(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepCounts{Moods: 1, Pauses: 1, Flows: 1}, st)
		es, err := s.ListLexicon(ctx, b.ID)
		require.NoError(t, err)
		require.Len(t, es, 2)
		assert.Equal(t, "Anna", es[0].Term)
		assert.Equal(t, store.LexiconSuggested, es[0].Status)
		assert.Equal(t, "ah-nah", es[0].Replacement)

		// Again by force: what it found is discarded first; an answer
		// that does not fit leaves the chapter as it is.
		srv.Respond = func(llmtest.Request) llmtest.Reply { return llmtest.Reply{Content: "not json"} }
		require.ErrorIs(t, pr.Start(ctx, b, got, true, 0), prepare.ErrBusy, "while its job is pending")
		finishJobs(t, s)
		require.NoError(t, pr.Start(ctx, b, got, true, 0))
		require.NoError(t, pr.Run(ctx, ch.ID, func(float64) {}))
		st, err = s.PrepStats(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepCounts{}, st)
		got, err = s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepDone, got.PrepStatus)
		es, err = s.ListLexicon(ctx, b.ID)
		require.NoError(t, err)
		assert.Len(t, es, 2, "suggestions stay")

		// A book that does not ask for it needs none.
		b.PrepareChapters = false
		assert.False(t, prepare.Needed(b, &cs[2]))
	})
}

func TestPrepareFromScratchClearsLeftovers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		pr, srv, b, cs := setup(t, s)
		ch := cs[1]
		srv.Respond = func(llmtest.Request) llmtest.Reply { return reply(t, map[string]any{}) }
		// What an earlier preparation left (a merged chapter keeps it).
		paras, segs, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		require.NoError(t, s.DB().Model(&store.Paragraph{}).Where("id = ?", paras[1].ID).Update("mood", "tense").Error)
		require.NoError(t, s.DB().Model(&store.Segment{}).Where("id = ?", segs[1].ID).
			Update("pauses", "[[1,500]]").Error)
		require.NoError(t, pr.Start(ctx, b, &ch, false, 0))
		require.NoError(t, pr.Run(ctx, ch.ID, func(float64) {}))
		st, err := s.PrepStats(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepCounts{}, st)
	})
}

func TestPrepareResumesAndStopsWhenReset(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		pr, srv, b, cs := setup(t, s)
		pr.PartTokens = 10 // a part per paragraph
		ch := cs[1]
		calls := 0
		srv.Respond = func(llmtest.Request) llmtest.Reply {
			calls++
			if calls == 2 {
				// Reset while the second part is being prepared.
				require.NoError(t, s.ResetPreparation(ctx, ch.ID))
			}
			return reply(t, map[string]any{})
		}
		require.NoError(t, pr.Start(ctx, b, &ch, false, 0))
		require.NoError(t, pr.Run(ctx, ch.ID, func(float64) {}))
		assert.Equal(t, 2, calls, "the answer for a reset chapter is dropped and the job stops")
		got, err := s.GetChapter(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, store.PrepNone, got.PrepStatus)

		// A job that stopped after two parts resumes with the third.
		finishJobs(t, s)
		require.NoError(t, pr.Start(ctx, b, got, false, 0))
		require.NoError(t, s.DB().Model(&store.Chapter{}).Where("id = ?", ch.ID).
			Updates(map[string]any{"prep_status": store.PrepRunning, "prep_part": 2}).Error)
		calls = 10
		require.NoError(t, pr.Run(ctx, ch.ID, func(float64) {}))
		paras, _, err := s.ChapterText(ctx, ch.ID)
		require.NoError(t, err)
		assert.Equal(t, 10+len(paras)-2-1, calls, "one request per remaining paragraph but the scene break")
	})
}
