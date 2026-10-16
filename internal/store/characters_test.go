package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

// bookWithChapters creates a book with n one-paragraph chapters.
func bookWithChapters(t *testing.T, s *store.Store, n int) (*store.Book, []store.Chapter) {
	t.Helper()
	ctx := context.Background()
	b := &store.Book{Title: "B", Status: store.BookImported}
	require.NoError(t, s.CreateBook(ctx, b))
	var cs []store.Chapter
	for i := range n {
		c := &store.ChapterContent{
			Chapter: store.Chapter{BookID: b.ID, Idx: i, Title: "Chapter", Status: store.ChapterImported},
			Paragraphs: []store.Paragraph{
				{Idx: 0, Kind: store.ParagraphText, Text: "Text one.", WordCount: 2},
				{Idx: 1, Kind: store.ParagraphText, Text: "Text two.", WordCount: 2},
			},
			Segments: []store.Segment{
				{ParagraphID: 0, Idx: 0, Kind: store.SegmentNarration, Text: "Text one."},
				{ParagraphID: 1, Idx: 1, Kind: store.SegmentNarration, Text: "Text two."},
			},
		}
		require.NoError(t, s.CreateChapterContent(ctx, c))
		cs = append(cs, c.Chapter)
	}
	return b, cs
}

func names(cs []store.Character) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func TestNormName(t *testing.T) {
	for in, want := range map[string]string{
		"The Innkeeper":       "innkeeper",
		"  Erin   Solstice! ": "erin solstice",
		"Mrs. Bennet":         "mrs bennet",
		"O’Brien":             "o'brien",
		"the":                 "the",
		"[Innkeeper]":         "innkeeper",
		"A":                   "a",
	} {
		assert.Equal(t, want, store.NormName(in), in)
	}
}

