package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// settingEngineRequests prefixes the request options of a speech engine
// (by name), as set in the UI.
const settingEngineRequests = "tts_requests_"

// EngineRequests are how long a speech engine's requests get, as set in
// the UI.
type EngineRequests struct {
	MaxChars       int  `json:"maxChars"`
	JoinParagraphs bool `json:"joinParagraphs"`
}

// EngineRequests returns the request options set for an engine (nil:
// none, its configuration applies).
func (s *Store) EngineRequests(ctx context.Context, engine string) (*EngineRequests, error) {
	v, err := s.GetSetting(ctx, settingEngineRequests+engine)
	if err != nil || v == "" {
		return nil, err
	}
	var o EngineRequests
	if err := json.Unmarshal([]byte(v), &o); err != nil {
		return nil, fmt.Errorf("setting %s%s: %w", settingEngineRequests, engine, err)
	}
	return &o, nil
}

// SetEngineRequests stores the request options of an engine (nil: back
// to its configuration).
func (s *Store) SetEngineRequests(ctx context.Context, engine string, o *EngineRequests) error {
	v := ""
	if o != nil {
		b, err := json.Marshal(o)
		if err != nil {
			return err
		}
		v = string(b)
	}
	return s.SetSetting(ctx, settingEngineRequests+engine, v)
}

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
