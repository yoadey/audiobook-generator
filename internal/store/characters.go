package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Tx runs fn in a transaction with a Store bound to it. Store methods
// called on the inner Store take part in the transaction.
func (s *Store) Tx(ctx context.Context, fn func(tx *Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Store{db: tx, dialect: s.dialect})
	})
}

// NormName is the matching form of a character name or alias: lower case,
// letters, digits, apostrophes and hyphens only, single spaces, without a
// leading article ("The Innkeeper" → "innkeeper").
func NormName(s string) string {
	s = strings.ToLower(strings.NewReplacer("’", "'", "‘", "'").Replace(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '-':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	out := b.String()
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(out, article); ok && rest != "" {
			out = rest
			break
		}
	}
	return out
}

// CharacterFilter narrows ListCharacters.
type CharacterFilter struct {
	// Search matches names and aliases (case-insensitive substring).
	Search string
	// Sort: "importance" (default), "name", "chapters" or "first".
	Sort string
	// Tier limits the list to main or minor characters.
	Tier Tier
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(s)) + "%"
}

// ListCharacters returns a page of a book's characters.
func (s *Store) ListCharacters(ctx context.Context, bookID int64, f CharacterFilter, offset, limit int) ([]Character, int64, error) {
	db := s.db.WithContext(ctx)
	q := db.Model(&Character{}).Where("book_id = ?", bookID)
	if f.Search != "" {
		p := likePattern(f.Search)
		q = q.Where(`LOWER(name) LIKE ? ESCAPE '\' OR id IN (?)`, p,
			db.Session(&gorm.Session{NewDB: true}).Model(&CharacterAlias{}).Select("character_id").
				Where(`book_id = ? AND LOWER(alias) LIKE ? ESCAPE '\'`, bookID, p))
	}
	if f.Tier != "" {
		q = q.Where("tier = ?", f.Tier)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "importance DESC, chapter_count DESC, name, id"
	switch f.Sort {
	case "name":
		order = "name, id"
	case "chapters":
		order = "chapter_count DESC, importance DESC, name, id"
	case "first":
		order = "first_chapter_idx, importance DESC, name, id"
	}
	var cs []Character
	err := q.Order(order).Offset(offset).Limit(limit).Find(&cs).Error
	return cs, total, err
}

// CountCharacters returns the number of characters of a book.
func (s *Store) CountCharacters(ctx context.Context, bookID int64) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Character{}).Where("book_id = ?", bookID).Count(&n).Error
	return n, err
}

// GetCharacter loads one character.
func (s *Store) GetCharacter(ctx context.Context, id int64) (*Character, error) {
	var c Character
	if err := s.db.WithContext(ctx).Take(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// CharacterAliases returns the aliases of characters, by character ID.
func (s *Store) CharacterAliases(ctx context.Context, ids ...int64) (map[int64][]CharacterAlias, error) {
	out := map[int64][]CharacterAlias{}
	if len(ids) == 0 {
		return out, nil
	}
	var as []CharacterAlias
	if err := s.db.WithContext(ctx).Where("character_id IN ?", ids).Order("id").Find(&as).Error; err != nil {
		return nil, err
	}
	for _, a := range as {
		out[a.CharacterID] = append(out[a.CharacterID], a)
	}
	return out, nil
}

// BookAliases returns every alias of a book's characters.
func (s *Store) BookAliases(ctx context.Context, bookID int64) ([]CharacterAlias, error) {
	var as []CharacterAlias
	err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Order("id").Find(&as).Error
	return as, err
}

// BookCharacters returns all characters of a book (for analysis).
func (s *Store) BookCharacters(ctx context.Context, bookID int64) ([]Character, error) {
	var cs []Character
	err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Order("id").Find(&cs).Error
	return cs, err
}

// CharacterChapters returns the chapters a character appears in, in
// reading order.
func (s *Store) CharacterChapters(ctx context.Context, characterID int64) ([]Chapter, error) {
	db := s.db.WithContext(ctx)
	var cs []Chapter
	err := db.Where("id IN (?)", db.Session(&gorm.Session{NewDB: true}).Model(&CharacterChapter{}).
		Select("chapter_id").Where("character_id = ?", characterID)).
		Order("idx").Find(&cs).Error
	return cs, err
}

// ChapterCharacters returns the characters appearing in a chapter, most
// important first.
func (s *Store) ChapterCharacters(ctx context.Context, chapterID int64) ([]Character, error) {
	db := s.db.WithContext(ctx)
	var cs []Character
	err := db.Where("id IN (?)", db.Session(&gorm.Session{NewDB: true}).Model(&CharacterChapter{}).
		Select("character_id").Where("chapter_id = ?", chapterID)).
		Order("importance DESC, chapter_count DESC, name").Find(&cs).Error
	return cs, err
}

// CreateCharacter inserts a character with aliases.
func (s *Store) CreateCharacter(ctx context.Context, c *Character, aliases ...string) error {
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.db.Create(c).Error; err != nil {
			return err
		}
		_, err := tx.AddCharacterAliases(ctx, c, aliases...)
		return err
	})
}

