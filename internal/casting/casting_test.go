package casting

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

func TestRecipes(t *testing.T) {
	r, err := ParseRecipe("bf_emma(2)+af_bella(1)")
	require.NoError(t, err)
	assert.Equal(t, "bf_emma(0.67)+af_bella(0.33)", r.String())
	assert.Equal(t, "bf_emma", r.Main())
	r, err = ParseRecipe(" af_heart ")
	require.NoError(t, err)
	assert.Equal(t, "af_heart", r.String())
	r, err = ParseRecipe("af_sky(1)+af_sky(1)+am_adam(2)")
	require.NoError(t, err)
	assert.Equal(t, "af_sky(0.5)+am_adam(0.5)", r.String())
	for _, bad := range []string{"", "heart", "af_heart(0)", "af_heart+", "AF_heart", "a(1)+b(1)",
		"af_a+af_b+af_c+af_d+af_e"} {
		_, err := ParseRecipe(bad)
		assert.ErrorIs(t, err, ErrRecipe, bad)
	}

	// Rounding never leaves the parts out of order: the string is stable.
	for _, in := range []string{"af_x(1)+af_y(7)+af_z(7)", "af_x(0.335)+af_y(0.335)+af_z(0.33)"} {
		r, err := ParseRecipe(in)
		require.NoError(t, err)
		again, err := ParseRecipe(r.String())
		require.NoError(t, err)
		assert.Equal(t, r.String(), again.String(), in)
		for i := 1; i < len(r); i++ {
			assert.GreaterOrEqual(t, r[i-1].Weight, r[i].Weight, in)
		}
	}
	_, err = ParseRecipe(strings.Repeat("a", 201))
	assert.ErrorIs(t, err, ErrRecipe)

	a, _ := ParseRecipe("bf_emma(0.7)+af_bella(0.3)")
	b, _ := ParseRecipe("bf_emma")
	assert.InDelta(t, 0.3, Distance(a, 1, b, 1), 1e-9)
	assert.InDelta(t, 0.4, Distance(a, 1, b, 1.1), 1e-9)
	assert.InDelta(t, 0.15, Distance(b, 1, b, 1.8), 1e-9, "speed alone does not make a voice distinct")
	assert.InDelta(t, 1, Distance(b, 1, Recipe{{Voice: "af_sky", Weight: 1}}, 1), 1e-9)
	assert.InDelta(t, 0, Distance(a, 1, a, 1), 1e-9)
}

func available() []BaseVoice {
	out := assumed()
	return out
}

func TestPoolVoices(t *testing.T) {
	vs := available()
	narrator, _ := ParseRecipe("am_michael")
	seen := map[string]bool{}
	for id := range 200 {
		r, speed := PoolVoice(vs, narrator, store.GenderMale, store.AgeAdult, strconv.Itoa(id))
		require.Len(t, r, 1)
		v := describe(r.Main())
		assert.Equal(t, store.GenderMale, v.Gender)
		assert.NotEqual(t, "am_michael", r.Main(), "the narrator's voice is kept for the narrator")
		assert.InDelta(t, 1, speed, 1e-9)
		seen[r.Main()] = true
	}
	assert.Greater(t, len(seen), 5, "minor characters are spread over the pool")
	r1, _ := PoolVoice(vs, narrator, store.GenderFemale, store.AgeChild, "42")
	r2, s2 := PoolVoice(vs, narrator, store.GenderFemale, store.AgeChild, "42")
	assert.Equal(t, r1, r2, "deterministic")
	assert.Greater(t, s2, 1.0, "children speak faster")
	assert.True(t, suits(lookup(t, r1.Main()), store.AgeChild))
	r, _ := PoolVoice(nil, narrator, store.GenderMale, store.AgeAdult, "1")
	assert.Nil(t, r)

	c := &store.Character{ID: 7, Gender: store.GenderMale, AgeGroup: store.AgeElderly}
	v := Effective(c, vs, "am_michael", 1)
	assert.Equal(t, SourcePool, v.Source)
	assert.InDelta(t, 0.92, v.Speed, 1e-9)
	c.VoiceRecipe, c.VoiceSpeed = "bm_george", 0.9
	assert.Equal(t, Voice{Recipe: "bm_george", Speed: 0.9, Source: SourceOwn}, Effective(c, vs, "am_michael", 1))
	assert.Equal(t, SourceNarrator, Effective(&store.Character{ID: 1}, nil, "af_heart", 1).Source)
}

