package store

import (
	"context"
	"time"
)

// TransitionCasting sets a book's casting status to `to` (plus extra
// columns) only if it is one of from; it reports whether it changed.
func (s *Store) TransitionCasting(ctx context.Context, bookID int64, to CastingStatus, extra map[string]any,
	from ...CastingStatus,
) (bool, error) {
	fields := map[string]any{"casting_status": to}
	for k, v := range extra {
		fields[k] = v
	}
	res := s.db.WithContext(ctx).Model(&Book{}).Where("id = ? AND casting_status IN ?", bookID, from).Updates(fields)
	return res.RowsAffected == 1, res.Error
}

// ResetCasting forgets the automatic casting of a book: tiers and recipes
// the user did not choose.
func (s *Store) ResetCasting(ctx context.Context, bookID int64) error {
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.db.Model(&Character{}).Where("book_id = ? AND tier_manual = ?", bookID, false).
			Update("tier", TierMinor).Error; err != nil {
			return err
		}
		return tx.ResetCastVoices(ctx, bookID)
	})
}

// ResetCastVoices forgets the voices the casting chose for a book's
// characters (not those the user chose), e.g. when the book changes to
// another engine.
func (s *Store) ResetCastVoices(ctx context.Context, bookID int64) error {
	var cast []int64
	if err := s.db.WithContext(ctx).Model(&Character{}).
		Where("book_id = ? AND voice_manual = ? AND voice_recipe <> ''", bookID, false).
		Pluck("id", &cast).Error; err != nil {
		return err
	}
	return s.ClearCastVoices(ctx, cast...)
}

// ClearCastVoices forgets the voices the casting chose for characters
// (voices the user chose stay); the chapters they speak in get another
// voice for them.
func (s *Store) ClearCastVoices(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.TouchCharacterVoices(ctx, ids...); err != nil {
			return err
		}
		return tx.db.Model(&Character{}).Where("id IN ? AND voice_manual = ?", ids, false).
			Updates(map[string]any{"voice_recipe": "", "voice_speed": 1, "voice_note": ""}).Error
	})
}

// PromoteCharacters makes characters main characters unless the user chose
// their tier.
func (s *Store) PromoteCharacters(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Character{}).
		Where("id IN ? AND tier_manual = ?", ids, false).Update("tier", TierMain).Error
}

// AssignVoice stores the casting's recipe for a main character that has
// none yet, was not voiced by hand meanwhile and still has the gender the
// recipe was chosen for; it reports whether it did.
func (s *Store) AssignVoice(ctx context.Context, id int64, gender Gender, recipe string, speed float64, note string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&Character{}).
		Where("id = ? AND tier = ? AND voice_recipe = '' AND voice_manual = ? AND gender = ?",
			id, TierMain, false, gender).
		Updates(map[string]any{"voice_recipe": recipe, "voice_speed": speed, "voice_note": CleanText(note)})
	if res.Error != nil || res.RowsAffected == 0 {
		return false, res.Error
	}
	return true, s.TouchCharacterVoices(ctx, id)
}

// VoicedCharacters returns a book's characters with a recipe of their own.
func (s *Store) VoicedCharacters(ctx context.Context, bookID int64) ([]Character, error) {
	var cs []Character
	err := s.db.WithContext(ctx).Select("id", "name", "voice_recipe", "voice_speed").
		Where("book_id = ? AND voice_recipe <> ''", bookID).Order("id").Find(&cs).Error
	return cs, err
}

// CastingCounts summarizes the tiers of a book's characters.
type CastingCounts struct {
	Main, Minor int64
	// Voiced main characters (with a recipe).
	Voiced int64
}

// CastingCounts counts a book's main and minor characters.
func (s *Store) CastingCounts(ctx context.Context, bookID int64) (CastingCounts, error) {
	var rows []struct {
		Tier   Tier
		N      int64
		Voiced int64
	}
	err := s.db.WithContext(ctx).Model(&Character{}).
		Select("tier, COUNT(*) AS n, SUM(CASE WHEN voice_recipe <> '' THEN 1 ELSE 0 END) AS voiced").
		Where("book_id = ?", bookID).Group("tier").Scan(&rows).Error
	var c CastingCounts
	for _, r := range rows {
		if r.Tier == TierMain {
			c.Main, c.Voiced = r.N, r.Voiced
		} else {
			c.Minor += r.N
		}
	}
	return c, err
}

// MarkCastingDone finishes a running casting.
func (s *Store) MarkCastingDone(ctx context.Context, bookID int64) (bool, error) {
	return s.TransitionCasting(ctx, bookID, CastingDone,
		map[string]any{"casting_finished_at": time.Now().UTC(), "casting_error": ""}, CastingRunning)
}

// FailStaleCasting marks a casting that is active but has no job as
// failed, only if its status is still status and unchanged since
// updatedAt (a concurrent start wins).
func (s *Store) FailStaleCasting(ctx context.Context, bookID int64, status CastingStatus, updatedAt time.Time,
	msg string,
) (bool, error) {
	res := s.db.WithContext(ctx).Model(&Book{}).
		Where("id = ? AND casting_status = ? AND updated_at = ?", bookID, status, updatedAt).
		Updates(map[string]any{"casting_status": CastingFailed, "casting_error": msg})
	return res.RowsAffected == 1, res.Error
}
