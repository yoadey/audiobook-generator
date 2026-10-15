package store

import (
	"context"

	"gorm.io/gorm"
)

// Rows per INSERT; stays well below SQLite's and PostgreSQL's bind limits.
const batchSize = 500

// ChapterContent is a chapter with its paragraphs and segments, used when
// importing. Segment.ParagraphID holds the index into Paragraphs until the
// paragraphs have IDs.
type ChapterContent struct {
	Chapter    Chapter
	Paragraphs []Paragraph
	Segments   []Segment
}

func chapterIDs(tx *gorm.DB, bookID int64) *gorm.DB {
	return tx.Model(&Chapter{}).Select("id").Where("book_id = ?", bookID)
}

func deleteBookContent(tx *gorm.DB, bookID int64) error {
	// The analysis refers to chapters and is invalid without them.
	if err := deleteAnalysis(tx, bookID); err != nil {
		return err
	}
	ids := chapterIDs(tx.Session(&gorm.Session{NewDB: true}), bookID)
	if err := tx.Where("chapter_id IN (?)", ids).Delete(&Segment{}).Error; err != nil {
		return err
	}
	if err := tx.Where("chapter_id IN (?)", ids).Delete(&Paragraph{}).Error; err != nil {
		return err
	}
	if err := tx.Where("book_id = ?", bookID).Delete(&Chapter{}).Error; err != nil {
		return err
	}
	return tx.Where("book_id = ?", bookID).Delete(&Volume{}).Error
}

// ResetBookContent deletes a book's volumes, chapters, paragraphs and
// segments (an import starts from scratch, so it is safe to retry).
func (s *Store) ResetBookContent(ctx context.Context, bookID int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteBookContent(tx, bookID)
	})
}

// CreateVolumes inserts volumes and fills in their IDs.
func (s *Store) CreateVolumes(ctx context.Context, vs []Volume) error {
	if len(vs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).CreateInBatches(vs, batchSize).Error
}

// CreateChapterContent inserts a chapter with its paragraphs and segments
// in one transaction.
func (s *Store) CreateChapterContent(ctx context.Context, c *ChapterContent) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&c.Chapter).Error; err != nil {
			return err
		}
		for i := range c.Paragraphs {
			c.Paragraphs[i].ChapterID = c.Chapter.ID
		}
		if len(c.Paragraphs) > 0 {
			if err := tx.CreateInBatches(c.Paragraphs, batchSize).Error; err != nil {
				return err
			}
		}
		for i := range c.Segments {
			c.Segments[i].ChapterID = c.Chapter.ID
			c.Segments[i].ParagraphID = c.Paragraphs[c.Segments[i].ParagraphID].ID
		}
		if len(c.Segments) > 0 {
			return tx.CreateInBatches(c.Segments, batchSize).Error
		}
		return nil
	})
}

// VolumeStats is a volume with aggregated chapter numbers.
type VolumeStats struct {
	Volume
	ChapterCount int
	WordCount    int64
}

// ListVolumes returns a book's volumes in order with chapter counts.
func (s *Store) ListVolumes(ctx context.Context, bookID int64) ([]VolumeStats, error) {
	var vols []Volume
	db := s.db.WithContext(ctx)
	if err := db.Where("book_id = ?", bookID).Order("idx").Find(&vols).Error; err != nil {
		return nil, err
	}
	var agg []struct {
		VolumeID int64
		Chapters int
		Words    int64
	}
	err := db.Model(&Chapter{}).
		Select("volume_id, COUNT(*) AS chapters, COALESCE(SUM(word_count), 0) AS words").
		Where("book_id = ? AND volume_id IS NOT NULL", bookID).
		Group("volume_id").Scan(&agg).Error
	if err != nil {
		return nil, err
	}
	byID := map[int64]int{}
	for i, a := range agg {
		byID[a.VolumeID] = i
	}
	out := make([]VolumeStats, len(vols))
	for i, v := range vols {
		out[i].Volume = v
		if j, ok := byID[v.ID]; ok {
			out[i].ChapterCount = agg[j].Chapters
			out[i].WordCount = agg[j].Words
		}
	}
	return out, nil
}