func lookup(t *testing.T, id string) BaseVoice {
	t.Helper()
	for _, v := range catalog {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("voice %s not in the catalogue", id)
	return BaseVoice{}
}

func prop(speed float64, parts ...any) *proposal {
	p := &proposal{Speed: speed, Reason: "fits"}
	for i := 0; i < len(parts); i += 2 {
		p.Parts = append(p.Parts, struct {
			Voice  string  `json:"voice"`
			Weight float64 `json:"weight"`
		}{parts[i].(string), parts[i+1].(float64)})
	}
	return p
}

func TestChoose(t *testing.T) {
	vs := Castable(available())
	narrator, _ := ParseRecipe("af_heart")
	erin := &store.Character{ID: 1, Name: "Erin", Gender: store.GenderFemale, AgeGroup: store.AgeAdult}

	r, speed, note := choose(vs, erin, prop(1.05, "bf_emma", 0.7, "af_bella", 0.3), narrator, 1, nil)
	assert.Equal(t, "bf_emma(0.7)+af_bella(0.3)", r.String())
	assert.InDelta(t, 1.05, speed, 1e-9)
	assert.Equal(t, "fits", note)

	// Too close to the narrator: the nearest fitting blend instead.
	r, _, note = choose(vs, erin, prop(1, "af_heart", 0.8, "af_bella", 0.2), narrator, 1, nil)
	assert.GreaterOrEqual(t, Distance(r, 1, narrator, 1), NarratorDistance)
	assert.Contains(t, note, "adjusted")

	// Male voices for a woman are dropped, speeds clamped.
	r, speed, _ = choose(vs, erin, prop(3, "am_adam", 0.5, "bf_emma", 0.5), narrator, 1, nil)
	assert.Equal(t, "bf_emma", r.String())
	assert.InDelta(t, MaxSpeed, speed, 1e-9)

	// Taken recipes are avoided; without a proposal the best grade wins.
	used := []taken{{"Lyonette", Recipe{{Voice: "af_bella", Weight: 1}}, 1}}
	r, _, note = choose(vs, erin, nil, narrator, 1, used)
	assert.Equal(t, "chosen by rule", note)
	assert.GreaterOrEqual(t, Distance(r, 1, used[0].recipe, 1), MinDistance)
	assert.GreaterOrEqual(t, Distance(r, 1, narrator, 1), NarratorDistance)
	for _, p := range r {
		assert.Equal(t, store.GenderFemale, lookup(t, p.Voice).Gender)
	}

	// Many characters: all distinct.
	for i := range 60 {
		ch := &store.Character{ID: int64(i + 2), Name: "x", Gender: store.GenderMale, AgeGroup: store.AgeAdult}
		r, speed, _ := choose(vs, ch, nil, narrator, 1, used)
		require.NotNil(t, r)
		for _, u := range used {
			if u.name == "Lyonette" {
				continue
			}
			require.GreaterOrEqual(t, Distance(r, speed, u.recipe, u.speed), MinDistance, "%d: %s vs %s", i, r, u.recipe)
		}
		used = append(used, taken{"m", r, speed})
	}
}

type fakeVoices struct {
	ids []string
	err error
	n   int
}

func (f *fakeVoices) Voices(context.Context) ([]tts.Voice, error) {
	f.n++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]tts.Voice, len(f.ids))
	for i, id := range f.ids {
		out[i] = tts.Voice{ID: id}
	}
	return out, nil
}

func TestCatalog(t *testing.T) {
	f := &fakeVoices{ids: []string{"af_heart", "am_michael", "zf_xiaobei", "bf_newvoice"}}
	c := NewCatalog(f)
	vs, err := c.Voices(context.Background())
	require.NoError(t, err)
	byID := map[string]BaseVoice{}
	for _, v := range vs {
		byID[v.ID] = v
	}
	assert.True(t, byID["af_heart"].Available)
	assert.False(t, byID["af_bella"].Available, "not offered by the server")
	assert.Equal(t, BaseVoice{ID: "bf_newvoice", Gender: store.GenderFemale, Accent: "en-GB", Available: true}, byID["bf_newvoice"])
	ids := []string{}
	for _, v := range Castable(vs) {
		ids = append(ids, v.ID)
	}
	assert.ElementsMatch(t, []string{"af_heart", "am_michael", "bf_newvoice"}, ids)
	_, _ = c.Voices(context.Background())
	assert.Equal(t, 1, f.n, "cached")

	f2 := &fakeVoices{err: errors.New("down")}
	c2 := NewCatalog(f2)
	vs, err = c2.Voices(context.Background())
	require.Error(t, err)
	assert.Len(t, Castable(vs), len(catalog), "the catalogue is assumed")
	_, _ = c2.Voices(context.Background())
	assert.Equal(t, 1, f2.n, "failures are remembered briefly")
}

