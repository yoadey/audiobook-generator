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
	Progress float64 `gorm:"not null;default:0"`
	Error    string  `gorm:"not null;default:''"`
	// NarratorVoice is a Kokoro voice recipe ("af_heart" or a weighted mix);
	// empty means the configured default.
	NarratorVoice string  `gorm:"not null;default:''"`
	NarratorSpeed float64 `gorm:"not null;default:1"`
	// Lookahead is how many chapters after the listening position are kept
	// rendered.
	Lookahead int `gorm:"not null;default:3"`
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

// Chapter states (later milestones add attribution states).
const (
	ChapterImported ChapterStatus = "imported"
)

// AudioStatus is the rendering state of a chapter's audio.
type AudioStatus string

// Audio states.
const (
	AudioNone      AudioStatus = "none"
	AudioQueued    AudioStatus = "queued"
	AudioRendering AudioStatus = "rendering"
	AudioReady     AudioStatus = "ready"
	AudioFailed    AudioStatus = "failed"
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

	AudioStatus     AudioStatus `gorm:"not null;default:'none'"`
	AudioPath       string      `gorm:"not null;default:''"` // relative to the data dir
	AudioDurationMs int64       `gorm:"not null;default:0"`
	AudioSize       int64       `gorm:"not null;default:0"`
	AudioError      string      `gorm:"not null;default:''"`
	// AudioVoice is the voice key (recipe@speed) the audio was rendered with.
	AudioVoice     string `gorm:"not null;default:''"`
	AudioUpdatedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
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

// ListenProgress is the playback position in a book.
type ListenProgress struct {
	ID         int64 `gorm:"primaryKey"`
	BookID     int64 `gorm:"not null;uniqueIndex"`
	ChapterID  int64 `gorm:"not null"`
	PositionMs int64 `gorm:"not null;default:0"`
	UpdatedAt  time.Time
}

// Structured-output modes of an LLM provider.
const (
	// StructuredJSONSchema sends the JSON schema as response_format.
	StructuredJSONSchema = "json_schema"
	// StructuredJSONObject asks for any JSON object; the schema goes into
	// the prompt.
	StructuredJSONObject = "json_object"
	// StructuredPrompt sends no response_format at all.
	StructuredPrompt = "prompt"
)

// Sources of an LLM provider definition.
const (
	ProviderSourceUI     = "ui"
	ProviderSourceConfig = "config"
)

// LLMProvider is an OpenAI-compatible chat completions endpoint. Providers
// are tried in Position order; limits of 0 mean "no limit".
type LLMProvider struct {
	ID       int64  `gorm:"primaryKey"`
	Name     string `gorm:"not null;uniqueIndex"`
	Source   string `gorm:"not null;default:'ui'"`
	Position int    `gorm:"not null;default:0"`
	// No GORM default here: it would turn an explicit false into true on
	// insert (the column default lives in the migration).
	Enabled bool   `gorm:"not null"`
	BaseURL string `gorm:"not null"`
	Model   string `gorm:"not null"`
	// APIKey is stored for providers created in the UI; providers from the
	// Helm values name an environment variable instead (APIKeyEnv).
	APIKey           string `gorm:"not null;default:''"`
	APIKeyEnv        string `gorm:"not null;default:''"`
	StructuredOutput string `gorm:"not null;default:'json_schema'"`
	// ExtraParams is a JSON object merged into every request body
	// (e.g. {"reasoning_effort": "low"}).
	ExtraParams     string `gorm:"not null;default:''"`
	RPM             int    `gorm:"not null;default:0"`
	TPM             int    `gorm:"not null;default:0"`
	RPD             int    `gorm:"not null;default:0"`
	TPD             int    `gorm:"not null;default:0"`
	MaxOutputTokens int    `gorm:"not null;default:0"`
	TimeoutSeconds  int    `gorm:"not null;default:300"`
	// QuotaTimezone is where the provider's daily quota resets at midnight.
	QuotaTimezone string `gorm:"not null;default:'UTC'"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// LLMUsage counts the requests and tokens of a provider on one day (in
// the provider's quota time zone).
type LLMUsage struct {
	ID           int64  `gorm:"primaryKey"`
	ProviderID   int64  `gorm:"not null;uniqueIndex:idx_llm_usages_provider_day,priority:1"`
	Day          string `gorm:"not null;uniqueIndex:idx_llm_usages_provider_day,priority:2"` // YYYY-MM-DD
	Requests     int64  `gorm:"not null;default:0"`
	InputTokens  int64  `gorm:"not null;default:0"`
	OutputTokens int64  `gorm:"not null;default:0"`
	// Errors counts failed requests, RateLimited the 429 answers among them.
	Errors      int64 `gorm:"not null;default:0"`
	RateLimited int64 `gorm:"not null;default:0"`
	UpdatedAt   time.Time
}
