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
	// PreviewText is what voice previews in this book say ("" = the
	// instance's sample text).
	PreviewText string `gorm:"not null;default:''"`
	// PrepareChapters: chapters are prepared by the LLM before they are
	// rendered (narration mood, pauses, spoken forms, pronunciations).
	PrepareChapters bool `gorm:"not null;default:false"`
	// CharacterVoices: quotations are spoken by their speakers' voices;
	// without, the narrator reads everything (no speaker attribution is
	// waited for).
	CharacterVoices bool `gorm:"not null;default:true"`

	// Character analysis (see AnalysisStatus); the progress is counted
	// from the analysis batches.
	AnalysisStatus     AnalysisStatus `gorm:"not null;default:'none'"`
	AnalysisError      string         `gorm:"not null;default:''"`
	AnalysisStartedAt  *time.Time
	AnalysisFinishedAt *time.Time

	// Voice casting (see CastingStatus).
	CastingStatus     CastingStatus `gorm:"not null;default:'none'"`
	CastingError      string        `gorm:"not null;default:''"`
	CastingFinishedAt *time.Time

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

// DownloadStatus is the state of an assembled audiobook file.
type DownloadStatus string

// Download states.
const (
	DownloadQueued     DownloadStatus = "queued" // also: waiting for chapters to be rendered
	DownloadAssembling DownloadStatus = "assembling"
	DownloadReady      DownloadStatus = "ready"
	DownloadFailed     DownloadStatus = "failed"
)

// Download is an M4B file with chapter markers: one per volume, or one for
// the whole book (VolumeID 0) when it has no volumes.
type Download struct {
	ID       int64          `gorm:"primaryKey"`
	BookID   int64          `gorm:"not null;uniqueIndex:idx_downloads_book_volume,priority:1"`
	VolumeID int64          `gorm:"not null;default:0;uniqueIndex:idx_downloads_book_volume,priority:2"`
	Status   DownloadStatus `gorm:"not null"`
	Error    string         `gorm:"not null;default:''"`
	// Path of the finished file (relative to the data dir); a previous file
	// stays available while a new one is assembled.
	Path       string `gorm:"not null;default:''"`
	Size       int64  `gorm:"not null;default:0"`
	DurationMs int64  `gorm:"not null;default:0"`
	// SourceKey identifies the chapter audio the file was built from; a
	// different key means the file is outdated.
	SourceKey string `gorm:"not null;default:''"`
	CreatedAt time.Time
	UpdatedAt time.Time
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
	// VoiceRev counts changes to who speaks the chapter's quotes with which
	// voice (attribution, speaker edits, character voices); AudioVoiceRev
	// is the revision the audio was rendered at.
	VoiceRev      int `gorm:"not null;default:0"`
	AudioVoiceRev int `gorm:"not null;default:0"`

	// Speaker attribution (see AttributionStatus). AttributionPart is the
	// number of paragraphs (from the start) already attributed: a long
	// chapter is sent in parts, a resumed job continues there.
	AttributionStatus AttributionStatus `gorm:"not null;default:'none'"`
	AttributionError  string            `gorm:"not null;default:''"`
	AttributionPrompt string            `gorm:"not null;default:''"`
	AttributionPart   int               `gorm:"not null;default:0"`
	AttributedAt      *time.Time

	// Preparation for reading aloud (see PrepStatus); PrepPart counts the
	// paragraphs already prepared, like AttributionPart.
	PrepStatus PrepStatus `gorm:"not null;default:'none'"`
	PrepError  string     `gorm:"not null;default:''"`
	PrepPrompt string     `gorm:"not null;default:''"`
	PrepPart   int        `gorm:"not null;default:0"`
	PreparedAt *time.Time

	// Narration (first/third person) and point-of-view character, from
	// the character analysis.
	Narration      Narration `gorm:"not null;default:''"`
	POVCharacterID *int64

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
	// Mood of narration from the chapter preparation ("" neutral).
	Mood string `gorm:"not null;default:''"`
	// Flows: the chapter preparation found it goes on from the paragraph
	// before (the same moment, scene and tone), so an engine may read the
	// two in one go.
	Flows bool `gorm:"not null;default:false"`
}