// UpdateCharacter sets columns (by Go field name) of a character.
func (s *Store) UpdateCharacter(ctx context.Context, id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := s.db.WithContext(ctx).Model(&Character{ID: id}).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AddCharacterAliases adds aliases that the character does not have yet
// (by normalized form, its name included) and returns how many were added.
func (s *Store) AddCharacterAliases(ctx context.Context, c *Character, aliases ...string) (int, error) {
	if len(aliases) == 0 {
		return 0, nil
	}
	db := s.db.WithContext(ctx)
	var existing []string
	if err := db.Model(&CharacterAlias{}).Where("character_id = ?", c.ID).Pluck("norm", &existing).Error; err != nil {
		return 0, err
	}
	seen := map[string]bool{NormName(c.Name): true}
	for _, n := range existing {
		seen[n] = true
	}
	var add []CharacterAlias
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		n := NormName(a)
		if n == "" || seen[n] || utf8.RuneCountInString(a) > 120 {
			continue
		}
		seen[n] = true
		add = append(add, CharacterAlias{BookID: c.BookID, CharacterID: c.ID, Alias: a, Norm: n})
	}
	if len(add) == 0 {
		return 0, nil
	}
	return len(add), db.CreateInBatches(add, batchSize).Error
}

// SetCharacterAliases replaces the aliases of a character.
func (s *Store) SetCharacterAliases(ctx context.Context, c *Character, aliases []string) error {
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.db.Where("character_id = ?", c.ID).Delete(&CharacterAlias{}).Error; err != nil {
			return err
		}
		_, err := tx.AddCharacterAliases(ctx, c, aliases...)
		return err
	})
}

// LinkCharacterChapters records appearances (existing ones are kept).
func (s *Store) LinkCharacterChapters(ctx context.Context, bookID, characterID int64, chapterIDs ...int64) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	rows := make([]CharacterChapter, 0, len(chapterIDs))
	for _, id := range chapterIDs {
		rows = append(rows, CharacterChapter{BookID: bookID, CharacterID: characterID, ChapterID: id})
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(rows, batchSize).Error
}

// RefreshCharacterStats recomputes chapter counts and first appearances.
func (s *Store) RefreshCharacterStats(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Character{}).Where("id IN ?", ids).Updates(map[string]any{
		"chapter_count": gorm.Expr("(SELECT COUNT(*) FROM character_chapters cc WHERE cc.character_id = characters.id)"),
		"first_chapter_idx": gorm.Expr("COALESCE((SELECT MIN(ch.idx) FROM chapters ch JOIN character_chapters cc " +
			"ON cc.chapter_id = ch.id WHERE cc.character_id = characters.id), 0)"),
	}).Error
}

