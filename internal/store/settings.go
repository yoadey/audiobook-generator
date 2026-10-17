package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keys of AppSetting.
const (
	// SettingPreviewText is the sample text of voice previews.
	SettingPreviewText = "preview_text"
	// SettingUnloadIdleModels is "off" to keep speech models loaded while
	// idle (unset: unload them).
	SettingUnloadIdleModels = "unload_idle_models"
)

// UnloadIdleModels reports whether idle speech models are unloaded.
func (s *Store) UnloadIdleModels(ctx context.Context) (bool, error) {
	v, err := s.GetSetting(ctx, SettingUnloadIdleModels)
	return v != "off", err
}

// SetUnloadIdleModels stores whether idle speech models are unloaded.
func (s *Store) SetUnloadIdleModels(ctx context.Context, on bool) error {
	v := "on"
	if !on {
		v = "off"
	}
	return s.SetSetting(ctx, SettingUnloadIdleModels, v)
}

// GetSetting returns a setting ("" when unset).
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var st AppSetting
	err := s.db.WithContext(ctx).Where("name = ?", key).Take(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return st.Value, err
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&AppSetting{Name: key, Value: CleanText(value)}).Error
}
