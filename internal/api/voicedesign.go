package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
)

func toVoiceDesign(c *store.Character) VoiceDesign {
	return VoiceDesign{
		Status:               VoiceDesignStatus(cmp.Or(string(c.DesignStatus), "none")),
		Description:          c.DesignDescription,
		SuggestedDescription: voicedesign.DefaultDescription(c),
		Error:                nonEmpty(c.DesignError),
	}
}

// designBlocked tells why a book cannot design voices ("" when it can):
// designed voices are clips of one engine, and a book speaks with one.
func (s *Server) designBlocked(b *store.Book) string {
	voice, _ := render.NarratorVoice(b, s.DefaultVoice)
	if e := tts.EngineOf(voice); e != s.Designer.Engine {
		return fmt.Sprintf("this book speaks with %s: switch it to %s to design voices", e, s.Designer.Engine)
	}
	return ""
}

// DesignVoice queues the design of a character's voice.
func (s *Server) DesignVoice(ctx context.Context, req DesignVoiceRequestObject) (DesignVoiceResponseObject, error) {
	if s.Designer == nil {
		return DesignVoice409JSONResponse{Message: "voice design is not configured"}, nil
	}
	c, err := s.Store.GetCharacter(ctx, req.CharacterId)
	if isNotFound(err) {
		return DesignVoice404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	b, err := s.Store.GetBook(ctx, c.BookID)
	if err != nil {
		return nil, err
	}
	if msg := s.designBlocked(b); msg != "" {
		return DesignVoice409JSONResponse{Message: msg}, nil
	}
	desc, msg := voicedesign.CleanDescription(req.Body.Description)
	if msg != "" {
		return DesignVoice400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	switch err := s.Designer.Start(ctx, c, desc); {
	case errors.Is(err, voicedesign.ErrBusy):
		return DesignVoice409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	if c, err = s.Store.GetCharacter(ctx, c.ID); err != nil {
		return nil, err
	}
	return DesignVoice202JSONResponse(toVoiceDesign(c)), nil
}

// DesignBookVoices queues designs for the main characters of a book
// whose voice was not chosen by hand (designed ones count as chosen).
func (s *Server) DesignBookVoices(ctx context.Context, req DesignBookVoicesRequestObject) (DesignBookVoicesResponseObject, error) {
	if s.Designer == nil {
		return DesignBookVoices409JSONResponse{Message: "voice design is not configured"}, nil
	}
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return DesignBookVoices404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if msg := s.designBlocked(b); msg != "" {
		return DesignBookVoices409JSONResponse{Message: msg}, nil
	}
	chars, err := s.Store.BookCharacters(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	n := 0
	for i := range chars {
		c := &chars[i]
		if c.Tier != store.TierMain || c.VoiceManual || c.DesignStatus == store.DesignQueued ||
			c.DesignStatus == store.DesignRunning {
			continue
		}
		switch err := s.Designer.Start(ctx, c, voicedesign.DefaultDescription(c)); {
		case errors.Is(err, voicedesign.ErrBusy):
		case err != nil:
			return nil, err
		default:
			n++
		}
	}
	return DesignBookVoices202JSONResponse(VoiceDesignBatch{Queued: n}), nil
}
