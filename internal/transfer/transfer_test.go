package transfer_test

import (
	"archive/zip"
	"bytes"
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
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/transfer"
)

func setup(t *testing.T, s *store.Store, dir string) (*store.Book, []store.Chapter) {
	t.Helper()
	ctx := context.Background()
	data, err := epubtest.Sample()
	require.NoError(t, err)
	b := &store.Book{Title: "The Sample", Author: "A. Writer", Status: store.BookImporting}
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
	return b, cs
}

// speakers maps "chapter title/quote ref" to the speaker's name.
func speakers(t *testing.T, s *store.Store, bookID int64) map[string]string {
	t.Helper()
	ctx := context.Background()
	cs, _, err := s.ListChapters(ctx, bookID, store.ChapterFilter{}, 0, 100)
	require.NoError(t, err)
	out := map[string]string{}
	for _, c := range cs {
		_, segs, err := s.ChapterText(ctx, c.ID)
		require.NoError(t, err)
		for _, sg := range segs {
			if sg.SpeakerCharacterID != nil {
				ch, err := s.GetCharacter(ctx, *sg.SpeakerCharacterID)
				require.NoError(t, err)
				assert.Equal(t, bookID, ch.BookID)
				out[c.Title+"/"+string(rune('0'+sg.QuoteRef))] = ch.Name + "/" + sg.Emotion
			}
		}
	}
	return out
}