func TestCharacters(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, chs := bookWithChapters(t, s, 4)

		erin := &store.Character{BookID: b.ID, Name: "Erin Solstice", Gender: store.GenderFemale, Importance: 5}
		require.NoError(t, s.CreateCharacter(ctx, erin, "Erin", "the Innkeeper", "erin", "Erin Solstice", ""))
		as, err := s.CharacterAliases(ctx, erin.ID)
		require.NoError(t, err)
		require.Len(t, as[erin.ID], 2, "duplicates and the name itself are skipped")
		assert.Equal(t, "innkeeper", as[erin.ID][1].Norm)

		ryoka := &store.Character{BookID: b.ID, Name: "Ryoka Griffin", Importance: 4}
		require.NoError(t, s.CreateCharacter(ctx, ryoka, "the Runner"))
		guard := &store.Character{BookID: b.ID, Name: "Guard"}
		require.NoError(t, s.CreateCharacter(ctx, guard))
		assert.Equal(t, store.GenderUnknown, guard.Gender, "default")
		assert.Equal(t, 1, guard.Importance)

		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, erin.ID, chs[1].ID, chs[2].ID, chs[1].ID))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, erin.ID, chs[2].ID, chs[3].ID))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, ryoka.ID, chs[0].ID))
		require.NoError(t, s.RefreshBookCharacterStats(ctx, b.ID))
		got, err := s.GetCharacter(ctx, erin.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, got.ChapterCount)
		assert.Equal(t, 1, got.FirstChapterIdx)

		list, total, err := s.ListCharacters(ctx, b.ID, store.CharacterFilter{}, 0, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		assert.Equal(t, []string{"Erin Solstice", "Ryoka Griffin", "Guard"}, names(list))
		list, _, err = s.ListCharacters(ctx, b.ID, store.CharacterFilter{Sort: "first"}, 0, 10)
		require.NoError(t, err)
		assert.Equal(t, "Ryoka Griffin", list[0].Name)
		list, total, err = s.ListCharacters(ctx, b.ID, store.CharacterFilter{Search: "runner"}, 0, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total, "aliases are searched")
		assert.Equal(t, "Ryoka Griffin", list[0].Name)
		_, total, err = s.ListCharacters(ctx, b.ID, store.CharacterFilter{Search: "100%_"}, 0, 10)
		require.NoError(t, err)
		assert.Zero(t, total, "wildcards are literal")

		in2, err := s.ChapterCharacters(ctx, chs[2].ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"Erin Solstice"}, names(in2))
		chapters, err := s.CharacterChapters(ctx, erin.ID)
		require.NoError(t, err)
		require.Len(t, chapters, 3)
		assert.Equal(t, 1, chapters[0].Idx)

		// POV and distinctness move with a merge.
		require.NoError(t, s.SetChapterNarration(ctx, chs[0].ID, store.NarrationFirstPerson, &ryoka.ID))
		require.NoError(t, s.MarkDistinct(ctx, b.ID, ryoka.ID, guard.ID))
		require.NoError(t, s.MarkDistinct(ctx, b.ID, guard.ID, ryoka.ID), "idempotent in any order")
		merged, err := s.MergeCharacters(ctx, erin.ID, ryoka.ID)
		require.NoError(t, err)
		assert.Equal(t, 4, merged.ChapterCount)
		assert.Equal(t, 0, merged.FirstChapterIdx)
		assert.Equal(t, store.GenderFemale, merged.Gender)
		as, err = s.CharacterAliases(ctx, erin.ID)
		require.NoError(t, err)
		var aliases []string
		for _, a := range as[erin.ID] {
			aliases = append(aliases, a.Alias)
		}
		assert.Equal(t, []string{"Erin", "the Innkeeper", "Ryoka Griffin", "the Runner"}, aliases)
		_, err = s.GetCharacter(ctx, ryoka.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		ch0, err := s.GetChapter(ctx, chs[0].ID)
		require.NoError(t, err)
		assert.Equal(t, erin.ID, *ch0.POVCharacterID)
		assert.Equal(t, store.NarrationFirstPerson, ch0.Narration)
		distinct, err := s.DistinctPairs(ctx, b.ID)
		require.NoError(t, err)
		assert.Len(t, distinct, 1)
		assert.True(t, distinct[[2]int64{min(erin.ID, guard.ID), max(erin.ID, guard.ID)}])

		_, err = s.MergeCharacters(ctx, erin.ID, erin.ID)
		require.ErrorIs(t, err, store.ErrInvalid)

		// Split the wrongly merged one off again.
		split, err := s.SplitCharacter(ctx, erin.ID, "Ryoka Griffin", []string{"the Runner"})
		require.NoError(t, err)
		as, err = s.CharacterAliases(ctx, erin.ID, split.ID)
		require.NoError(t, err)
		assert.Len(t, as[erin.ID], 2)
		require.Len(t, as[split.ID], 1)
		assert.Equal(t, "the Runner", as[split.ID][0].Alias)
		assert.Equal(t, 4, split.ChapterCount, "appearances are copied")
		assert.True(t, split.UserEdited)
		distinct, err = s.DistinctPairs(ctx, b.ID)
		require.NoError(t, err)
		assert.True(t, distinct[[2]int64{min(erin.ID, split.ID), max(erin.ID, split.ID)}])
		_, err = s.SplitCharacter(ctx, erin.ID, "erin solstice", nil)
		require.ErrorIs(t, err, store.ErrInvalid)

		require.NoError(t, s.SetCharacterAliases(ctx, guard, []string{"Gate guard", "gate guard"}))
		as, err = s.CharacterAliases(ctx, guard.ID)
		require.NoError(t, err)
		assert.Len(t, as[guard.ID], 1)

		require.NoError(t, s.DeleteCharacter(ctx, erin.ID))
		ch0, err = s.GetChapter(ctx, chs[0].ID)
		require.NoError(t, err)
		assert.Nil(t, ch0.POVCharacterID)
		require.ErrorIs(t, s.DeleteCharacter(ctx, erin.ID), store.ErrNotFound)
	})
}

func TestChapterEditsKeepAppearances(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, chs := bookWithChapters(t, s, 3)
		a := &store.Character{BookID: b.ID, Name: "A"}
		require.NoError(t, s.CreateCharacter(ctx, a))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, a.ID, chs[2].ID))
		require.NoError(t, s.RefreshCharacterStats(ctx, a.ID))

		// Merging chapter 1 and 2 moves A's appearance and shifts indexes.
		_, err := s.MergeChapterWithNext(ctx, chs[1].ID)
		require.NoError(t, err)
		got, err := s.GetCharacter(ctx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, got.ChapterCount)
		assert.Equal(t, 1, got.FirstChapterIdx)
		in, err := s.ChapterCharacters(ctx, chs[1].ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"A"}, names(in))

		// Splitting gives both parts the characters.
		ps, _, err := s.ChapterText(ctx, chs[1].ID)
		require.NoError(t, err)
		created, err := s.SplitChapter(ctx, chs[1].ID, ps[2].ID, "Part 2")
		require.NoError(t, err)
		in, err = s.ChapterCharacters(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"A"}, names(in))
		got, err = s.GetCharacter(ctx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, got.ChapterCount)
	})
}

