package store

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInvalid is returned for operations that do not make sense, e.g.
// merging the last chapter.
var ErrInvalid = errors.New("invalid operation")

// ChapterFilter narrows ListChapters.
type ChapterFilter struct {
	VolumeID    *int64
	CharacterID *int64 // chapters the character appears in
}

// ListChapters returns a page of a book's chapters in reading order.
func (s *Store) ListChapters(ctx context.Context, bookID int64, f ChapterFilter, offset, limit int) ([]Chapter, int64, error) {
	q := s.db.WithContext(ctx).Model(&Chapter{}).Where("book_id = ?", bookID)
	if f.VolumeID != nil {
		q = q.Where("volume_id = ?", *f.VolumeID)
	}
	if f.CharacterID != nil {
		q = q.Where("id IN (?)", s.db.Session(&gorm.Session{NewDB: true}).Model(&CharacterChapter{}).
			Select("chapter_id").Where("character_id = ?", *f.CharacterID))
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var cs []Chapter
	err := q.Order("idx").Offset(offset).Limit(limit).Find(&cs).Error
	return cs, total, err
}

// GetChapter loads one chapter.
func (s *Store) GetChapter(ctx context.Context, id int64) (*Chapter, error) {
	var c Chapter
	if err := s.db.WithContext(ctx).Take(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// ChapterText returns a chapter's paragraphs and segments in order.
func (s *Store) ChapterText(ctx context.Context, chapterID int64) ([]Paragraph, []Segment, error) {
	db := s.db.WithContext(ctx)
	var ps []Paragraph
	if err := db.Where("chapter_id = ?", chapterID).Order("idx").Find(&ps).Error; err != nil {
		return nil, nil, err
	}
	var ss []Segment
	if err := db.Where("chapter_id = ?", chapterID).Order("idx").Find(&ss).Error; err != nil {
		return nil, nil, err
	}
	return ps, ss, nil
}

// AdjacentChapters returns the IDs of the previous and next chapter (0 when
// there is none).
func (s *Store) AdjacentChapters(ctx context.Context, c *Chapter) (prev, next int64, err error) {
	db := s.db.WithContext(ctx)
	var p, n Chapter
	err = db.Where("book_id = ? AND idx < ?", c.BookID, c.Idx).Order("idx DESC").Limit(1).Find(&p).Error
	if err != nil {
		return 0, 0, err
	}
	err = db.Where("book_id = ? AND idx > ?", c.BookID, c.Idx).Order("idx").Limit(1).Find(&n).Error
	return p.ID, n.ID, err
}

// ChapterUpdate holds optional changes to a chapter.
type ChapterUpdate struct {
	Title    *string
	Excluded *bool
}

// UpdateChapter applies u and returns the updated chapter.
func (s *Store) UpdateChapter(ctx context.Context, id int64, u ChapterUpdate) (*Chapter, error) {
	fields := map[string]any{}
	if u.Title != nil {
		fields["title"] = *u.Title
	}
	if u.Excluded != nil {
		fields["excluded"] = *u.Excluded
	}
	if len(fields) > 0 {
		res := s.db.WithContext(ctx).Model(&Chapter{ID: id}).Updates(fields)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, ErrNotFound
		}
	}
	return s.GetChapter(ctx, id)
}

// MergeChapterWithNext appends the following chapter to this one.
func (s *Store) MergeChapterWithNext(ctx context.Context, id int64) (*Chapter, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c, next Chapter
		if err := tx.Take(&c, id).Error; err != nil {
			return notFound(err)
		}
		err := tx.Where("book_id = ? AND idx > ?", c.BookID, c.Idx).Order("idx").Take(&next).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: last chapter has no next chapter", ErrInvalid)
		} else if err != nil {
			return err
		}

		paraOffset, segOffset, quoteOffset, err := contentEnd(tx, c.ID)
		if err != nil {
			return err
		}
		if err := tx.Model(&Paragraph{}).Where("chapter_id = ?", next.ID).Updates(map[string]any{
			"chapter_id": c.ID,
			"idx":        gorm.Expr("idx + ?", paraOffset),
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Segment{}).Where("chapter_id = ?", next.ID).Updates(map[string]any{
			"chapter_id": c.ID,
			"idx":        gorm.Expr("idx + ?", segOffset),
			"quote_ref":  gorm.Expr("CASE WHEN quote_ref > 0 THEN quote_ref + ? ELSE 0 END", quoteOffset),
		}).Error; err != nil {
			return err
		}
		if err := moveCharacterLinks(tx, c.BookID, next.ID, c.ID); err != nil {
			return err
		}
		if err := tx.Delete(&Chapter{}, next.ID).Error; err != nil {
			return err
		}
		if err := tx.Model(&Chapter{}).Where("book_id = ? AND idx > ?", c.BookID, next.Idx).
			UpdateColumn("idx", gorm.Expr("idx - 1")).Error; err != nil {
			return err
		}
		if err := refreshCharacterStatsTx(ctx, tx, c.BookID); err != nil {
			return err
		}
		return refreshChapterStats(tx, c.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetChapter(ctx, id)
}

// SplitChapter moves the paragraphs from paragraphID on into a new chapter
// directly after this one and returns the new chapter.
func (s *Store) SplitChapter(ctx context.Context, id, paragraphID int64, title string) (*Chapter, error) {
	var created Chapter
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c Chapter
		if err := tx.Take(&c, id).Error; err != nil {
			return notFound(err)
		}
		var p Paragraph
		if err := tx.Where("id = ? AND chapter_id = ?", paragraphID, id).Take(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: paragraph %d is not part of chapter %d", ErrInvalid, paragraphID, id)
			}
			return err
		}
		if p.Idx == 0 {
			return fmt.Errorf("%w: cannot split before the first paragraph", ErrInvalid)
		}
		var firstSeg Segment
		err := tx.Where("chapter_id = ? AND paragraph_id IN (?)", id,
			tx.Session(&gorm.Session{NewDB: true}).Model(&Paragraph{}).Select("id").
				Where("chapter_id = ? AND idx >= ?", id, p.Idx)).
			Order("idx").Limit(1).Find(&firstSeg).Error
		if err != nil {
			return err
		}
		var minQuote struct{ Q int }
		if err := tx.Model(&Segment{}).Select("COALESCE(MIN(quote_ref), 0) AS q").
			Where("chapter_id = ? AND idx >= ? AND quote_ref > 0", id, firstSeg.Idx).
			Scan(&minQuote).Error; err != nil {
			return err
		}

		if err := tx.Model(&Chapter{}).Where("book_id = ? AND idx > ?", c.BookID, c.Idx).
			UpdateColumn("idx", gorm.Expr("idx + 1")).Error; err != nil {
			return err
		}
		created = Chapter{
			BookID: c.BookID, VolumeID: c.VolumeID, Idx: c.Idx + 1, Title: title, Status: c.Status,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if firstSeg.ID != 0 {
			quoteShift := max(minQuote.Q-1, 0)
			if err := tx.Model(&Segment{}).Where("chapter_id = ? AND idx >= ?", id, firstSeg.Idx).Updates(map[string]any{
				"chapter_id": created.ID,
				"idx":        gorm.Expr("idx - ?", firstSeg.Idx),
				"quote_ref":  gorm.Expr("CASE WHEN quote_ref > 0 THEN quote_ref - ? ELSE 0 END", quoteShift),
			}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&Paragraph{}).Where("chapter_id = ? AND idx >= ?", id, p.Idx).Updates(map[string]any{
			"chapter_id": created.ID,
			"idx":        gorm.Expr("idx - ?", p.Idx),
		}).Error; err != nil {
			return err
		}
		if err := refreshChapterStats(tx, c.ID); err != nil {
			return err
		}
		// Both parts keep the characters of the chapter (which part they
		// appear in is unknown) and its narration.
		if err := copyCharacterLinks(tx, c.BookID, c.ID, created.ID); err != nil {
			return err
		}
		if err := tx.Model(&Chapter{ID: created.ID}).Updates(map[string]any{
			"narration": c.Narration, "pov_character_id": c.POVCharacterID,
		}).Error; err != nil {
			return err
		}
		if err := refreshCharacterStatsTx(ctx, tx, c.BookID); err != nil {
			return err
		}
		return refreshChapterStats(tx, created.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetChapter(ctx, created.ID)
}

// contentEnd returns the next paragraph index, next segment index and the
// highest quote number of a chapter.
func contentEnd(tx *gorm.DB, chapterID int64) (paras, segs, quotes int, err error) {
	var r struct{ N int }
	if err = tx.Model(&Paragraph{}).Select("COALESCE(MAX(idx) + 1, 0) AS n").
		Where("chapter_id = ?", chapterID).Scan(&r).Error; err != nil {
		return
	}
	paras = r.N
	if err = tx.Model(&Segment{}).Select("COALESCE(MAX(idx) + 1, 0) AS n").
		Where("chapter_id = ?", chapterID).Scan(&r).Error; err != nil {
		return
	}
	segs = r.N
	err = tx.Model(&Segment{}).Select("COALESCE(MAX(quote_ref), 0) AS n").
		Where("chapter_id = ?", chapterID).Scan(&r).Error
	quotes = r.N
	return
}

// refreshChapterStats recomputes word and quotation counts from the content.
func refreshChapterStats(tx *gorm.DB, chapterID int64) error {
	var words, quotes struct{ N int }
	if err := tx.Model(&Paragraph{}).Select("COALESCE(SUM(word_count), 0) AS n").
		Where("chapter_id = ?", chapterID).Scan(&words).Error; err != nil {
		return err
	}
	if err := tx.Model(&Segment{}).Select("COUNT(DISTINCT quote_ref) AS n").
		Where("chapter_id = ? AND quote_ref > 0", chapterID).Scan(&quotes).Error; err != nil {
		return err
	}
	return tx.Model(&Chapter{ID: chapterID}).Updates(map[string]any{
		"word_count":  words.N,
		"quote_count": quotes.N,
	}).Error
}

// ChapterIDs returns the IDs of a book's chapters.
func (s *Store) ChapterIDs(ctx context.Context, bookID int64) ([]int64, error) {
	var ids []int64
	err := s.db.WithContext(ctx).Model(&Chapter{}).Where("book_id = ?", bookID).Pluck("id", &ids).Error
	return ids, err
}

// copyCharacterLinks gives chapter to the characters of chapter from.
func copyCharacterLinks(tx *gorm.DB, bookID, from, to int64) error {
	var charIDs []int64
	if err := tx.Model(&CharacterChapter{}).Where("chapter_id = ?", from).Pluck("character_id", &charIDs).Error; err != nil {
		return err
	}
	if len(charIDs) == 0 {
		return nil
	}
	rows := make([]CharacterChapter, len(charIDs))
	for i, id := range charIDs {
		rows[i] = CharacterChapter{BookID: bookID, CharacterID: id, ChapterID: to}
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, batchSize).Error
}

// moveCharacterLinks moves appearances from a chapter that is merged away.
func moveCharacterLinks(tx *gorm.DB, bookID, from, to int64) error {
	if err := copyCharacterLinks(tx, bookID, from, to); err != nil {
		return err
	}
	return tx.Where("chapter_id = ?", from).Delete(&CharacterChapter{}).Error
}

// refreshCharacterStatsTx recomputes all character stats of a book after
// chapters moved (first appearances are chapter indexes).
func refreshCharacterStatsTx(ctx context.Context, tx *gorm.DB, bookID int64) error {
	return (&Store{db: tx}).RefreshBookCharacterStats(ctx, bookID)
}
