package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// CreateBook inserts a book and fills in its ID and timestamps.
func (s *Store) CreateBook(ctx context.Context, b *Book) error {
	return s.db.WithContext(ctx).Create(b).Error
}

// GetBook loads one book.
func (s *Store) GetBook(ctx context.Context, id int64) (*Book, error) {
	var b Book
	if err := s.db.WithContext(ctx).Take(&b, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &b, nil
}

// ListBooks returns a page of books, newest first, plus the total count.
func (s *Store) ListBooks(ctx context.Context, offset, limit int) ([]Book, int64, error) {
	var (
		books []Book
		total int64
	)
	q := s.db.WithContext(ctx).Model(&Book{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&books).Error
	return books, total, err
}

// UpdateBook sets the given columns (by Go field name) on a book.
func (s *Store) UpdateBook(ctx context.Context, id int64, fields map[string]any) error {
	res := s.db.WithContext(ctx).Model(&Book{ID: id}).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// BooksWithoutDescription returns the imported books whose description
// was never read.
func (s *Store) BooksWithoutDescription(ctx context.Context) ([]Book, error) {
	var books []Book
	err := s.db.WithContext(ctx).
		Where("description IS NULL AND status IN ?", []BookStatus{BookImported, BookReady}).
		Order("id").Find(&books).Error
	return books, err
}

// DeleteBook removes a book with its content and jobs.
func (s *Store) DeleteBook(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteBookContent(tx, id); err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", id).Delete(&Job{}).Error; err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", id).Delete(&ListenProgress{}).Error; err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", id).Delete(&LexiconEntry{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&Book{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
