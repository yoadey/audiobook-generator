package store

import "context"

// CreateBook inserts a book and fills in its ID and timestamps.
func (s *Store) CreateBook(ctx context.Context, b *Book) error {
	return s.db.WithContext(ctx).Create(b).Error
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
