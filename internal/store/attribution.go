package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// TransitionAttribution sets a chapter's attribution status to `to` (plus
// extra columns) only if it is one of from; it reports whether it changed.
func (s *Store) TransitionAttribution(ctx context.Context, chapterID int64, to AttributionStatus,
	extra map[string]any, from ...AttributionStatus,
) (bool, error) {
	fields := map[string]any{"attribution_status": to}
	for k, v := range extra {
		fields[k] = v
	}
	res := s.db.WithContext(ctx).Model(&Chapter{}).Where("id = ? AND attribution_status IN ?", chapterID, from).
		Updates(fields)
	return res.RowsAffected == 1, res.Error
}

// Attribution is the speaker of one quotation.
type Attribution struct {
	QuoteRef    int
	CharacterID *int64 // nil: unknown speaker
	Emotion     string
	Confidence  float64
}

// ApplyAttribution stores speakers of a chapter's quotations (all pieces of
// each), except where the user chose the speaker, and marks the part as
// done. It bumps the chapter's voice revision.
func (s *Store) ApplyAttribution(ctx context.Context, chapterID int64, as []Attribution, partsDone int) error {
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		for _, a := range as {
			if err := db.Model(&Segment{}).
				Where("chapter_id = ? AND quote_ref = ? AND speaker_manual = ?", chapterID, a.QuoteRef, false).
				Updates(map[string]any{
					"speaker_character_id": a.CharacterID, "emotion": a.Emotion, "confidence": a.Confidence,
				}).Error; err != nil {
				return err
			}
		}
		return db.Model(&Chapter{ID: chapterID}).Updates(map[string]any{
			"attribution_part": partsDone, "voice_rev": gorm.Expr("voice_rev + 1"),
		}).Error
	})
}

// ResetAttribution forgets the automatic speakers of chapters (the user's
// choices stay) so they are attributed again.
func (s *Store) ResetAttribution(ctx context.Context, chapterIDs ...int64) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		if err := db.Model(&Segment{}).Where("chapter_id IN ? AND speaker_manual = ?", chapterIDs, false).
			Updates(map[string]any{"speaker_character_id": nil, "emotion": "", "confidence": 0}).Error; err != nil {
			return err
		}
		return db.Model(&Chapter{}).Where("id IN ?", chapterIDs).Updates(map[string]any{
			"attribution_status": AttributionNone, "attribution_error": "", "attribution_part": 0,
			"attributed_at": nil, "voice_rev": gorm.Expr("voice_rev + 1"),
		}).Error
	})
}

// SetQuoteSpeaker sets the speaker (nil: narrator) and emotion of every
// piece of a chapter's quotation by hand.
func (s *Store) SetQuoteSpeaker(ctx context.Context, chapterID int64, quoteRef int, characterID *int64,
	emotion string,
) error {
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		res := db.Model(&Segment{}).Where("chapter_id = ? AND quote_ref = ?", chapterID, quoteRef).
			Updates(map[string]any{
				"speaker_character_id": characterID, "emotion": emotion, "confidence": 1, "speaker_manual": true,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return db.Model(&Chapter{ID: chapterID}).Update("voice_rev", gorm.Expr("voice_rev + 1")).Error
	})
}

