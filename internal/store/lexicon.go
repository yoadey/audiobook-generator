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

// SaveLexiconEntry creates or updates an entry; a term may exist once per
// book (ignoring case), else ErrConflict.
func (s *Store) SaveLexiconEntry(ctx context.Context, e *LexiconEntry) error {
	return s.Tx(ctx, func(tx *Store) error {
		var n int64
		if err := tx.db.Model(&LexiconEntry{}).
			Where("book_id = ? AND LOWER(term) = ? AND id <> ?", e.BookID, strings.ToLower(e.Term), e.ID).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
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

// TouchTermChapters bumps the voice revision of a book's chapters whose
// text contains one of the terms (their pronunciation changed).
func (s *Store) TouchTermChapters(ctx context.Context, bookID int64, terms ...string) error {
	db := s.db.WithContext(ctx)
	for _, t := range terms {
		if strings.TrimSpace(t) == "" {
			continue
		}
		sub := db.Session(&gorm.Session{NewDB: true}).Model(&Paragraph{}).Select("chapter_id").
			Where(`LOWER(text) LIKE ? ESCAPE '\'`, likePattern(strings.TrimSpace(t)))
		if err := db.Model(&Chapter{}).Where("book_id = ? AND id IN (?)", bookID, sub).
			Update("voice_rev", gorm.Expr("voice_rev + 1")).Error; err != nil {
			return err
		}
	}
	return nil
}
