package api

import (
	"context"
	"errors"

	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// manualAttributionPriority puts an attribution asked for by hand before
// those the lookahead queues.
const manualAttributionPriority = 200

func (s *Server) toAttribution(ctx context.Context, c *store.Chapter) (ChapterAttribution, error) {
	st, err := s.Store.QuoteStats(ctx, c.ID, attribution.UnsureBelow)
	if err != nil {
		return ChapterAttribution{}, err
	}
	out := ChapterAttribution{
		Status:       AttributionStatus(c.AttributionStatus),
		Error:        nonEmpty(c.AttributionError),
		Prompt:       nonEmpty(c.AttributionPrompt),
		AttributedAt: c.AttributedAt,
		Quotes:       st.Quotes,
		Attributed:   st.Attributed,
		Unsure:       st.Unsure,
		UnsureBelow:  attribution.UnsureBelow,
	}
	if out.Status == "" {
		out.Status = AttributionStatusNone
	}
	// Before an attribution every quote has confidence 0: nothing to check.
	if c.AttributionStatus != store.AttributionDone {
		out.Unsure = 0
	}
	j, err := s.Store.PendingChapterJob(ctx, attribution.JobType, c.ID)
	if err != nil {
		return ChapterAttribution{}, err
	}
	if j != nil {
		job := toJob(j)
		out.Job = &job
	}
	return out, nil
}

// StartAttribution queues the speaker attribution of a chapter.
func (s *Server) StartAttribution(ctx context.Context, req StartAttributionRequestObject) (StartAttributionResponseObject, error) {
	c, b, err := s.chapterBook(ctx, req.ChapterId)
	if isNotFound(err) {
		return StartAttribution404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if c.Excluded {
		return StartAttribution409JSONResponse{Message: "the chapter is not narrated"}, nil
	}
	force := req.Body != nil && req.Body.Force != nil && *req.Body.Force
	switch err := s.Attributor.Start(ctx, b, c, force, manualAttributionPriority); {
	case errors.Is(err, attribution.ErrNotReady):
		return StartAttribution409JSONResponse{Message: "the character analysis must finish first"}, nil
	case errors.Is(err, attribution.ErrBusy):
		return StartAttribution409JSONResponse{Message: "the speakers are being attributed; wait for it to finish"}, nil
	case err != nil:
		return nil, err
	}
	if c, err = s.Store.GetChapter(ctx, c.ID); err != nil {
		return nil, err
	}
	out, err := s.toAttribution(ctx, c)
	if err != nil {
		return nil, err
	}
	return StartAttribution202JSONResponse(out), nil
}

// UpdateQuote sets the speaker or emotion of a quotation by hand.
func (s *Server) UpdateQuote(ctx context.Context, req UpdateQuoteRequestObject) (UpdateQuoteResponseObject, error) {
	c, b, err := s.chapterBook(ctx, req.ChapterId)
	if isNotFound(err) {
		return UpdateQuote404JSONResponse{Message: "chapter not found"}, nil
	} else if err != nil {
		return nil, err
	}
	u := req.Body
	narrator := u.Narrator != nil && *u.Narrator
	if u.SpeakerId != nil && narrator {
		return UpdateQuote400JSONResponse{ErrorJSONResponse(errMsg("give either speakerId or narrator"))}, nil
	}
	if u.Emotion != nil && !u.Emotion.Valid() {
		return UpdateQuote400JSONResponse{ErrorJSONResponse(errMsg("unknown emotion"))}, nil
	}
	_, segs, err := s.Store.ChapterText(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	var current *store.Segment
	for i := range segs {
		if segs[i].QuoteRef == req.QuoteRef {
			current = &segs[i]
			break
		}
	}
	if current == nil {
		return UpdateQuote404JSONResponse{Message: "quotation not found"}, nil
	}
	speaker, emotion := current.SpeakerCharacterID, current.Emotion
	switch {
	case narrator:
		speaker = nil
	case u.SpeakerId != nil:
		ch, err := s.Store.GetCharacter(ctx, *u.SpeakerId)
		if isNotFound(err) || (err == nil && ch.BookID != c.BookID) {
			return UpdateQuote400JSONResponse{ErrorJSONResponse(errMsg("no such character in this book"))}, nil
		} else if err != nil {
			return nil, err
		}
		speaker = &ch.ID
	}
	if u.Emotion != nil {
		emotion = string(*u.Emotion)
	}
	if err := s.Store.SetQuoteSpeaker(ctx, c.ID, req.QuoteRef, speaker, emotion); err != nil {
		return nil, err
	}
	// A speaker chosen for a chapter means the character appears in it.
	if speaker != nil {
		if err := s.Store.LinkCharacterChapters(ctx, c.BookID, *speaker, c.ID); err != nil {
			return nil, err
		}
		if err := s.Store.RefreshCharacterStats(ctx, *speaker); err != nil {
			return nil, err
		}
	}
	if c, err = s.Store.GetChapter(ctx, c.ID); err != nil {
		return nil, err
	}
	out, err := s.chapterDetail(ctx, b, c)
	if err != nil {
		return nil, err
	}
	return UpdateQuote200JSONResponse(out), nil
}