// GetSegment loads one segment.
func (s *Store) GetSegment(ctx context.Context, id int64) (*Segment, error) {
	var seg Segment
	if err := s.db.WithContext(ctx).Take(&seg, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &seg, nil
}

// TouchCharacterVoices bumps the voice revision of every chapter in which
// one of the characters speaks (its voice changed).
func (s *Store) TouchCharacterVoices(ctx context.Context, characterIDs ...int64) error {
	if len(characterIDs) == 0 {
		return nil
	}
	db := s.db.WithContext(ctx)
	return db.Model(&Chapter{}).Where("id IN (?)", db.Session(&gorm.Session{NewDB: true}).Model(&Segment{}).
		Select("chapter_id").Where("speaker_character_id IN ?", characterIDs)).
		Update("voice_rev", gorm.Expr("voice_rev + 1")).Error
}

// TouchBookVoices bumps the voice revision of all chapters of a book (a
// change that may affect every voice, e.g. a recast).
func (s *Store) TouchBookVoices(ctx context.Context, bookID int64) error {
	return s.db.WithContext(ctx).Model(&Chapter{}).Where("book_id = ?", bookID).
		Update("voice_rev", gorm.Expr("voice_rev + 1")).Error
}

// Speaker is a recent speaker of a book.
type Speaker struct {
	CharacterID int64
	Name        string
}

// LastSpeakers returns the speakers of the last quotations before chapter
// idx (most recent first, each once, at most limit).
func (s *Store) LastSpeakers(ctx context.Context, bookID int64, idx, limit int) ([]Speaker, error) {
	var prev Chapter
	err := s.db.WithContext(ctx).Where("book_id = ? AND idx < ? AND excluded = ?", bookID, idx, false).
		Order("idx DESC").Take(&prev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var rows []struct {
		CharacterID int64
		Name        string
	}
	err = s.db.WithContext(ctx).Table("segments s").
		Select("s.speaker_character_id AS character_id, c.name").
		Joins("JOIN characters c ON c.id = s.speaker_character_id").
		Where("s.chapter_id = ? AND s.quote_ref > 0", prev.ID).
		Order("s.idx DESC").Limit(200).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var out []Speaker
	seen := map[int64]bool{}
	for _, r := range rows {
		if !seen[r.CharacterID] {
			seen[r.CharacterID] = true
			out = append(out, Speaker(r))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// AttributionCounts summarizes the quotations of a chapter.
type AttributionCounts struct {
	Quotes, Attributed, Unsure int
}

// QuoteStats counts a chapter's quotations, those with a speaker, and
// those attributed automatically with a confidence below unsure.
func (s *Store) QuoteStats(ctx context.Context, chapterID int64, unsure float64) (AttributionCounts, error) {
	var r struct {
		Quotes, Attributed, Unsure int
	}
	err := s.db.WithContext(ctx).Model(&Segment{}).
		Select("COUNT(DISTINCT quote_ref) AS quotes, "+
			"COUNT(DISTINCT CASE WHEN speaker_character_id IS NOT NULL THEN quote_ref END) AS attributed, "+
			"COUNT(DISTINCT CASE WHEN speaker_manual = ? AND confidence < ? THEN quote_ref END) AS unsure",
			false, unsure).
		Where("chapter_id = ? AND quote_ref > 0", chapterID).Scan(&r).Error
	return AttributionCounts(r), err
}

// MarkAttributed finishes a running attribution.
func (s *Store) MarkAttributed(ctx context.Context, chapterID int64, prompt string) (bool, error) {
	return s.TransitionAttribution(ctx, chapterID, AttributionDone, map[string]any{
		"attributed_at": time.Now().UTC(), "attribution_error": "", "attribution_prompt": prompt,
	}, AttributionRunning)
}

// SpokenQuote is a quotation attributed to a character.
type SpokenQuote struct {
	ChapterID int64
	QuoteRef  int
	Text      string
}

// SpokenQuotes returns quotations attributed to a character with
// confidence (or by hand), in reading order, at most limit.
func (s *Store) SpokenQuotes(ctx context.Context, characterID int64, minConfidence float64, limit int,
) ([]SpokenQuote, error) {
	var segs []Segment
	err := s.db.WithContext(ctx).Table("segments s").Select("s.*").
		Joins("JOIN chapters c ON c.id = s.chapter_id").
		Where("s.speaker_character_id = ? AND s.quote_ref > 0 AND (s.speaker_manual = ? OR s.confidence >= ?)",
			characterID, true, minConfidence).
		Order("c.idx, s.idx").Limit(limit * 8).Find(&segs).Error
	if err != nil {
		return nil, err
	}
	var out []SpokenQuote
	for _, seg := range segs {
		if n := len(out); n > 0 && out[n-1].ChapterID == seg.ChapterID && out[n-1].QuoteRef == seg.QuoteRef {
			out[n-1].Text += " " + seg.Text
			continue
		}
		out = append(out, SpokenQuote{ChapterID: seg.ChapterID, QuoteRef: seg.QuoteRef, Text: seg.Text})
	}
	return out, nil
}