func TestExportRestore(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		dir := t.TempDir()
		b, cs := setup(t, s, dir)
		ch1, ch2 := cs[1], cs[2]

		// What an analysis and a user leave behind.
		anna := &store.Character{BookID: b.ID, Name: "Anna", Gender: store.GenderFemale, Importance: 4,
			Tier: store.TierMain, VoiceRecipe: "af_bella(2)+af_heart(1)", VoiceManual: true}
		require.NoError(t, s.CreateCharacter(ctx, anna, "Annie"))
		tom := &store.Character{BookID: b.ID, Name: "Tom", Gender: store.GenderMale, Importance: 2}
		require.NoError(t, s.CreateCharacter(ctx, tom))
		require.NoError(t, s.LinkCharacterChapters(ctx, b.ID, anna.ID, ch1.ID, ch2.ID))
		require.NoError(t, s.MarkDistinct(ctx, b.ID, anna.ID, tom.ID))
		require.NoError(t, s.SetChapterNarration(ctx, ch1.ID, store.NarrationThirdPerson, &anna.ID))
		require.NoError(t, s.SetQuoteSpeaker(ctx, ch1.ID, 1, &anna.ID, "cheerful"))
		require.NoError(t, s.SetQuoteSpeaker(ctx, ch1.ID, 2, &tom.ID, ""))
		_, err := s.UpdateChapter(ctx, ch2.ID, store.ChapterUpdate{Title: ptr("Renamed"), Excluded: ptr(true)})
		require.NoError(t, err)
		require.NoError(t, s.SaveLexiconEntry(ctx, &store.LexiconEntry{BookID: b.ID, Term: "Anna", Replacement: "Ah-na"}))
		parts, _ := json.Marshal([]map[string]any{{"chapterId": ch1.ID}})
		require.NoError(t, s.CreateAnalysisBatches(ctx, []store.AnalysisBatch{{
			BookID: b.ID, Parts: string(parts), Status: store.BatchDone, InputTokens: 1234,
		}}))
		require.NoError(t, s.UpdateBook(ctx, b.ID, map[string]any{
			"NarratorVoice": "bm_george", "NarratorSpeed": 1.1, "Lookahead": 5,
			"AnalysisStatus": store.AnalysisRunning, "CastingStatus": store.CastingDone,
		}))
		before := speakers(t, s, b.ID)
		require.Len(t, before, 2)

		var buf bytes.Buffer
		require.NoError(t, transfer.Export(ctx, s, dir, b.ID, &buf))

		// The archive: header, tables, EPUB, cover.
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		require.NoError(t, err)
		names := map[string]bool{}
		for _, f := range zr.File {
			names[f.Name] = true
		}
		for _, n := range []string{"book.json", "chapters.jsonl", "segments.jsonl", "characters.jsonl", "source.epub", "cover.png"} {
			assert.True(t, names[n], n)
		}

		// Restore into a new book, twice (the job is idempotent).
		path := filepath.Join(t.TempDir(), "export.zip")
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
		h, err := transfer.ReadHeader(path)
		require.NoError(t, err)
		assert.Equal(t, "The Sample", h.Book.Title)
		nb := &store.Book{Title: h.Book.Title, Status: store.BookImporting}
		require.NoError(t, s.CreateBook(ctx, nb))
		r := &transfer.Restorer{Store: s, DataDir: dir, Log: slog.New(slog.DiscardHandler)}
		for range 2 {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, importer.BookDir(nb.ID)), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, transfer.RestorePath(nb.ID)), buf.Bytes(), 0o600))
			require.NoError(t, r.Restore(ctx, nb.ID, func(float64) {}))
		}
		assert.NoFileExists(t, filepath.Join(dir, transfer.RestorePath(nb.ID)))

		got, err := s.GetBook(ctx, nb.ID)
		require.NoError(t, err)
		assert.Equal(t, store.BookImported, got.Status)
		assert.Equal(t, "bm_george", got.NarratorVoice)
		assert.InDelta(t, 1.1, got.NarratorSpeed, 0.001)
		assert.Equal(t, 5, got.Lookahead)
		assert.Equal(t, store.AnalysisPaused, got.AnalysisStatus, "a running analysis resumes later")
		assert.Equal(t, store.CastingDone, got.CastingStatus)
		assert.Equal(t, b.WordCount, got.WordCount)
		assert.FileExists(t, filepath.Join(dir, got.EpubPath))
		assert.FileExists(t, filepath.Join(dir, got.CoverPath))
		assert.Equal(t, "image/png", got.CoverMediaType)

		ncs, _, err := s.ListChapters(ctx, nb.ID, store.ChapterFilter{}, 0, 100)
		require.NoError(t, err)
		require.Len(t, ncs, len(cs))
		assert.Equal(t, "Renamed", ncs[2].Title)
		assert.True(t, ncs[2].Excluded)
		assert.NotEqual(t, cs[1].ID, ncs[1].ID)
		vols, err := s.ListVolumes(ctx, nb.ID)
		require.NoError(t, err)
		require.Len(t, vols, 2)
		assert.Equal(t, vols[0].ID, *ncs[1].VolumeID)
		assert.Equal(t, store.AudioNone, ncs[1].AudioStatus)

		// Text, speakers, characters and their links.
		op, osg, err := s.ChapterText(ctx, ch1.ID)
		require.NoError(t, err)
		np, nsg, err := s.ChapterText(ctx, ncs[1].ID)
		require.NoError(t, err)
		require.Len(t, np, len(op))
		require.Len(t, nsg, len(osg))
		for i := range nsg {
			assert.Equal(t, osg[i].Text, nsg[i].Text)
			assert.Equal(t, ncs[1].ID, nsg[i].ChapterID)
		}
		assert.Equal(t, before, speakers(t, s, nb.ID))
		chars, err := s.BookCharacters(ctx, nb.ID)
		require.NoError(t, err)
		require.Len(t, chars, 2)
		var na *store.Character
		for i := range chars {
			if chars[i].Name == "Anna" {
				na = &chars[i]
			}
		}
		require.NotNil(t, na)
		assert.Equal(t, "af_bella(2)+af_heart(1)", na.VoiceRecipe)
		assert.True(t, na.VoiceManual)
		assert.Equal(t, store.TierMain, na.Tier)
		aliases, err := s.CharacterAliases(ctx, na.ID)
		require.NoError(t, err)
		require.Len(t, aliases[na.ID], 1)
		assert.Equal(t, "Annie", aliases[na.ID][0].Alias)
		linked, err := s.CharacterChapters(ctx, na.ID)
		require.NoError(t, err)
		assert.Len(t, linked, 2)
		assert.Equal(t, na.ID, *ncs[1].POVCharacterID)
		pairs, err := s.DistinctPairs(ctx, nb.ID)
		require.NoError(t, err)
		assert.Len(t, pairs, 1)
		lex, err := s.ListLexicon(ctx, nb.ID)
		require.NoError(t, err)
		require.Len(t, lex, 1)
		assert.Equal(t, "Ah-na", lex[0].Replacement)
		bparts, err := s.AnalysisBatchParts(ctx, nb.ID)
		require.NoError(t, err)
		require.Len(t, bparts, 1)
		var p []map[string]any
		require.NoError(t, json.Unmarshal([]byte(bparts[0]), &p))
		assert.InDelta(t, float64(ncs[1].ID), p[0]["chapterId"], 0)

		// The original book is untouched.
		assert.Equal(t, before, speakers(t, s, b.ID))
	})
}

func TestReadHeaderRejects(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, files map[string]string) string {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for n, c := range files {
			w, err := zw.Create(n)
			require.NoError(t, err)
			_, _ = w.Write([]byte(c))
		}
		require.NoError(t, zw.Close())
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o600))
		return p
	}
	notZip := filepath.Join(dir, "x.zip")
	require.NoError(t, os.WriteFile(notZip, []byte("plain text"), 0o600))
	for _, p := range []string{
		notZip,
		write("empty.zip", map[string]string{"a.txt": "x"}),
		write("other.zip", map[string]string{"book.json": `{"format":"something-else","version":1}`}),
		write("untitled.zip", map[string]string{"book.json": `{"format":"audiobook-generator-book","version":1,"book":{}}`}),
	} {
		_, err := transfer.ReadHeader(p)
		assert.ErrorIs(t, err, transfer.ErrNotExport, p)
	}
	_, err := transfer.ReadHeader(write("newer.zip", map[string]string{
		"book.json": `{"format":"audiobook-generator-book","version":99,"book":{"Title":"x"}}`,
	}))
	assert.ErrorContains(t, err, "newer version")
}

func ptr[T any](v T) *T { return &v }