// SegmentKind classifies a segment.
type SegmentKind string

// Segment kinds.
const (
	SegmentNarration SegmentKind = "narration"
	SegmentQuote     SegmentKind = "quote"
	// SegmentThought is a character's thought (in italics or single
	// marks); like a quote it has a QuoteRef and is attributed.
	SegmentThought SegmentKind = "thought"
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
	// Emphasis lists the italic runs of Text as JSON [[start,end],…] in
	// runes ("" for none).
	Emphasis string `gorm:"not null;default:''"`
	// Speaker of a quote (nil: narrator voice — not attributed yet, or an
	// unknown speaker), how it is said and how sure the attribution is
	// (0..1). SpeakerManual protects a speaker the user chose.
	SpeakerCharacterID *int64  `gorm:"index"`
	Emotion            string  `gorm:"not null;default:''"`
	Confidence         float64 `gorm:"not null;default:0"`
	SpeakerManual      bool    `gorm:"not null;default:false"`
	// From the chapter preparation, for the TTS only (the text stays):
	// Pauses as JSON [[rune offset, ms],…] (an offset of len(text): after
	// the segment); SpokenAs as JSON [{"a":start,"b":end,"t":"said"},…].
	Pauses   string `gorm:"not null;default:''"`
	SpokenAs string `gorm:"not null;default:''"`
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
	ID     int64 `gorm:"primaryKey"`
	BookID int64 `gorm:"not null;uniqueIndex:idx_listen_progress_book_user,priority:1"`
	// UserID is the OIDC subject ("" without authentication).
	UserID     string `gorm:"not null;default:'';uniqueIndex:idx_listen_progress_book_user,priority:2"`
	ChapterID  int64  `gorm:"not null"`
	PositionMs int64  `gorm:"not null;default:0"`
	// SegmentID is the segment at the position: where reading along
	// continues (the only position of a chapter read without audio).
	SegmentID *int64
	UpdatedAt time.Time
}

// AppSetting is a setting of the instance changed in the UI.
type AppSetting struct {
	Name  string `gorm:"primaryKey"`
	Value string `gorm:"not null;default:''"`
}

