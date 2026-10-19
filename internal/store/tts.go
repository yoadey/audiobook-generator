package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetTTSUsage returns the usage of a speech engine on a day (zero when
// none).
func (s *Store) GetTTSUsage(ctx context.Context, engine, day string) (TTSUsage, error) {
	u := TTSUsage{Engine: engine, Day: day}
	err := s.db.WithContext(ctx).Where("engine = ? AND day = ?", engine, day).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return u, nil
	}
	return u, err
}

// AddTTSUsage adds the counters of d to the engine's row of d.Day.
func (s *Store) AddTTSUsage(ctx context.Context, d TTSUsage) error {
	d.ID = 0
	d.UpdatedAt = time.Now().UTC()
	add := func(col string) clause.Assignment {
		return clause.Assignment{
			Column: clause.Column{Name: col},
			Value:  gorm.Expr("tts_usages." + col + " + excluded." + col),
		}
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "engine"}, {Name: "day"}},
		DoUpdates: clause.Set{
			add("requests"), add("input_tokens"), add("output_tokens"), add("errors"), add("rate_limited"),
			{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("excluded.updated_at")},
		},
	}).Create(&d).Error
}
