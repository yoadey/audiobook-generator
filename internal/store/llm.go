package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrConflict is returned when a unique value (e.g. a name) is taken.
var ErrConflict = errors.New("conflict")

func conflict(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrConflict
	}
	return err
}

// ListLLMProviders returns all providers in the order they are tried.
func (s *Store) ListLLMProviders(ctx context.Context) ([]LLMProvider, error) {
	var ps []LLMProvider
	err := s.db.WithContext(ctx).Order("position, id").Find(&ps).Error
	return ps, err
}

// GetLLMProvider loads one provider.
func (s *Store) GetLLMProvider(ctx context.Context, id int64) (*LLMProvider, error) {
	var p LLMProvider
	if err := s.db.WithContext(ctx).Take(&p, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// CreateLLMProvider inserts a provider; ErrConflict when the name is taken.
func (s *Store) CreateLLMProvider(ctx context.Context, p *LLMProvider) error {
	return conflict(s.db.WithContext(ctx).Create(p).Error)
}

// UpdateLLMProvider stores every field of an existing provider.
func (s *Store) UpdateLLMProvider(ctx context.Context, p *LLMProvider) error {
	res := s.db.WithContext(ctx).Model(&LLMProvider{ID: p.ID}).Select("*").Omit("id", "created_at").Updates(p)
	if res.Error != nil {
		return conflict(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteLLMProvider removes a provider and its usage history.
func (s *Store) DeleteLLMProvider(ctx context.Context, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("provider_id = ?", id).Delete(&LLMUsage{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&LLMProvider{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SyncConfigLLMProviders makes the providers defined in the configuration
// (Helm values) match ps: they are created or updated by name (taking over
// a UI provider of the same name, including its usage history) and
// configuration providers missing from ps are deleted.
func (s *Store) SyncConfigLLMProviders(ctx context.Context, ps []LLMProvider) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		names := make([]string, 0, len(ps))
		for i := range ps {
			p := ps[i]
			p.Source = ProviderSourceConfig
			names = append(names, p.Name)
			var existing LLMProvider
			err := tx.Where("name = ?", p.Name).Take(&existing).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				if err := tx.Create(&p).Error; err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				p.ID = existing.ID
				if err := tx.Model(&LLMProvider{ID: p.ID}).Select("*").Omit("id", "created_at").
					Updates(&p).Error; err != nil {
					return err
				}
			}
		}
		stale := tx.Where("source = ?", ProviderSourceConfig)
		if len(names) > 0 {
			stale = stale.Where("name NOT IN ?", names)
		}
		var ids []int64
		if err := stale.Model(&LLMProvider{}).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("provider_id IN ?", ids).Delete(&LLMUsage{}).Error; err != nil {
			return err
		}
		return tx.Delete(&LLMProvider{}, ids).Error
	})
}

// GetLLMUsage returns the usage of a provider on a day (zero when none).
func (s *Store) GetLLMUsage(ctx context.Context, providerID int64, day string) (LLMUsage, error) {
	u := LLMUsage{ProviderID: providerID, Day: day}
	err := s.db.WithContext(ctx).Where("provider_id = ? AND day = ?", providerID, day).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return u, nil
	}
	return u, err
}

// AddLLMUsage adds the counters of d to the provider's row of d.Day.
func (s *Store) AddLLMUsage(ctx context.Context, d LLMUsage) error {
	d.ID = 0
	d.UpdatedAt = time.Now().UTC()
	add := func(col string) clause.Assignment {
		return clause.Assignment{
			Column: clause.Column{Name: col},
			Value:  gorm.Expr("llm_usages." + col + " + excluded." + col),
		}
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider_id"}, {Name: "day"}},
		DoUpdates: clause.Set{
			add("requests"), add("input_tokens"), add("output_tokens"), add("errors"), add("rate_limited"),
			{Column: clause.Column{Name: "updated_at"}, Value: gorm.Expr("excluded.updated_at")},
		},
	}).Create(&d).Error
}

// ListLLMUsage returns the usage rows of all providers since day (inclusive,
// YYYY-MM-DD), oldest first.
func (s *Store) ListLLMUsage(ctx context.Context, since string) ([]LLMUsage, error) {
	var us []LLMUsage
	err := s.db.WithContext(ctx).Where("day >= ?", since).Order("day, provider_id").Find(&us).Error
	return us, err
}
