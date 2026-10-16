package store

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ListLexicon returns a book's pronunciation entries by term.
func (s *Store) ListLexicon(ctx context.Context, bookID int64) ([]LexiconEntry, error) {
	var es []LexiconEntry
	err := s.db.WithContext(ctx).Where("book_id = ?", bookID).Order("LOWER(term), id").Find(&es).Error
	return es, err
}

// GetLexiconEntry loads one entry.
func (s *Store) GetLexiconEntry(ctx context.Context, id int64) (*LexiconEntry, error) {
	var e LexiconEntry
	if err := s.db.WithContext(ctx).Take(&e, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &e, nil
}

// LexiconTermTaken reports whether another entry of the book has the term
// (ignoring case; compared in Go: SQLite's LOWER() folds ASCII only).
func (s *Store) LexiconTermTaken(ctx context.Context, bookID, exceptID int64, term string) (bool, error) {
	var terms []string
	if err := s.db.WithContext(ctx).Model(&LexiconEntry{}).Where("book_id = ? AND id <> ?", bookID, exceptID).
		Pluck("term", &terms).Error; err != nil {
		return false, err
	}
	for _, t := range terms {
		if strings.EqualFold(t, term) {
			return true, nil
		}
	}
	return false, nil
}

// SaveLexiconEntry creates or updates an entry; a term may exist once per
// book (ignoring case), else ErrConflict.
func (s *Store) SaveLexiconEntry(ctx context.Context, e *LexiconEntry) error {
	return s.Tx(ctx, func(tx *Store) error {
		if taken, err := tx.LexiconTermTaken(ctx, e.BookID, e.ID, e.Term); err != nil {
			return err
		} else if taken {
			return fmt.Errorf("%w: %q is already in the lexicon", ErrConflict, e.Term)
		}
		return tx.db.Save(e).Error
	})
}

// DeleteLexiconEntry removes an entry.
func (s *Store) DeleteLexiconEntry(ctx context.Context, id int64) error {
	res := s.db.WithContext(ctx).Delete(&LexiconEntry{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchMatchingChapters bumps the voice revision of a book's chapters whose
// spoken text (title or paragraphs) match reports (their pronunciation
// changed), so they are rendered again. The text is matched in Go, the
// same way rendering applies the lexicon: SQL case folding differs between
// the dialects.
func (s *Store) TouchMatchingChapters(ctx context.Context, bookID int64, match func(text string) bool) error {
	db := s.db.WithContext(ctx)
	var chapters []Chapter
	if err := db.Select("id, title").Where("book_id = ?", bookID).Find(&chapters).Error; err != nil {
		return err
	}
	hit := map[int64]bool{}
	for _, c := range chapters {
		if match(c.Title) {
			hit[c.ID] = true
		}
	}
	ids := chapterIDs(s.db.Session(&gorm.Session{NewDB: true}), bookID)
	var last int64
	for {
		var paras []Paragraph
		if err := db.Select("id, chapter_id, text").Where("chapter_id IN (?) AND id > ?", ids, last).
			Order("id").Limit(2000).Find(&paras).Error; err != nil {
			return err
		}
		for _, p := range paras {
			if !hit[p.ChapterID] && match(p.Text) {
				hit[p.ChapterID] = true
			}
		}
		if len(paras) < 2000 {
			break
		}
		last = paras[len(paras)-1].ID
	}
	touched := make([]int64, 0, len(hit))
	for id := range hit {
		touched = append(touched, id)
	}
	for start := 0; start < len(touched); start += 500 {
		if err := db.Model(&Chapter{}).Where("id IN ?", touched[start:min(start+500, len(touched))]).
			Update("voice_rev", gorm.Expr("voice_rev + 1")).Error; err != nil {
			return err
		}
	}
	return nil
}