func TestAnalysisBatches(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b, chs := bookWithChapters(t, s, 3)
		require.NoError(t, s.CreateAnalysisBatches(ctx, []store.AnalysisBatch{
			{BookID: b.ID, FirstChapterIdx: 2, Parts: `[{"chapterId":3}]`, Tokens: 30},
			{BookID: b.ID, FirstChapterIdx: 0, Parts: `[{"chapterId":1}]`, Tokens: 10},
		}))
		next, err := s.NextAnalysisBatch(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, next.FirstChapterIdx, "reading order")

		// Split it into two halves.
		require.NoError(t, s.ReplaceAnalysisBatch(ctx, next.ID, []store.AnalysisBatch{
			{BookID: b.ID, FirstChapterIdx: 0, FirstParaIdx: 0, Parts: `[{"chapterId":1,"toPara":1}]`, Tokens: 5},
			{BookID: b.ID, FirstChapterIdx: 0, FirstParaIdx: 1, Parts: `[{"chapterId":1,"fromPara":1}]`, Tokens: 5},
		}))
		next, err = s.NextAnalysisBatch(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, next.FirstParaIdx)
		next.Prompt, next.Provider, next.InputTokens, next.OutputTokens, next.Result = "p@v1", "gemini", 100, 20, `{"characters":[]}`
		require.NoError(t, s.CompleteAnalysisBatch(ctx, next))
		next, err = s.NextAnalysisBatch(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, next.FirstParaIdx)

		st, err := s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisStats{Total: 3, Done: 1, EstimatedTokens: 40, InputTokens: 100, OutputTokens: 20}, st)

		c := &store.Character{BookID: b.ID, Name: "X"}
		require.NoError(t, s.CreateCharacter(ctx, c, "Y"))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, c.ID, chs[0].ID))
		require.NoError(t, s.SetChapterNarration(ctx, chs[0].ID, store.NarrationThirdPerson, &c.ID))
		require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{"AnalysisStatus": store.AnalysisDone}))
		require.NoError(t, s.ResetAnalysis(ctx, b.ID))
		st, err = s.AnalysisStats(ctx, b.ID)
		require.NoError(t, err)
		assert.Zero(t, st.Total)
		n, err := s.CountCharacters(ctx, b.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
		got, err := s.GetBook(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, store.AnalysisNone, got.AnalysisStatus)
		ch, err := s.GetChapter(ctx, chs[0].ID)
		require.NoError(t, err)
		assert.Nil(t, ch.POVCharacterID)

		// Deleting the book removes everything.
		require.NoError(t, s.CreateCharacter(ctx, &store.Character{BookID: b.ID, Name: "Z"}))
		require.NoError(t, s.DeleteBook(ctx, b.ID))
		n, err = s.CountCharacters(ctx, b.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
	})
}

func TestMergeKeepsFittingVoices(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := &store.Book{Title: "B", Status: store.BookImported}
		require.NoError(t, s.CreateBook(ctx, b))
		mk := func(c store.Character) *store.Character {
			c.BookID = b.ID
			require.NoError(t, s.CreateCharacter(ctx, &c))
			return &c
		}
		// A main character's cast voice moves to a voiceless target of
		// the same gender, which becomes main.
		target := mk(store.Character{Name: "Erin", Gender: store.GenderFemale})
		src := mk(store.Character{Name: "Erin Solstice", Gender: store.GenderFemale, Tier: store.TierMain,
			VoiceRecipe: "bf_emma", VoiceSpeed: 1.05})
		got, err := s.MergeCharacters(ctx, target.ID, src.ID)
		require.NoError(t, err)
		assert.Equal(t, store.TierMain, got.Tier)
		assert.Equal(t, "bf_emma", got.VoiceRecipe)
		assert.InDelta(t, 1.05, got.VoiceSpeed, 1e-9)

		// A voice cast for an unknown gender is dropped once the gender is
		// known; one of the other gender is not taken over.
		unknown := mk(store.Character{Name: "Guard", Tier: store.TierMain, VoiceRecipe: "af_sky"})
		male := mk(store.Character{Name: "The Guard", Gender: store.GenderMale, Tier: store.TierMain})
		got, err = s.MergeCharacters(ctx, unknown.ID, male.ID)
		require.NoError(t, err)
		assert.Equal(t, store.GenderMale, got.Gender)
		assert.Empty(t, got.VoiceRecipe)

		// A minor target (chosen by hand) does not keep a cast voice.
		minor := mk(store.Character{Name: "Tom", Gender: store.GenderMale, Tier: store.TierMinor, TierManual: true})
		cast := mk(store.Character{Name: "Thomas", Gender: store.GenderMale, Tier: store.TierMain, VoiceRecipe: "bm_george"})
		got, err = s.MergeCharacters(ctx, minor.ID, cast.ID)
		require.NoError(t, err)
		assert.Equal(t, store.TierMinor, got.Tier)
		assert.Empty(t, got.VoiceRecipe)
	})
}