// LexiconEntry tells how a book's word (a fantasy name) is pronounced:
// replaced by other text ("Ryoka" → "Ree-oh-ka"), or by phonemes in
// Kokoro's notation.
type LexiconEntry struct {
	ID          int64  `gorm:"primaryKey"`
	BookID      int64  `gorm:"not null;index"`
	Term        string `gorm:"not null"`
	Replacement string `gorm:"not null"`
	IsPhonemes  bool   `gorm:"not null;default:false"`
	// Status: LexiconActive, or a suggestion of the chapter preparation
	// (not applied until accepted) or a dismissed one (not suggested
	// again). Note says why it was suggested.
	Status    LexiconStatus `gorm:"not null;default:''"`
	Note      string        `gorm:"not null;default:''"`
	CreatedAt time.Time
	UpdatedAt time.Time
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

// AnalysisStatus is the state of a book's character analysis.
type AnalysisStatus string

// Analysis states.
const (
	AnalysisNone    AnalysisStatus = "none"
	AnalysisQueued  AnalysisStatus = "queued"
	AnalysisRunning AnalysisStatus = "running"
	AnalysisMerging AnalysisStatus = "merging"
	AnalysisPaused  AnalysisStatus = "paused"
	AnalysisDone    AnalysisStatus = "done"
	AnalysisFailed  AnalysisStatus = "failed"
)

// AttributionStatus is the state of a chapter's speaker attribution.
type AttributionStatus string

// Attribution states.
const (
	AttributionNone    AttributionStatus = "none"
	AttributionQueued  AttributionStatus = "queued"
	AttributionRunning AttributionStatus = "running"
	AttributionDone    AttributionStatus = "done"
	AttributionFailed  AttributionStatus = "failed"
)

// PrepStatus is the state of a chapter's preparation for reading aloud.
type PrepStatus string

// Preparation states.
const (
	PrepNone    PrepStatus = "none"
	PrepQueued  PrepStatus = "queued"
	PrepRunning PrepStatus = "running"
	PrepDone    PrepStatus = "done"
	PrepFailed  PrepStatus = "failed"
)

// LexiconStatus tells whether a lexicon entry applies.
type LexiconStatus string

// Lexicon entry states.
const (
	LexiconActive    LexiconStatus = ""
	LexiconSuggested LexiconStatus = "suggested"
	LexiconDismissed LexiconStatus = "dismissed"
)

// CastingStatus is the state of a book's voice casting.
type CastingStatus string

// Casting states.
const (
	CastingNone    CastingStatus = "none"
	CastingQueued  CastingStatus = "queued"
	CastingRunning CastingStatus = "running"
	CastingDone    CastingStatus = "done"
	CastingFailed  CastingStatus = "failed"
)

// Tier of a character.
type Tier string

// Tiers.
const (
	TierMain  Tier = "main"
	TierMinor Tier = "minor"
)

// Narration is how a chapter is told.
type Narration string

// Narration kinds ("" = not analysed yet).
const (
	NarrationFirstPerson Narration = "first_person"
	NarrationThirdPerson Narration = "third_person"
	NarrationMixed       Narration = "mixed"
	NarrationUnknown     Narration = "unknown"
)

// Gender of a character (what voice casting can use).
type Gender string

// Genders.
const (
	GenderFemale  Gender = "female"
	GenderMale    Gender = "male"
	GenderUnknown Gender = "unknown"
)

// AgeGroup of a character.
type AgeGroup string

// Age groups.
const (
	AgeChild   AgeGroup = "child"
	AgeTeen    AgeGroup = "teen"
	AgeAdult   AgeGroup = "adult"
	AgeElderly AgeGroup = "elderly"
	AgeUnknown AgeGroup = "unknown"
)

// Character is an entry of a book's character bible.
type Character struct {
	ID          int64    `gorm:"primaryKey"`
	BookID      int64    `gorm:"not null;index"`
	Name        string   `gorm:"not null"`
	Gender      Gender   `gorm:"not null;default:'unknown'"`
	AgeGroup    AgeGroup `gorm:"not null;default:'unknown'"`
	Role        string   `gorm:"not null;default:''"`
	Description string   `gorm:"not null;default:''"`
	SpeechStyle string   `gorm:"not null;default:''"`
	// Importance 1 (walk-on) … 5 (protagonist), the highest any analysis
	// batch gave.
	Importance int `gorm:"not null;default:1"`
	// Speaks is set once any batch saw the character speak.
	Speaks bool `gorm:"not null;default:false"`
	// Derived from CharacterChapter (RefreshCharacterStats).
	ChapterCount    int `gorm:"not null;default:0"`
	FirstChapterIdx int `gorm:"not null;default:0"`
	// UserEdited protects the profile from being overwritten by analysis.
	UserEdited bool `gorm:"not null;default:false"`
	// Tier decides how a character is voiced: main characters get their
	// own recipe, minor ones a pool voice. TierManual protects a tier the
	// user chose from the casting.
	Tier       Tier `gorm:"not null;default:'minor'"`
	TierManual bool `gorm:"not null;default:false"`
	// VoiceRecipe is the character's own Kokoro recipe ("" = pool voice);
	// VoiceManual protects one the user chose. VoiceNote is the casting's
	// reasoning.
	VoiceRecipe string  `gorm:"not null;default:''"`
	VoiceSpeed  float64 `gorm:"not null;default:1"`
	VoiceManual bool    `gorm:"not null;default:false"`
	VoiceNote   string  `gorm:"not null;default:''"`
	// PreviewText is what the voice previews say ("" = quotes of the
	// character).
	PreviewText string `gorm:"not null;default:''"`
	// A voice designed from a description: the job's state, the
	// description it was (or is being) designed from, and why it failed.
	DesignStatus      DesignStatus `gorm:"not null;default:''"`
	DesignDescription string       `gorm:"not null;default:''"`
	DesignError       string       `gorm:"not null;default:''"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// DesignStatus is the state of a character's voice design.
type DesignStatus string

// Voice design states ("" none).
const (
	DesignNone    DesignStatus = ""
	DesignQueued  DesignStatus = "queued"
	DesignRunning DesignStatus = "running"
	DesignDone    DesignStatus = "done"
	DesignFailed  DesignStatus = "failed"
)

// CharacterAlias is another name of a character. Norm is the normalized
// form used for matching (not unique: "the captain" may be ambiguous).
type CharacterAlias struct {
	ID          int64  `gorm:"primaryKey"`
	BookID      int64  `gorm:"not null;index:idx_character_aliases_book_norm,priority:1"`
	CharacterID int64  `gorm:"not null;index"`
	Alias       string `gorm:"not null"`
	Norm        string `gorm:"not null;index:idx_character_aliases_book_norm,priority:2"`
}

// CharacterChapter records that a character appears in a chapter.
type CharacterChapter struct {
	ID          int64 `gorm:"primaryKey"`
	BookID      int64 `gorm:"not null;index"`
	CharacterID int64 `gorm:"not null;uniqueIndex:idx_character_chapters_pair,priority:1"`
	ChapterID   int64 `gorm:"not null;uniqueIndex:idx_character_chapters_pair,priority:2;index"`
}

// CharacterDistinct records that two characters are different people (a
// merge candidate was rejected, or a character was split), so they are not
// proposed for merging again. AID < BID.
type CharacterDistinct struct {
	ID     int64 `gorm:"primaryKey"`
	BookID int64 `gorm:"not null;index"`
	AID    int64 `gorm:"column:a_id;not null;uniqueIndex:idx_character_distincts_pair,priority:1"`
	BID    int64 `gorm:"column:b_id;not null;uniqueIndex:idx_character_distincts_pair,priority:2"`
}

// BatchStatus is the state of an analysis batch.
type BatchStatus string

// Batch states.
const (
	BatchPending BatchStatus = "pending"
	BatchDone    BatchStatus = "done"
)

// AnalysisBatch is one LLM request of a book's character analysis: a run
// of chapters (or paragraphs of one huge chapter), analysed in reading
// order. The validated answer is kept, so results can be traced to a
// prompt version and provider.
type AnalysisBatch struct {
	ID     int64 `gorm:"primaryKey"`
	BookID int64 `gorm:"not null;index:idx_analysis_batches_order,priority:1"`
	// Reading order of the batch's first part.
	FirstChapterIdx int `gorm:"not null;index:idx_analysis_batches_order,priority:2"`
	FirstParaIdx    int `gorm:"not null;index:idx_analysis_batches_order,priority:3"`
	// Parts is a JSON array of {chapterId, fromPara, toPara} (paragraph
	// indexes, toPara exclusive and 0 = to the end).
	Parts        string      `gorm:"not null"`
	Tokens       int         `gorm:"not null;default:0"` // estimated input
	Status       BatchStatus `gorm:"not null;default:'pending'"`
	Prompt       string      `gorm:"not null;default:''"`
	Provider     string      `gorm:"not null;default:''"`
	Model        string      `gorm:"not null;default:''"`
	InputTokens  int         `gorm:"not null;default:0"`
	OutputTokens int         `gorm:"not null;default:0"`
	Result       string      `gorm:"not null;default:''"`
	CompletedAt  *time.Time
}
