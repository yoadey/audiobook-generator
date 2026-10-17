package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migrations is the ordered, append-only schema history. Never edit a
// migration that has been released; add a new one instead. Each migration
// declares its own snapshot of the structs it touches, so later changes to
// the models in models.go cannot alter what an old migration does.
var migrations = []*gormigrate.Migration{
	{
		ID: "202610150001_create_books",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				ID        int64  `gorm:"primaryKey"`
				Title     string `gorm:"not null"`
				Author    string `gorm:"not null;default:''"`
				Status    string `gorm:"not null;index"`
				CreatedAt time.Time
				UpdatedAt time.Time
			}
			return tx.Migrator().CreateTable(&Book{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("books")
		},
	},
	{
		ID: "202610150002_book_content_and_jobs",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				Language       string  `gorm:"not null;default:''"`
				EpubPath       string  `gorm:"not null;default:''"`
				CoverPath      string  `gorm:"not null;default:''"`
				CoverMediaType string  `gorm:"not null;default:''"`
				QuoteStyle     string  `gorm:"not null;default:''"`
				WordCount      int64   `gorm:"not null;default:0"`
				ChapterCount   int     `gorm:"not null;default:0"`
				Progress       float64 `gorm:"not null;default:0"`
				Error          string  `gorm:"not null;default:''"`
			}
			for _, col := range []string{"Language", "EpubPath", "CoverPath", "CoverMediaType",
				"QuoteStyle", "WordCount", "ChapterCount", "Progress", "Error"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}

			type Volume struct {
				ID     int64  `gorm:"primaryKey"`
				BookID int64  `gorm:"not null;index"`
				Idx    int    `gorm:"not null"`
				Title  string `gorm:"not null"`
			}
			type Chapter struct {
				ID         int64  `gorm:"primaryKey"`
				BookID     int64  `gorm:"not null;index:idx_chapters_book_idx,priority:1"`
				VolumeID   *int64 `gorm:"index"`
				Idx        int    `gorm:"not null;index:idx_chapters_book_idx,priority:2"`
				Title      string `gorm:"not null"`
				WordCount  int    `gorm:"not null;default:0"`
				QuoteCount int    `gorm:"not null;default:0"`
				Excluded   bool   `gorm:"not null;default:false"`
				Status     string
				CreatedAt  time.Time
				UpdatedAt  time.Time
			}
			type Paragraph struct {
				ID        int64  `gorm:"primaryKey"`
				ChapterID int64  `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:1"`
				Idx       int    `gorm:"not null;index:idx_paragraphs_chapter_idx,priority:2"`
				Kind      string `gorm:"not null"`
				Text      string `gorm:"not null"`
				WordCount int    `gorm:"not null;default:0"`
			}
			type Segment struct {
				ID          int64  `gorm:"primaryKey"`
				ChapterID   int64  `gorm:"not null;index:idx_segments_chapter_idx,priority:1"`
				ParagraphID int64  `gorm:"not null;index"`
				Idx         int    `gorm:"not null;index:idx_segments_chapter_idx,priority:2"`
				Kind        string `gorm:"not null"`
				QuoteRef    int    `gorm:"not null;default:0"`
				Text        string `gorm:"not null"`
			}
			type Job struct {
				ID          int64     `gorm:"primaryKey"`
				Type        string    `gorm:"not null"`
				Queue       string    `gorm:"not null;index:idx_jobs_claim,priority:1"`
				Status      string    `gorm:"not null;index:idx_jobs_claim,priority:2"`
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
			return tx.Migrator().CreateTable(&Volume{}, &Chapter{}, &Paragraph{}, &Segment{}, &Job{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("jobs", "segments", "paragraphs", "chapters", "volumes")
		},
	},
	{
		ID: "202610150003_audio_and_progress",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				NarratorVoice string  `gorm:"not null;default:''"`
				NarratorSpeed float64 `gorm:"not null;default:1"`
				Lookahead     int     `gorm:"not null;default:3"`
			}
			for _, col := range []string{"NarratorVoice", "NarratorSpeed", "Lookahead"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}
			type Chapter struct {
				AudioStatus     string `gorm:"not null;default:'none'"`
				AudioPath       string `gorm:"not null;default:''"`
				AudioDurationMs int64  `gorm:"not null;default:0"`
				AudioSize       int64  `gorm:"not null;default:0"`
				AudioError      string `gorm:"not null;default:''"`
				AudioVoice      string `gorm:"not null;default:''"`
				AudioUpdatedAt  *time.Time
			}
			for _, col := range []string{"AudioStatus", "AudioPath", "AudioDurationMs", "AudioSize",
				"AudioError", "AudioVoice", "AudioUpdatedAt"} {
				if err := tx.Migrator().AddColumn(&Chapter{}, col); err != nil {
					return fmt.Errorf("chapters.%s: %w", col, err)
				}
			}
			type ListenProgress struct {
				ID         int64 `gorm:"primaryKey"`
				BookID     int64 `gorm:"not null;uniqueIndex"`
				ChapterID  int64 `gorm:"not null"`
				PositionMs int64 `gorm:"not null;default:0"`
				UpdatedAt  time.Time
			}
			return tx.Migrator().CreateTable(&ListenProgress{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("listen_progresses")
		},
	},
	{
		ID: "202610160001_llm_providers",
		Migrate: func(tx *gorm.DB) error {
			type LLMProvider struct {
				ID               int64  `gorm:"primaryKey"`
				Name             string `gorm:"not null;uniqueIndex"`
				Source           string `gorm:"not null;default:'ui'"`
				Position         int    `gorm:"not null;default:0"`
				Enabled          bool   `gorm:"not null;default:true"`
				BaseURL          string `gorm:"not null"`
				Model            string `gorm:"not null"`
				APIKey           string `gorm:"not null;default:''"`
				APIKeyEnv        string `gorm:"not null;default:''"`
				StructuredOutput string `gorm:"not null;default:'json_schema'"`
				ExtraParams      string `gorm:"not null;default:''"`
				RPM              int    `gorm:"not null;default:0"`
				TPM              int    `gorm:"not null;default:0"`
				RPD              int    `gorm:"not null;default:0"`
				TPD              int    `gorm:"not null;default:0"`
				MaxOutputTokens  int    `gorm:"not null;default:0"`
				TimeoutSeconds   int    `gorm:"not null;default:300"`
				QuotaTimezone    string `gorm:"not null;default:'UTC'"`
				CreatedAt        time.Time
				UpdatedAt        time.Time
			}
			type LLMUsage struct {
				ID           int64  `gorm:"primaryKey"`
				ProviderID   int64  `gorm:"not null;uniqueIndex:idx_llm_usages_provider_day,priority:1"`
				Day          string `gorm:"not null;uniqueIndex:idx_llm_usages_provider_day,priority:2"`
				Requests     int64  `gorm:"not null;default:0"`
				InputTokens  int64  `gorm:"not null;default:0"`
				OutputTokens int64  `gorm:"not null;default:0"`
				Errors       int64  `gorm:"not null;default:0"`
				RateLimited  int64  `gorm:"not null;default:0"`
				UpdatedAt    time.Time
			}
			return tx.Migrator().CreateTable(&LLMProvider{}, &LLMUsage{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("llm_usages", "llm_providers")
		},
	},
	{
		ID: "202610170001_characters",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				AnalysisStatus     string `gorm:"not null;default:'none'"`
				AnalysisError      string `gorm:"not null;default:''"`
				AnalysisStartedAt  *time.Time
				AnalysisFinishedAt *time.Time
			}
			for _, col := range []string{"AnalysisStatus", "AnalysisError", "AnalysisStartedAt", "AnalysisFinishedAt"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}
			type Chapter struct {
				Narration      string `gorm:"not null;default:''"`
				POVCharacterID *int64
			}
			for _, col := range []string{"Narration", "POVCharacterID"} {
				if err := tx.Migrator().AddColumn(&Chapter{}, col); err != nil {
					return fmt.Errorf("chapters.%s: %w", col, err)
				}
			}
			type Character struct {
				ID              int64  `gorm:"primaryKey"`
				BookID          int64  `gorm:"not null;index"`
				Name            string `gorm:"not null"`
				Gender          string `gorm:"not null;default:'unknown'"`
				AgeGroup        string `gorm:"not null;default:'unknown'"`
				Role            string `gorm:"not null;default:''"`
				Description     string `gorm:"not null;default:''"`
				SpeechStyle     string `gorm:"not null;default:''"`
				Importance      int    `gorm:"not null;default:1"`
				Speaks          bool   `gorm:"not null;default:false"`
				ChapterCount    int    `gorm:"not null;default:0"`
				FirstChapterIdx int    `gorm:"not null;default:0"`
				UserEdited      bool   `gorm:"not null;default:false"`
				CreatedAt       time.Time
				UpdatedAt       time.Time
			}
			type CharacterAlias struct {
				ID          int64  `gorm:"primaryKey"`
				BookID      int64  `gorm:"not null;index:idx_character_aliases_book_norm,priority:1"`
				CharacterID int64  `gorm:"not null;index"`
				Alias       string `gorm:"not null"`
				Norm        string `gorm:"not null;index:idx_character_aliases_book_norm,priority:2"`
			}
			type CharacterChapter struct {
				ID          int64 `gorm:"primaryKey"`
				BookID      int64 `gorm:"not null;index"`
				CharacterID int64 `gorm:"not null;uniqueIndex:idx_character_chapters_pair,priority:1"`
				ChapterID   int64 `gorm:"not null;uniqueIndex:idx_character_chapters_pair,priority:2;index"`
			}
			type CharacterDistinct struct {
				ID     int64 `gorm:"primaryKey"`
				BookID int64 `gorm:"not null;index"`
				AID    int64 `gorm:"column:a_id;not null;uniqueIndex:idx_character_distincts_pair,priority:1"`
				BID    int64 `gorm:"column:b_id;not null;uniqueIndex:idx_character_distincts_pair,priority:2"`
			}
			type AnalysisBatch struct {
				ID              int64  `gorm:"primaryKey"`
				BookID          int64  `gorm:"not null;index:idx_analysis_batches_order,priority:1"`
				FirstChapterIdx int    `gorm:"not null;index:idx_analysis_batches_order,priority:2"`
				FirstParaIdx    int    `gorm:"not null;index:idx_analysis_batches_order,priority:3"`
				Parts           string `gorm:"not null"`
				Tokens          int    `gorm:"not null;default:0"`
				Status          string `gorm:"not null;default:'pending'"`
				Prompt          string `gorm:"not null;default:''"`
				Provider        string `gorm:"not null;default:''"`
				Model           string `gorm:"not null;default:''"`
				InputTokens     int    `gorm:"not null;default:0"`
				OutputTokens    int    `gorm:"not null;default:0"`
				Result          string `gorm:"not null;default:''"`
				CompletedAt     *time.Time
			}
			return tx.Migrator().CreateTable(&Character{}, &CharacterAlias{}, &CharacterChapter{},
				&CharacterDistinct{}, &AnalysisBatch{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Migrator().DropTable("analysis_batches", "character_distincts", "character_chapters",
				"character_aliases", "characters")
		},
	},
	{
		ID: "202610180001_casting",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				CastingStatus     string `gorm:"not null;default:'none'"`
				CastingError      string `gorm:"not null;default:''"`
				CastingFinishedAt *time.Time
			}
			for _, col := range []string{"CastingStatus", "CastingError", "CastingFinishedAt"} {
				if err := tx.Migrator().AddColumn(&Book{}, col); err != nil {
					return fmt.Errorf("books.%s: %w", col, err)
				}
			}
			type Character struct {
				Tier        string  `gorm:"not null;default:'minor'"`
				TierManual  bool    `gorm:"not null;default:false"`
				VoiceRecipe string  `gorm:"not null;default:''"`
				VoiceSpeed  float64 `gorm:"not null;default:1"`
				VoiceManual bool    `gorm:"not null;default:false"`
				VoiceNote   string  `gorm:"not null;default:''"`
			}
			for _, col := range []string{"Tier", "TierManual", "VoiceRecipe", "VoiceSpeed", "VoiceManual", "VoiceNote"} {
				if err := tx.Migrator().AddColumn(&Character{}, col); err != nil {
					return fmt.Errorf("characters.%s: %w", col, err)
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			type Character struct {
				Tier        string
				TierManual  bool
				VoiceRecipe string
				VoiceSpeed  float64
				VoiceManual bool
				VoiceNote   string
			}
			for _, col := range []string{"Tier", "TierManual", "VoiceRecipe", "VoiceSpeed", "VoiceManual", "VoiceNote"} {
				if err := tx.Migrator().DropColumn(&Character{}, col); err != nil {
					return err
				}
			}
			type Book struct {
				CastingStatus     string
				CastingError      string
				CastingFinishedAt *time.Time
			}
			for _, col := range []string{"CastingStatus", "CastingError", "CastingFinishedAt"} {
				if err := tx.Migrator().DropColumn(&Book{}, col); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		ID: "202610190001_attribution",
		Migrate: func(tx *gorm.DB) error {
			type Chapter struct {
				VoiceRev          int    `gorm:"not null;default:0"`
				AudioVoiceRev     int    `gorm:"not null;default:0"`
				AttributionStatus string `gorm:"not null;default:'none'"`
				AttributionError  string `gorm:"not null;default:''"`
				AttributionPrompt string `gorm:"not null;default:''"`
				AttributionPart   int    `gorm:"not null;default:0"`
				AttributedAt      *time.Time
			}
			for _, col := range []string{"VoiceRev", "AudioVoiceRev", "AttributionStatus", "AttributionError",
				"AttributionPrompt", "AttributionPart", "AttributedAt"} {
				if err := tx.Migrator().AddColumn(&Chapter{}, col); err != nil {
					return fmt.Errorf("chapters.%s: %w", col, err)
				}
			}
			type Segment struct {
				SpeakerCharacterID *int64  `gorm:"index"`
				Emotion            string  `gorm:"not null;default:''"`
				Confidence         float64 `gorm:"not null;default:0"`
				SpeakerManual      bool    `gorm:"not null;default:false"`
			}
			for _, col := range []string{"SpeakerCharacterID", "Emotion", "Confidence", "SpeakerManual"} {
				if err := tx.Migrator().AddColumn(&Segment{}, col); err != nil {
					return fmt.Errorf("segments.%s: %w", col, err)
				}
			}
			return tx.Migrator().CreateIndex(&Segment{}, "SpeakerCharacterID")
		},
		Rollback: func(tx *gorm.DB) error {
			type Segment struct {
				SpeakerCharacterID *int64 `gorm:"index"`
				Emotion            string
				Confidence         float64
				SpeakerManual      bool
			}
			if err := tx.Migrator().DropIndex(&Segment{}, "SpeakerCharacterID"); err != nil {
				return err
			}
			for _, col := range []string{"SpeakerCharacterID", "Emotion", "Confidence", "SpeakerManual"} {
				if err := tx.Migrator().DropColumn(&Segment{}, col); err != nil {
					return err
				}
			}
			type Chapter struct {
				VoiceRev          int
				AudioVoiceRev     int
				AttributionStatus string
				AttributionError  string
				AttributionPrompt string
				AttributionPart   int
				AttributedAt      *time.Time
			}
			for _, col := range []string{"VoiceRev", "AudioVoiceRev", "AttributionStatus", "AttributionError",
				"AttributionPrompt", "AttributionPart", "AttributedAt"} {
				if err := tx.Migrator().DropColumn(&Chapter{}, col); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		// Audio rendered before speaker attribution existed is narrated
		// only: chapters with quotations of analysed books are outdated.
		ID: "202610200001_outdate_narrated_audio",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`UPDATE chapters SET voice_rev = voice_rev + 1
				WHERE audio_path <> '' AND quote_count > 0
				AND book_id IN (SELECT id FROM books WHERE analysis_status = 'done')`).Error
		},
		Rollback: func(*gorm.DB) error { return nil },
	},
	{
		ID: "202610210001_lexicon_and_user_progress",
		Migrate: func(tx *gorm.DB) error {
			type LexiconEntry struct {
				ID          int64  `gorm:"primaryKey"`
				BookID      int64  `gorm:"not null;index"`
				Term        string `gorm:"not null"`
				Replacement string `gorm:"not null"`
				IsPhonemes  bool   `gorm:"not null;default:false"`
				CreatedAt   time.Time
				UpdatedAt   time.Time
			}
			if err := tx.Migrator().CreateTable(&LexiconEntry{}); err != nil {
				return err
			}
			// Listening progress per user (OIDC): unique per book and user.
			type ListenProgress struct {
				BookID int64  `gorm:"not null;uniqueIndex:idx_listen_progress_book_user,priority:1"`
				UserID string `gorm:"not null;default:'';uniqueIndex:idx_listen_progress_book_user,priority:2"`
			}
			if err := tx.Migrator().AddColumn(&ListenProgress{}, "UserID"); err != nil {
				return err
			}
			if err := tx.Migrator().DropIndex(&ListenProgress{}, "idx_listen_progresses_book_id"); err != nil {
				return err
			}
			return tx.Migrator().CreateIndex(&ListenProgress{}, "idx_listen_progress_book_user")
		},
		Rollback: func(tx *gorm.DB) error {
			type ListenProgress struct {
				BookID int64  `gorm:"not null;uniqueIndex"`
				UserID string `gorm:"uniqueIndex:idx_listen_progress_book_user,priority:2"`
			}
			if err := tx.Migrator().DropIndex(&ListenProgress{}, "idx_listen_progress_book_user"); err != nil {
				return err
			}
			if err := tx.Migrator().DropColumn(&ListenProgress{}, "UserID"); err != nil {
				return err
			}
			if err := tx.Migrator().CreateIndex(&ListenProgress{}, "BookID"); err != nil {
				return err
			}
			return tx.Migrator().DropTable("lexicon_entries")
		},
	},
	{
		ID: "202610220001_downloads",
		Migrate: func(tx *gorm.DB) error {
			type Download struct {
				ID         int64  `gorm:"primaryKey"`
				BookID     int64  `gorm:"not null;uniqueIndex:idx_downloads_book_volume,priority:1"`
				VolumeID   int64  `gorm:"not null;default:0;uniqueIndex:idx_downloads_book_volume,priority:2"`
				Status     string `gorm:"not null"`
				Error      string `gorm:"not null;default:''"`
				Path       string `gorm:"not null;default:''"`
				Size       int64  `gorm:"not null;default:0"`
				DurationMs int64  `gorm:"not null;default:0"`
				SourceKey  string `gorm:"not null;default:''"`
				CreatedAt  time.Time
				UpdatedAt  time.Time
			}
			return tx.Migrator().CreateTable(&Download{})
		},
		Rollback: func(tx *gorm.DB) error { return tx.Migrator().DropTable("downloads") },
	},
	{
		ID: "202610230001_reading_previews",
		Migrate: func(tx *gorm.DB) error {
			type ListenProgress struct {
				SegmentID *int64
			}
			type Book struct {
				PreviewText string `gorm:"not null;default:''"`
			}
			type Character struct {
				PreviewText string `gorm:"not null;default:''"`
			}
			type AppSetting struct {
				Name  string `gorm:"primaryKey"`
				Value string `gorm:"not null;default:''"`
			}
			type Segment struct {
				Emphasis string `gorm:"not null;default:''"`
			}
			if err := tx.Migrator().AddColumn(&Segment{}, "Emphasis"); err != nil {
				return err
			}
			if err := tx.Migrator().AddColumn(&ListenProgress{}, "SegmentID"); err != nil {
				return err
			}
			if err := tx.Migrator().AddColumn(&Book{}, "PreviewText"); err != nil {
				return err
			}
			if err := tx.Migrator().AddColumn(&Character{}, "PreviewText"); err != nil {
				return err
			}
			return tx.Migrator().CreateTable(&AppSetting{})
		},
		Rollback: func(tx *gorm.DB) error {
			type ListenProgress struct{ SegmentID *int64 }
			type Book struct{ PreviewText string }
			type Character struct{ PreviewText string }
			type Segment struct{ Emphasis string }
			if err := tx.Migrator().DropTable("app_settings"); err != nil {
				return err
			}
			if err := tx.Migrator().DropColumn(&Segment{}, "Emphasis"); err != nil {
				return err
			}
			if err := tx.Migrator().DropColumn(&Character{}, "PreviewText"); err != nil {
				return err
			}
			if err := tx.Migrator().DropColumn(&Book{}, "PreviewText"); err != nil {
				return err
			}
			return tx.Migrator().DropColumn(&ListenProgress{}, "SegmentID")
		},
	},
	{
		ID: "202610240001_chapter_preparation",
		Migrate: func(tx *gorm.DB) error {
			type Book struct {
				PrepareChapters bool `gorm:"not null;default:false"`
			}
			type Chapter struct {
				PrepStatus string `gorm:"not null;default:'none'"`
				PrepError  string `gorm:"not null;default:''"`
				PrepPrompt string `gorm:"not null;default:''"`
				PrepPart   int    `gorm:"not null;default:0"`
				PreparedAt *time.Time
			}
			type Paragraph struct {
				Mood string `gorm:"not null;default:''"`
			}
			type Segment struct {
				Pauses   string `gorm:"not null;default:''"`
				SpokenAs string `gorm:"not null;default:''"`
			}
			type LexiconEntry struct {
				Status string `gorm:"not null;default:''"`
				Note   string `gorm:"not null;default:''"`
			}
			for _, c := range []struct {
				model any
				cols  []string
			}{
				{&Book{}, []string{"PrepareChapters"}},
				{&Chapter{}, []string{"PrepStatus", "PrepError", "PrepPrompt", "PrepPart", "PreparedAt"}},
				{&Paragraph{}, []string{"Mood"}},
				{&Segment{}, []string{"Pauses", "SpokenAs"}},
				{&LexiconEntry{}, []string{"Status", "Note"}},
			} {
				for _, col := range c.cols {
					if err := tx.Migrator().AddColumn(c.model, col); err != nil {
						return err
					}
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			type Book struct{ PrepareChapters bool }
			type Chapter struct {
				PrepStatus, PrepError, PrepPrompt string
				PrepPart                          int
				PreparedAt                        *time.Time
			}
			type Paragraph struct{ Mood string }
			type Segment struct{ Pauses, SpokenAs string }
			type LexiconEntry struct{ Status, Note string }
			for _, c := range []struct {
				model any
				cols  []string
			}{
				{&LexiconEntry{}, []string{"Status", "Note"}},
				{&Segment{}, []string{"Pauses", "SpokenAs"}},
				{&Paragraph{}, []string{"Mood"}},
				{&Chapter{}, []string{"PrepStatus", "PrepError", "PrepPrompt", "PrepPart", "PreparedAt"}},
				{&Book{}, []string{"PrepareChapters"}},
			} {
				for _, col := range c.cols {
					if err := tx.Migrator().DropColumn(c.model, col); err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		ID: "202610250001_voice_design",
		Migrate: func(tx *gorm.DB) error {
			type Character struct {
				DesignStatus      string `gorm:"not null;default:''"`
				DesignDescription string `gorm:"not null;default:''"`
				DesignError       string `gorm:"not null;default:''"`
			}
			for _, col := range []string{"DesignStatus", "DesignDescription", "DesignError"} {
				if err := tx.Migrator().AddColumn(&Character{}, col); err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			type Character struct{ DesignStatus, DesignDescription, DesignError string }
			for _, col := range []string{"DesignError", "DesignDescription", "DesignStatus"} {
				if err := tx.Migrator().DropColumn(&Character{}, col); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// LatestSchemaVersion is the ID of the newest migration.
func LatestSchemaVersion() string { return migrations[len(migrations)-1].ID }

// Migrate applies all pending migrations.
func (s *Store) Migrate(ctx context.Context) error {
	m := gormigrate.New(s.db.WithContext(ctx), gormigrate.DefaultOptions, migrations)
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// SchemaVersion returns the ID of the last applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (string, error) {
	var row struct{ ID string }
	err := s.db.WithContext(ctx).
		Table(gormigrate.DefaultOptions.TableName).
		Select(gormigrate.DefaultOptions.IDColumnName + " AS id").
		Order(gormigrate.DefaultOptions.IDColumnName + " DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return row.ID, err
}
