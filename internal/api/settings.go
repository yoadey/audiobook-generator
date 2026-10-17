package api

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// maxPreviewText is the longest sample text of a voice preview.
const maxPreviewText = 500

// previewText cleans a sample text from a request; msg says why it cannot
// be used.
func previewText(v string) (text, msg string) {
	text = strings.TrimSpace(store.CleanText(v))
	if utf8.RuneCountInString(text) > maxPreviewText {
		return "", "previewText must be at most 500 characters"
	}
	return text, ""
}

// samplePreviewText is what a preview says when the request names no
// text: the book's sample text, else the instance's, else the built-in one.
func (s *Server) samplePreviewText(ctx context.Context, bookID *int64) (string, error) {
	if bookID != nil {
		b, err := s.Store.GetBook(ctx, *bookID)
		if err != nil && !isNotFound(err) {
			return "", err
		}
		if err == nil && b.PreviewText != "" {
			return b.PreviewText, nil
		}
	}
	text, err := s.Store.GetSetting(ctx, store.SettingPreviewText)
	if err != nil || text != "" {
		return text, err
	}
	return defaultPreviewText, nil
}

func (s *Server) settings(ctx context.Context) (AppSettings, error) {
	text, err := s.Store.GetSetting(ctx, store.SettingPreviewText)
	if err != nil {
		return AppSettings{}, err
	}
	unload, err := s.Store.UnloadIdleModels(ctx)
	return AppSettings{
		PreviewText: text, DefaultPreviewText: defaultPreviewText,
		UnloadIdleModels: unload, UnloadIdleSeconds: int(s.UnloadIdle / time.Second),
	}, err
}

// GetSettings returns the settings of this instance.
func (s *Server) GetSettings(ctx context.Context, _ GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	out, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return GetSettings200JSONResponse(out), nil
}

// UpdateSettings changes settings of this instance.
func (s *Server) UpdateSettings(ctx context.Context, req UpdateSettingsRequestObject) (UpdateSettingsResponseObject, error) {
	if v := req.Body.PreviewText; v != nil {
		text, msg := previewText(*v)
		if msg != "" {
			return UpdateSettings400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
		}
		if err := s.Store.SetSetting(ctx, store.SettingPreviewText, text); err != nil {
			return nil, err
		}
	}
	if v := req.Body.UnloadIdleModels; v != nil {
		if err := s.Store.SetUnloadIdleModels(ctx, *v); err != nil {
			return nil, err
		}
	}
	out, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return UpdateSettings200JSONResponse(out), nil
}