// setup imports the sample book and marks its analysis done.
func setup(t *testing.T, s *store.Store) *store.Book {
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
	require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"AnalysisStatus": store.AnalysisDone}))
	b, err = s.GetBook(ctx, b.ID)
	require.NoError(t, err)
	return b
}

type enqueuer struct {
	s    *store.Store
	jobs []store.Job
}

func (e *enqueuer) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = Queue
	if err := e.s.EnqueueJob(ctx, j); err != nil {
		return err
	}
	e.jobs = append(e.jobs, *j)
	return nil
}

func newCaster(t *testing.T, s *store.Store) (*Caster, *llmtest.Server) {
	t.Helper()
	srv := llmtest.New(t)
	p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: srv.BaseURL(), Model: "fake-small"}
	llm.Normalize(p)
	require.NoError(t, s.CreateLLMProvider(context.Background(), p))
	log := slog.New(slog.DiscardHandler)
	ids := make([]string, len(catalog))
	for i, v := range catalog {
		ids[i] = v.ID
	}
	return &Caster{
		Store: s, LLM: llm.NewPool(s, nil, log), Jobs: &enqueuer{s: s}, Log: log,
		Catalog: NewCatalog(&fakeVoices{ids: ids}), DefaultVoice: "af_heart", MainCharacters: 3,
	}, srv
}

func finishJobs(t *testing.T, s *store.Store) {
	t.Helper()
	require.NoError(t, s.DB().Model(&store.Job{}).Where("status IN ?", []store.JobStatus{store.JobQueued, store.JobRunning}).
		Update("status", store.JobDone).Error)
}

func castAnswer(t *testing.T, vs ...map[string]any) llmtest.Reply {
	t.Helper()
	if vs == nil {
		vs = []map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"voices": vs})
	require.NoError(t, err)
	return llmtest.Reply{Content: string(raw)}
}

func ref(c *store.Character) string { return "c" + strconv.FormatInt(c.ID, 10) }