// RefreshBookCharacterStats recomputes the stats of all of a book's
// characters.
func (s *Store) RefreshBookCharacterStats(ctx context.Context, bookID int64) error {
	var ids []int64
	if err := s.db.WithContext(ctx).Model(&Character{}).Where("book_id = ?", bookID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for start := 0; start < len(ids); start += batchSize {
		if err := s.RefreshCharacterStats(ctx, ids[start:min(start+batchSize, len(ids))]...); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCharacter removes a character and everything that refers to it.
func (s *Store) DeleteCharacter(ctx context.Context, id int64) error {
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		for _, m := range []any{&CharacterAlias{}, &CharacterChapter{}} {
			if err := db.Where("character_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		if err := db.Where("a_id = ? OR b_id = ?", id, id).Delete(&CharacterDistinct{}).Error; err != nil {
			return err
		}
		if err := db.Model(&Chapter{}).Where("pov_character_id = ?", id).
			Update("pov_character_id", nil).Error; err != nil {
			return err
		}
		res := db.Delete(&Character{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// MergeCharacters folds sources into target: their names become aliases,
// appearances and point-of-view chapters move over, empty profile fields
// are filled in. Sources are deleted.
func (s *Store) MergeCharacters(ctx context.Context, targetID int64, sourceIDs ...int64) (*Character, error) {
	var target Character
	err := s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		if err := db.Take(&target, targetID).Error; err != nil {
			return notFound(err)
		}
		var sources []Character
		if err := db.Where("id IN ? AND id <> ?", sourceIDs, targetID).Find(&sources).Error; err != nil {
			return err
		}
		if len(sources) == 0 {
			return fmt.Errorf("%w: nothing to merge", ErrInvalid)
		}
		var names []string
		var srcIDs []int64
		for _, src := range sources {
			if src.BookID != target.BookID {
				return fmt.Errorf("%w: characters of different books", ErrInvalid)
			}
			srcIDs = append(srcIDs, src.ID)
			names = append(names, src.Name)
			mergeProfile(&target, &src)
		}
		var srcAliases []string
		if err := db.Model(&CharacterAlias{}).Where("character_id IN ?", srcIDs).Order("id").
			Pluck("alias", &srcAliases).Error; err != nil {
			return err
		}
		if _, err := tx.AddCharacterAliases(ctx, &target, append(names, srcAliases...)...); err != nil {
			return err
		}
		var chapterIDs []int64
		if err := db.Model(&CharacterChapter{}).Where("character_id IN ?", srcIDs).
			Distinct().Pluck("chapter_id", &chapterIDs).Error; err != nil {
			return err
		}
		if err := tx.LinkCharacterChapters(ctx, target.BookID, target.ID, chapterIDs...); err != nil {
			return err
		}
		if err := db.Model(&Chapter{}).Where("pov_character_id IN ?", srcIDs).
			Update("pov_character_id", target.ID).Error; err != nil {
			return err
		}
		// "X is not Y" still holds for the merged character.
		var distinct []CharacterDistinct
		if err := db.Where("a_id IN ? OR b_id IN ?", srcIDs, srcIDs).Find(&distinct).Error; err != nil {
			return err
		}
		for _, d := range distinct {
			other := d.AID
			if containsID(srcIDs, other) {
				other = d.BID
			}
			if other != target.ID && !containsID(srcIDs, other) {
				if err := tx.MarkDistinct(ctx, target.BookID, target.ID, other); err != nil {
					return err
				}
			}
		}
		for _, m := range []any{&CharacterAlias{}, &CharacterChapter{}} {
			if err := db.Where("character_id IN ?", srcIDs).Delete(m).Error; err != nil {
				return err
			}
		}
		if err := db.Where("a_id IN ? OR b_id IN ?", srcIDs, srcIDs).Delete(&CharacterDistinct{}).Error; err != nil {
			return err
		}
		if err := db.Delete(&Character{}, srcIDs).Error; err != nil {
			return err
		}
		if err := db.Model(&Character{ID: target.ID}).Select("Gender", "AgeGroup", "Role", "Description",
			"SpeechStyle", "Importance", "Speaks", "UserEdited", "Tier", "TierManual",
			"VoiceRecipe", "VoiceSpeed", "VoiceManual", "VoiceNote").Updates(&target).Error; err != nil {
			return err
		}
		return tx.RefreshCharacterStats(ctx, target.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCharacter(ctx, targetID)
}

// mergeProfile fills empty fields of dst from src and keeps the strongest
// signals (importance, speaks).
func mergeProfile(dst, src *Character) {
	if dst.Gender == GenderUnknown || dst.Gender == "" {
		if dst.Gender != src.Gender && !dst.VoiceManual {
			// A voice cast for an unknown gender may not fit the known one.
			dst.VoiceRecipe, dst.VoiceSpeed, dst.VoiceNote = "", 1, ""
		}
		dst.Gender = src.Gender
	}
	if dst.AgeGroup == AgeUnknown || dst.AgeGroup == "" {
		dst.AgeGroup = src.AgeGroup
	}
	if dst.Role == "" {
		dst.Role = src.Role
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.SpeechStyle == "" {
		dst.SpeechStyle = src.SpeechStyle
	}
	dst.Importance = max(dst.Importance, src.Importance)
	dst.Speaks = dst.Speaks || src.Speaks
	dst.UserEdited = dst.UserEdited || src.UserEdited
	// The merged character keeps a voice: its own, else a hand-picked or
	// cast one of a source; a main character stays main.
	if dst.VoiceRecipe == "" || (!dst.VoiceManual && src.VoiceManual) {
		// A cast voice only fits a character of the same gender.
		if src.VoiceRecipe != "" && (src.VoiceManual || src.Gender == dst.Gender) {
			dst.VoiceRecipe, dst.VoiceSpeed, dst.VoiceManual, dst.VoiceNote =
				src.VoiceRecipe, src.VoiceSpeed, src.VoiceManual, src.VoiceNote
		}
	}
	switch {
	case dst.TierManual:
	case src.TierManual:
		dst.Tier, dst.TierManual = src.Tier, true
	case src.Tier == TierMain:
		dst.Tier = TierMain
	}
	// Minor characters speak with pool voices unless one was chosen.
	if dst.Tier != TierMain && !dst.VoiceManual {
		dst.VoiceRecipe, dst.VoiceSpeed, dst.VoiceNote = "", 1, ""
	}
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// SplitCharacter moves the given aliases of a character to a new
// character named name (which may be one of them). Appearances are copied:
// the analysis does not know which alias appeared where. The two are
// recorded as distinct.
func (s *Store) SplitCharacter(ctx context.Context, id int64, name string, aliases []string) (*Character, error) {
	name = strings.TrimSpace(name)
	var created Character
	err := s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		var orig Character
		if err := db.Take(&orig, id).Error; err != nil {
			return notFound(err)
		}
		if NormName(name) == "" || NormName(name) == NormName(orig.Name) {
			return fmt.Errorf("%w: the new character needs a name other than %q", ErrInvalid, orig.Name)
		}
		norms := []string{NormName(name)}
		for _, a := range aliases {
			norms = append(norms, NormName(a))
		}
		var moving []CharacterAlias
		if err := db.Where("character_id = ? AND norm IN ?", id, norms).Find(&moving).Error; err != nil {
			return err
		}
		created = Character{BookID: orig.BookID, Name: name, UserEdited: true}
		if err := db.Create(&created).Error; err != nil {
			return err
		}
		var moved []string
		for _, a := range moving {
			moved = append(moved, a.Alias)
		}
		if len(moving) > 0 {
			ids := make([]int64, len(moving))
			for i := range moving {
				ids[i] = moving[i].ID
			}
			if err := db.Where("id IN ?", ids).Delete(&CharacterAlias{}).Error; err != nil {
				return err
			}
		}
		if _, err := tx.AddCharacterAliases(ctx, &created, moved...); err != nil {
			return err
		}
		var chapterIDs []int64
		if err := db.Model(&CharacterChapter{}).Where("character_id = ?", id).Pluck("chapter_id", &chapterIDs).Error; err != nil {
			return err
		}
		if err := tx.LinkCharacterChapters(ctx, orig.BookID, created.ID, chapterIDs...); err != nil {
			return err
		}
		if err := tx.MarkDistinct(ctx, orig.BookID, orig.ID, created.ID); err != nil {
			return err
		}
		return tx.RefreshCharacterStats(ctx, created.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCharacter(ctx, created.ID)
}

// MarkDistinct records that two characters are different people.
func (s *Store) MarkDistinct(ctx context.Context, bookID, a, b int64) error {
	if a == b {
		return nil
	}
	if a > b {
		a, b = b, a
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&CharacterDistinct{BookID: bookID, AID: a, BID: b}).Error
}

// DistinctPairs returns the pairs known to be different people.
func (s *Store) DistinctPairs(ctx context.Context, bookID int64) (map[[2]int64]bool, error) {
	var ds []CharacterDistinct
	if err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Find(&ds).Error; err != nil {
		return nil, err
	}
	out := make(map[[2]int64]bool, len(ds))
	for _, d := range ds {
		out[[2]int64{d.AID, d.BID}] = true
	}
	return out, nil
}

// SetChapterNarration records how a chapter is told.
func (s *Store) SetChapterNarration(ctx context.Context, chapterID int64, n Narration, povCharacterID *int64) error {
	return s.db.WithContext(ctx).Model(&Chapter{ID: chapterID}).Updates(map[string]any{
		"narration": n, "pov_character_id": povCharacterID,
	}).Error
}

// --- Analysis batches ---------------------------------------------------------

// CreateAnalysisBatches inserts batches.
func (s *Store) CreateAnalysisBatches(ctx context.Context, bs []AnalysisBatch) error {
	if len(bs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).CreateInBatches(bs, 100).Error
}

// NextAnalysisBatch returns the first pending batch in reading order (nil
// when all are done).
func (s *Store) NextAnalysisBatch(ctx context.Context, bookID int64) (*AnalysisBatch, error) {
	var b AnalysisBatch
	err := s.db.WithContext(ctx).Where("book_id = ? AND status = ?", bookID, BatchPending).
		Order("first_chapter_idx, first_para_idx, id").Take(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &b, err
}

// CompleteAnalysisBatch stores a batch's result.
func (s *Store) CompleteAnalysisBatch(ctx context.Context, b *AnalysisBatch) error {
	now := time.Now().UTC()
	b.Status, b.CompletedAt = BatchDone, &now
	fields := map[string]any{
		"status": BatchDone, "prompt": b.Prompt, "provider": b.Provider, "model": b.Model,
		"input_tokens": b.InputTokens, "output_tokens": b.OutputTokens, "result": CleanText(b.Result),
		"completed_at": now,
	}
	if b.Parts != "" {
		fields["parts"] = b.Parts
	}
	return s.db.WithContext(ctx).Model(&AnalysisBatch{ID: b.ID}).Updates(fields).Error
}

// ReplaceAnalysisBatch swaps a pending batch for smaller ones (a batch
// whose answer was cut off, or that no provider accepts).
func (s *Store) ReplaceAnalysisBatch(ctx context.Context, id int64, parts []AnalysisBatch) error {
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.db.Delete(&AnalysisBatch{}, id).Error; err != nil {
			return err
		}
		return tx.CreateAnalysisBatches(ctx, parts)
	})
}

// AnalysisStats summarizes a book's batches.
type AnalysisStats struct {
	Total, Done               int
	EstimatedTokens           int64 // input of all batches
	InputTokens, OutputTokens int64 // actually used
}

// AnalysisStats returns the progress of a book's analysis.
func (s *Store) AnalysisStats(ctx context.Context, bookID int64) (AnalysisStats, error) {
	var rows []struct {
		Status BatchStatus
		N      int
		Est    int64
		InTok  int64
		OutTok int64
	}
	err := s.db.WithContext(ctx).Model(&AnalysisBatch{}).
		Select("status, COUNT(*) AS n, COALESCE(SUM(tokens), 0) AS est, "+
			"COALESCE(SUM(input_tokens), 0) AS in_tok, COALESCE(SUM(output_tokens), 0) AS out_tok").
		Where("book_id = ?", bookID).Group("status").Scan(&rows).Error
	var st AnalysisStats
	for _, r := range rows {
		st.Total += r.N
		st.EstimatedTokens += r.Est
		st.InputTokens += r.InTok
		st.OutputTokens += r.OutTok
		if r.Status == BatchDone {
			st.Done += r.N
		}
	}
	return st, err
}

// ResetAnalysis deletes a book's analysis results (batches, characters,
// aliases, appearances, narration) and sets its state back to none.
func (s *Store) ResetAnalysis(ctx context.Context, bookID int64) error {
	return s.Tx(ctx, func(tx *Store) error {
		if err := deleteAnalysis(tx.db, bookID); err != nil {
			return err
		}
		return tx.db.Model(&Book{ID: bookID}).Updates(map[string]any{
			"analysis_status": AnalysisNone, "analysis_error": "",
			"analysis_started_at": nil, "analysis_finished_at": nil,
			"casting_status": CastingNone, "casting_error": "", "casting_finished_at": nil,
		}).Error
	})
}

func deleteAnalysis(tx *gorm.DB, bookID int64) error {
	for _, m := range []any{&AnalysisBatch{}, &CharacterDistinct{}, &CharacterChapter{}, &CharacterAlias{}, &Character{}} {
		if err := tx.Where("book_id = ?", bookID).Delete(m).Error; err != nil {
			return err
		}
	}
	return tx.Model(&Chapter{}).Where("book_id = ?", bookID).
		Updates(map[string]any{"narration": "", "pov_character_id": nil}).Error
}

// PauseStaleAnalysis sets an analysis that is active but has no job back to
// paused, only if its status is still status and unchanged since
// updatedAt (a concurrent start or resume wins).
func (s *Store) PauseStaleAnalysis(ctx context.Context, bookID int64, status AnalysisStatus, updatedAt time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&Book{}).
		Where("id = ? AND analysis_status = ? AND updated_at = ?", bookID, status, updatedAt).
		Update("analysis_status", AnalysisPaused)
	return res.RowsAffected == 1, res.Error
}

// TransitionAnalysis sets a book's analysis status to "to" only while it is
// one of from (a concurrent pause or resume wins); extra columns are set
// with it. It reports whether it changed the status.
func (s *Store) TransitionAnalysis(ctx context.Context, bookID int64, to AnalysisStatus, extra map[string]any,
	from ...AnalysisStatus,
) (bool, error) {
	fields := map[string]any{"analysis_status": to}
	for k, v := range extra {
		fields[k] = v
	}
	res := s.db.WithContext(ctx).Model(&Book{}).Where("id = ? AND analysis_status IN ?", bookID, from).Updates(fields)
	return res.RowsAffected == 1, res.Error
}

// AnalysisBatchParts returns the Parts of all of a book's batches.
func (s *Store) AnalysisBatchParts(ctx context.Context, bookID int64) ([]string, error) {
	var parts []string
	err := s.db.WithContext(ctx).Model(&AnalysisBatch{}).Where("book_id = ?", bookID).Pluck("parts", &parts).Error
	return parts, err
}

// LastChapterIdx returns the index of each character's last chapter.
func (s *Store) LastChapterIdx(ctx context.Context, ids ...int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		CharacterID int64
		Last        int
	}
	err := s.db.WithContext(ctx).Table("character_chapters cc").
		Select("cc.character_id, MAX(ch.idx) AS last").
		Joins("JOIN chapters ch ON ch.id = cc.chapter_id").
		Where("cc.character_id IN ?", ids).Group("cc.character_id").Scan(&rows).Error
	for _, r := range rows {
		out[r.CharacterID] = r.Last
	}
	return out, err
}
