package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// analysisAnswer finds Anna and Tom in the sample book.
func analysisAnswer(r llmtest.Request) llmtest.Reply {
	ms := r.Messages()
	msg, _ := ms[len(ms)-1]["content"].(string)
	if strings.Contains(msg, "Pairs (") {
		return llmtest.Reply{Content: `{"decisions": []}`}
	}
	raw, _ := json.Marshal(map[string]any{
		"characters": []map[string]any{
			{"ref": "", "name": "Anna", "aliases": []string{"Annie"}, "gender": "female", "age": "adult",
				"role": "neighbour", "description": "Asks about the weather.", "speech_style": "polite",
				"importance": 3, "speaks": true, "chapters": []int{1}},
			{"ref": "", "name": "Tom", "aliases": []string{"Thomas", "Tommy"}, "gender": "male", "age": "adult",
				"role": "", "description": "", "speech_style": "", "importance": 4, "speaks": true, "chapters": []int{1, 2}},
		},
		"chapters": []map[string]any{{"n": 1, "narration": "third_person", "pov": "Tom"}},
	})
	return llmtest.Reply{Content: string(raw)}
}

func TestCharacterAnalysisAndEditing(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		fake := llmtest.New(t)
		fake.Respond = analysisAnswer
		p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: fake.BaseURL(), Model: "m"}
		llm.Normalize(p)
		require.NoError(t, s.CreateLLMProvider(ctx, p))

		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)

		var an api.Analysis
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/analysis", nil, &an))
		assert.Equal(t, api.AnalysisStatusNone, an.Status)
		assert.Equal(t, 1, an.BatchesTotal, "estimate before the start")
		assert.Positive(t, an.EstimatedTokens)
		assert.Equal(t, "character_analysis@v1", an.Prompt)

		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/analysis", nil, &an))
		require.Eventually(t, func() bool {
			require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/analysis", nil, &an))
			return an.Status == api.AnalysisStatusDone || an.Status == api.AnalysisStatusFailed
		}, 10*time.Second, 20*time.Millisecond)
		require.Equal(t, api.AnalysisStatusDone, an.Status, an.Error)
		assert.Equal(t, 1, an.BatchesDone)
		assert.EqualValues(t, 2, an.CharacterCount)
		assert.Positive(t, an.InputTokens)

		var b api.Book
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id, nil, &b))
		assert.Equal(t, api.AnalysisStatusDone, b.AnalysisStatus)
		require.NotNil(t, b.AnalysisProgress)
		assert.InDelta(t, 1.0, *b.AnalysisProgress, 0.001)

		var chars api.CharacterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/characters", nil, &chars))
		require.Len(t, chars.Items, 2)
		tom, anna := chars.Items[0], chars.Items[1]
		assert.Equal(t, "Tom", tom.Name, "most important first")
		assert.Equal(t, []string{"Thomas", "Tommy"}, tom.Aliases)
		assert.Equal(t, 2, tom.ChapterCount)
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/characters?search=annie", nil, &chars))
		require.Len(t, chars.Items, 1)
		assert.Equal(t, "Anna", chars.Items[0].Name)

		var tomDetail api.CharacterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/characters/"+itoa(tom.Id), nil, &tomDetail))
		require.Len(t, tomDetail.Chapters, 2)
		assert.Equal(t, "Chapter 1", tomDetail.Chapters[0].Title)

		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet,
			"/api/books/"+id+"/chapters?characterId="+itoa(anna.Id), nil, &chapters))
		require.Len(t, chapters.Items, 1)
		ch1 := chapters.Items[0]
		assert.Equal(t, api.NarrationThirdPerson, *ch1.Narration)
		assert.Equal(t, tom.Id, *ch1.PovCharacterId)
		var detail api.ChapterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/chapters/"+itoa(ch1.Id), nil, &detail))
		require.NotNil(t, detail.Characters)
		assert.Equal(t, []api.CharacterRef{{Id: tom.Id, Name: "Tom"}, {Id: anna.Id, Name: "Anna"}}, *detail.Characters)

		// Corrections.
		var updated api.CharacterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/characters/"+itoa(tom.Id), map[string]any{
			"name": "Tom Baker", "aliases": []string{"Tom", "Thomas", "Tommy", "tom"}, "speechStyle": "laconic",
			"ageGroup": "elderly",
		}, &updated))
		assert.Equal(t, "Tom Baker", updated.Name)
		assert.Equal(t, []string{"Tom", "Thomas", "Tommy"}, updated.Aliases)
		assert.Equal(t, api.AgeGroupElderly, updated.AgeGroup)
		assert.True(t, updated.UserEdited)
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/characters/"+itoa(tom.Id),
			map[string]any{"name": "!!"}, nil))

		var split api.CharacterDetail
		require.Equal(t, http.StatusCreated, c.json(http.MethodPost, "/api/characters/"+itoa(tom.Id)+"/split",
			map[string]any{"name": "Tommy", "aliases": []string{"Thomas"}}, &split))
		assert.Equal(t, []string{"Thomas"}, split.Aliases)
		assert.Len(t, split.Chapters, 2)
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/characters/"+itoa(tom.Id)+"/split",
			map[string]any{"name": "Tom Baker"}, nil))

		var merged api.CharacterDetail
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/books/"+id+"/characters/merge",
			map[string]any{"targetId": tom.Id, "sourceIds": []int64{split.Id}}, &merged))
		assert.Contains(t, merged.Aliases, "Tommy")
		assert.Contains(t, merged.Aliases, "Thomas")
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/books/"+id+"/characters/merge",
			map[string]any{"targetId": tom.Id, "sourceIds": []int64{tom.Id}}, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodPost, "/api/books/"+id+"/characters/merge",
			map[string]any{"targetId": 999999, "sourceIds": []int64{anna.Id}}, nil))

		require.Equal(t, http.StatusNoContent, c.json(http.MethodDelete, "/api/characters/"+itoa(anna.Id), nil, nil))
		require.Equal(t, http.StatusNotFound, c.json(http.MethodGet, "/api/characters/"+itoa(anna.Id), nil, nil))

		// While an analysis runs, chapters and characters cannot be
		// restructured.
		require.NoError(t, s.UpdateBook(ctx, book.Id, map[string]any{"AnalysisStatus": store.AnalysisRunning}))
		var e api.Error
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/merge-next", nil, &e))
		assert.Contains(t, e.Message, "pause it first")
		require.Equal(t, http.StatusConflict, c.json(http.MethodDelete, "/api/characters/"+itoa(tom.Id), nil, nil))
		require.Equal(t, http.StatusConflict, c.json(http.MethodDelete, "/api/books/"+id, nil, nil))
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/books/"+id+"/analysis/pause", nil, &an))
		assert.Equal(t, api.AnalysisStatusPaused, an.Status)
		require.Equal(t, http.StatusOK, c.json(http.MethodPost, "/api/chapters/"+itoa(ch1.Id)+"/merge-next", nil, nil))

		// Restart discards everything.
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/analysis",
			map[string]any{"restart": true}, &an))
		assert.EqualValues(t, 0, an.CharacterCount)
	})
}