func TestCastBook(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		c, srv := newCaster(t, s)
		mk := func(name string, g store.Gender, imp int, speaks bool) *store.Character {
			ch := &store.Character{BookID: b.ID, Name: name, Gender: g, AgeGroup: store.AgeAdult, Importance: imp, Speaks: speaks}
			require.NoError(t, s.CreateCharacter(ctx, ch))
			return ch
		}
		anna := mk("Anna", store.GenderFemale, 5, true)
		tom := mk("Tom", store.GenderMale, 4, true)
		ben := mk("Ben", store.GenderMale, 3, true)
		mute := mk("Mute", store.GenderMale, 5, false)
		extra := mk("Extra", store.GenderFemale, 1, true)
		manual := mk("Manual", store.GenderFemale, 4, true)
		require.NoError(t, s.UpdateCharacter(ctx, manual.ID, map[string]any{
			"VoiceRecipe": "bf_emma", "VoiceManual": true, "Tier": store.TierMinor, "TierManual": true,
		}))

		srv.Respond = func(r llmtest.Request) llmtest.Reply {
			ms := r.Messages()
			msg, _ := ms[len(ms)-1]["content"].(string)
			assert.Contains(t, msg, "Narrator: af_heart, speed 1.00")
			assert.Contains(t, msg, "Manual: bf_emma", "taken voices are listed")
			return castAnswer(t,
				map[string]any{"ref": ref(anna), "parts": []map[string]any{{"voice": "bf_emma", "weight": 0.5}, {"voice": "af_bella", "weight": 0.5}}, "speed": 1, "reason": "warm"},
				// Tom's proposal is a female voice: replaced by rule.
				map[string]any{"ref": ref(tom), "parts": []map[string]any{{"voice": "af_sky", "weight": 1}}, "speed": 1, "reason": "x"},
				map[string]any{"ref": "c999", "parts": []map[string]any{{"voice": "am_adam", "weight": 1}}, "speed": 1, "reason": "unknown ref"},
			)
		}
		require.ErrorIs(t, c.Start(ctx, &store.Book{ID: b.ID, AnalysisStatus: store.AnalysisRunning}, false), store.ErrInvalid)
		require.NoError(t, c.Start(ctx, b, false))
		require.NoError(t, c.Run(ctx, b.ID, func(float64) {}))

		get := func(ch *store.Character) *store.Character {
			got, err := s.GetCharacter(ctx, ch.ID)
			require.NoError(t, err)
			return got
		}
		assert.Equal(t, store.TierMain, get(anna).Tier)
		assert.Equal(t, "af_bella(0.5)+bf_emma(0.5)", get(anna).VoiceRecipe)
		assert.Equal(t, "warm", get(anna).VoiceNote)
		tomVoice, err := ParseRecipe(get(tom).VoiceRecipe)
		require.NoError(t, err)
		for _, p := range tomVoice {
			assert.Equal(t, store.GenderMale, lookup(t, p.Voice).Gender)
		}
		assert.Equal(t, store.TierMain, get(ben).Tier, "top 3 speaking characters")
		assert.NotEmpty(t, get(ben).VoiceRecipe)
		assert.Equal(t, store.TierMinor, get(mute).Tier, "does not speak")
		assert.Equal(t, store.TierMinor, get(extra).Tier)
		assert.Empty(t, get(extra).VoiceRecipe)
		assert.Equal(t, "bf_emma", get(manual).VoiceRecipe, "the user's choice stays")
		assert.Equal(t, store.TierMinor, get(manual).Tier)

		got, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.CastingDone, got.CastingStatus)
		counts, err := s.CastingCounts(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.CastingCounts{Main: 3, Minor: 3, Voiced: 3}, counts)

		// A promoted character is cast on the next run; others keep theirs.
		require.NoError(t, s.UpdateCharacter(ctx, extra.ID, map[string]any{"Tier": store.TierMain, "TierManual": true}))
		annaVoice := get(anna).VoiceRecipe
		srv.Respond = func(llmtest.Request) llmtest.Reply {
			return llmtest.Reply{Status: 400, Body: `{"error":"bad request"}`}
		}
		require.NoError(t, c.Start(ctx, got, false))
		require.NoError(t, c.Run(ctx, b.ID, func(float64) {}))
		assert.NotEmpty(t, get(extra).VoiceRecipe, "cast by rule when the LLM refuses")
		assert.Equal(t, annaVoice, get(anna).VoiceRecipe)

		// A restart recasts all but the user's choices.
		srv.Respond = func(llmtest.Request) llmtest.Reply { return castAnswer(t) }
		got, err = s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		finishJobs(t, s)
		require.NoError(t, c.Start(ctx, got, true))
		assert.Empty(t, get(anna).VoiceRecipe)
		assert.Equal(t, store.TierMain, get(extra).Tier, "chosen by hand")
		require.NoError(t, c.Run(ctx, b.ID, func(float64) {}))
		assert.NotEmpty(t, get(anna).VoiceRecipe)
		assert.Equal(t, "bf_emma", get(manual).VoiceRecipe)

		// All main recipes are distinct and away from the narrator.
		chars, err := s.BookCharacters(ctx, b.ID)
		require.NoError(t, err)
		var rs []Recipe
		narrator, _ := ParseRecipe("af_heart")
		for _, ch := range chars {
			if ch.VoiceRecipe == "" || ch.VoiceManual {
				continue
			}
			r, err := ParseRecipe(ch.VoiceRecipe)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, Distance(r, ch.VoiceSpeed, narrator, 1), NarratorDistance, ch.Name)
			for _, o := range rs {
				assert.GreaterOrEqual(t, Distance(r, ch.VoiceSpeed, o, 1)+0.5, MinDistance)
			}
			rs = append(rs, r)
		}
	})
}

func TestPromotionWhileCasting(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		c, srv := newCaster(t, s)
		anna := &store.Character{BookID: b.ID, Name: "Anna", Gender: store.GenderFemale, Speaks: true, Importance: 5}
		tom := &store.Character{BookID: b.ID, Name: "Tom", Gender: store.GenderMale}
		require.NoError(t, s.CreateCharacter(ctx, anna))
		require.NoError(t, s.CreateCharacter(ctx, tom))
		require.NoError(t, c.Start(ctx, b, false))
		_, err := s.ClaimJob(ctx, Queue, time.Now().UTC())
		require.NoError(t, err)
		calls := 0
		srv.Respond = func(llmtest.Request) llmtest.Reply {
			calls++
			if calls == 1 {
				// Tom is promoted while Anna is cast.
				require.NoError(t, s.UpdateCharacter(ctx, tom.ID, map[string]any{"Tier": store.TierMain, "TierManual": true}))
				got, err := s.GetBook(ctx, b.ID)
				require.NoError(t, err)
				require.NoError(t, c.Start(ctx, got, false))
			}
			return castAnswer(t)
		}
		require.NoError(t, c.Run(ctx, b.ID, func(float64) {}))
		assert.Equal(t, 2, calls)
		got, err := s.GetCharacter(ctx, tom.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, got.VoiceRecipe)
		book, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.CastingDone, book.CastingStatus)
	})
}

