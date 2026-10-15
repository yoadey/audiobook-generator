package store

import "time"

// BookStatus is the pipeline state of a book.
type BookStatus string

// Book states.
const (
	BookImported  BookStatus = "imported"
	BookAnalyzing BookStatus = "analyzing"
	BookReady     BookStatus = "ready"
	BookFailed    BookStatus = "failed"
)

// Book is an imported EPUB.
type Book struct {
	ID        int64  `gorm:"primaryKey"`
	Title     string `gorm:"not null"`
	Author    string `gorm:"not null;default:''"`
	Status    BookStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}