func TestAnalysisGuards(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)
		ids, err := s.ChapterIDs(ctx, book.Id)
		require.NoError(t, err)
		ch := itoa(ids[1])

		// A paused analysis with pending batches: chapters stay as planned.
		require.NoError(t, s.CreateAnalysisBatches(ctx, []store.AnalysisBatch{
			{BookID: book.Id, FirstChapterIdx: 1, Parts: `[{"chapterId":` + ch + `}]`, Tokens: 10},
		}))
		require.NoError(t, s.UpdateBook(ctx, book.Id, map[string]any{"AnalysisStatus": store.AnalysisPaused}))
		var e api.Error
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, "/api/chapters/"+ch+"/merge-next", nil, &e))
		assert.Contains(t, e.Message, "not finished")

		// Validation of corrections.
		char := &store.Character{BookID: book.Id, Name: "Anna"}
		require.NoError(t, s.CreateCharacter(ctx, char))
		cid := itoa(char.ID)
		for _, body := range []map[string]any{
			{"gender": "x"}, {"ageGroup": "ancient"}, {"importance": 9}, {"importance": 0},
			{"description": strings.Repeat("x", 1001)}, {"aliases": []string{strings.Repeat("y", 121)}},
		} {
			assert.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, "/api/characters/"+cid, body, nil), body)
		}
		require.Equal(t, http.StatusBadRequest, c.json(http.MethodPost, "/api/books/"+id+"/characters/merge",
			map[string]any{"targetId": char.ID, "sourceIds": []int64{}}, nil))

		// While a job still runs (e.g. finishing a batch after a pause),
		// nothing may change under it.
		j := &store.Job{Type: "analyze_book", Queue: "llm", BookID: &book.Id}
		require.NoError(t, s.EnqueueJob(ctx, j))
		_, err = s.ClaimJob(ctx, "llm", time.Now().UTC())
		require.NoError(t, err)
		require.Equal(t, http.StatusConflict, c.json(http.MethodPatch, "/api/characters/"+cid,
			map[string]any{"role": "x"}, nil))
		require.Equal(t, http.StatusConflict, c.json(http.MethodDelete, "/api/books/"+id, nil, nil))
		require.NoError(t, s.FinishJob(ctx, j.ID))

		// A status left "running" without a job is reported as paused.
		require.NoError(t, s.UpdateBook(ctx, book.Id, map[string]any{"AnalysisStatus": store.AnalysisRunning}))
		var an api.Analysis
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/analysis", nil, &an))
		assert.Equal(t, api.AnalysisStatusPaused, an.Status)
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, "/api/characters/"+cid,
			map[string]any{"role": "neighbour", "importance": 2}, nil))
	})
}