func TestStartWhileTheJobFinishes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		c, _ := newCaster(t, s)
		require.NoError(t, c.Start(ctx, b, false))
		_, err := s.ClaimJob(ctx, Queue, time.Now().UTC())
		require.NoError(t, err)
		// The running job has just finished its work …
		require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"CastingStatus": store.CastingDone}))
		got, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		// … when a start arrives: another job is queued, so nothing is lost.
		require.NoError(t, c.Start(ctx, got, false))
		assert.Len(t, c.Jobs.(*enqueuer).jobs, 2)
		got, err = s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.CastingQueued, got.CastingStatus)
	})
}

func TestGenderChangedWhileCasting(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		c, srv := newCaster(t, s)
		x := &store.Character{BookID: b.ID, Name: "X", Gender: store.GenderMale, Speaks: true, Importance: 5}
		require.NoError(t, s.CreateCharacter(ctx, x))
		require.NoError(t, c.Start(ctx, b, false))
		calls := 0
		srv.Respond = func(llmtest.Request) llmtest.Reply {
			calls++
			if calls == 1 {
				require.NoError(t, s.UpdateCharacter(ctx, x.ID, map[string]any{"Gender": store.GenderFemale}))
				got, err := s.GetBook(ctx, b.ID)
				require.NoError(t, err)
				require.NoError(t, c.Start(ctx, got, false))
			}
			return castAnswer(t)
		}
		require.NoError(t, c.Run(ctx, b.ID, func(float64) {}))
		got, err := s.GetCharacter(ctx, x.ID)
		require.NoError(t, err)
		r, err := ParseRecipe(got.VoiceRecipe)
		require.NoError(t, err)
		for _, p := range r {
			assert.Equal(t, store.GenderFemale, lookup(t, p.Voice).Gender, "cast for the new gender")
		}
	})
}

func TestCastingFailureAndQuota(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		c, _ := newCaster(t, s)
		require.NoError(t, s.CreateCharacter(ctx, &store.Character{BookID: b.ID, Name: "Anna", Speaks: true}))
		c.Catalog = NewCatalog(&fakeVoices{err: errors.New("down")})
		require.NoError(t, c.Start(ctx, b, false))
		require.Error(t, c.Run(ctx, b.ID, func(float64) {}), "retried until the TTS server answers")
		require.ErrorIs(t, c.Start(ctx, b, true), ErrBusy)

		j := c.Jobs.(*enqueuer).jobs[0]
		c.Handler().OnFail(ctx, &j, errors.New("gave up"))
		got, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.CastingFailed, got.CastingStatus)
		assert.Equal(t, "gave up", got.CastingError)
	})
}

func TestQuotes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := setup(t, s)
		chs, _, err := s.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 10)
		require.NoError(t, err)
		anna := &store.Character{BookID: b.ID, Name: "Anna Smith"}
		require.NoError(t, s.CreateCharacter(ctx, anna))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, anna.ID, chs[1].ID))
		qs, err := Quotes(ctx, s, anna, nil, 5)
		require.NoError(t, err)
		require.Len(t, qs, 1)
		assert.Contains(t, qs[0].Text, "Is it raining?")
		assert.Equal(t, chs[1].ID, qs[0].Chapter.ID)

		qs, err = Quotes(ctx, s, &store.Character{ID: anna.ID, Name: "the girl"}, nil, 5)
		require.NoError(t, err)
		assert.Empty(t, qs, "descriptions are not names")
		re := attributionRe("Émile Zola", []string{"Zoë"})
		assert.True(t, re.MatchString("“Hi,” Émile said."))
		assert.True(t, re.MatchString("“Hi,” said Zoë."))
		assert.False(t, re.MatchString("“Hi,” said Zoëtrope."))
		assert.False(t, re.MatchString("“Hi,” Rémile said."))
	})
}
