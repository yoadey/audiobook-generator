package store

import "time"

// BookStatus is the pipeline state of a book.
type BookStatus string

// Book states.
const (
	BookImporting BookStatus = "importing"
	BookImported  BookStatus = "imported"
	BookAnalyzing BookStatus = "analyzing"
	BookReady     BookStatus = "ready"
	BookFailed    BookStatus = "failed"
)

// Book is an uploaded EPUB.
type Book struct {
	ID       int64  `gorm:"primaryKey"`
	Title    string `gorm:"not null"`
	Author   string `gorm:"not null;default:''"`
	Language string `gorm:"not null;default:''"`
	Status   BookStatus
	// Paths are relative to the data directory.
	EpubPath       string `gorm:"not null;default:''"`
	CoverPath      string `gorm:"not null;default:''"`
	CoverMediaType string `gorm:"not null;default:''"`
	QuoteStyle     string `gorm:"not null;default:''"`
	WordCount      int64  `gorm:"not null;default:0"`
	ChapterCount   int    `gorm:"not null;default:0"`
	// Progress of the running import (0..1).
	Progress  float64 `gorm:"not null;default:0"`
	Error     string  `gorm:"not null;default:''"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Volume groups chapters (a top-level TOC entry).
type Volume struct {
	ID     int64  `gorm:"primaryKey"`
	BookID int64  `gorm:"not null;index"`
	Idx    int    `gorm:"not null"`
	Title  string `gorm:"not null"`
}

// ChapterStatus is the processing state of a chapter.
type ChapterStatus string

// Chapter states (later milestones add attribution and rendering states).
const (
	ChapterImported ChapterStatus = "imported"
)

// Chapter is one chapter of a book; Idx orders chapters within the book.
type Chapter struct {
	ID         int64  `gorm:"primaryKey"`
	BookID     int64  `gorm:"not null;index:idx_chapters_book_idx,priority:1"`
	VolumeID   *int64 `gorm:"index"`
	Idx        int    `gorm:"not null;index:idx_chapters_book_idx,priority:2"`
	Title      string `gorm:"not null"`
	WordCount  int    `gorm:"not null;default:0"`
	QuoteCount int    `gorm:"not null;default:0"`
	Excluded   bool   `gorm:"not null;default:false"`
	Status     ChapterStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ParagraphKind classifies a paragraph.
type ParagraphKind string

// Paragraph kinds.
const (
	ParagraphText       ParagraphKind = "text"
	ParagraphHeading    ParagraphKind = "heading"
	ParagraphSceneBreak ParagraphKind = "scene_break"
)

// Paragraph is one block of a chapter.
type Paragraph struct {
	ID        int64         `gorm:"primaryKey"`
	ChapterID int64         `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:1"`
	Idx       int           `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:2"`
	Kind      ParagraphKind `gorm:"not null"`
	Text      string        `gorm:"not null"`
	WordCount int           `gorm:"not null;default:0"`
}

// SegmentKind classifies a segment.
type SegmentKind string

// Segment kinds.
const (
	SegmentNarration SegmentKind = "narration"
	SegmentQuote     SegmentKind = "quote"
)

// Segment is the unit of speech: a TTS-sized piece of narration or of one
// quotation. Idx orders segments within the chapter.
type Segment struct {
	ID          int64       `gorm:"primaryKey"`
	ChapterID   int64       `gorm:"not null;index:idx_segments_chapter_idx,priority:1"`
	ParagraphID int64       `gorm:"not null;index"`
	Idx         int         `gorm:"not null;index:idx_segments_chapter_idx,priority:2"`
	Kind        SegmentKind `gorm:"not null"`
	// QuoteRef numbers the quotations of a chapter (0 for narration);
	// pieces of one quotation share the number.
	QuoteRef int    `gorm:"not null;default:0"`
	Text     string `gorm:"not null"`
}

// JobStatus is the state of a job.
type JobStatus string

// Job states.
const (
	JobQueued   JobStatus = "queued"
	JobRunning  JobStatus = "running"
	JobDone     JobStatus = "done"
	JobFailed   JobStatus = "failed"
	JobCanceled JobStatus = "canceled"
)

// Job is a persistent unit of background work.
type Job struct {
	ID          int64     `gorm:"primaryKey"`
	Type        string    `gorm:"not null"`
	Queue       string    `gorm:"not null;index:idx_jobs_claim,priority:1"`
	Status      JobStatus `gorm:"not null;index:idx_jobs_claim,priority:2"`
	Priority    int       `gorm:"not null;default:0"`
	BookID      *int64    `gorm:"index"`
	ChapterID   *int64    `gorm:"index"`
	Payload     string    `gorm:"not null;default:''"`
	Attempts    int       `gorm:"not null;default:0"`
	MaxAttempts int       `gorm:"not null;default:3"`
	NotBefore   time.Time `gorm:"not null"`
	Progress    float64   `gorm:"not null;default:0"`
	Error       string    `gorm:"not null;default:''"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
}
