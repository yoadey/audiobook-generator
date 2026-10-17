package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// maxCastingWarnings bounds the warnings a casting reports.
const maxCastingWarnings = 50

// voiceOf resolves characters' effective voices for one book.
type voiceOf func(c *store.Character) casting.Voice

// bookVoices returns the voice resolver of a book: the voices of its
// narrator's engine as the server offers them (Kokoro's catalogue is
// assumed complete while the server is down).
func (s *Server) bookVoices(ctx context.Context, b *store.Book) voiceOf {
	narrator, speed := casting.Narrator(b, s.DefaultVoice)
	vs, _ := s.Voices.VoicesFor(ctx, narrator)
	return func(c *store.Character) casting.Voice { return casting.Effective(c, vs, narrator, speed) }
}

func (s *Server) characterVoices(ctx context.Context, bookID int64) (voiceOf, error) {
	b, err := s.Store.GetBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	return s.bookVoices(ctx, b), nil
}

// castingWarnings lists main characters that sound like the narrator or
// like each other.
func castingWarnings(chars []store.Character, narrator string, narratorSpeed float64) []CastingWarning {
	out := []CastingWarning{}
	nr, err := casting.ParseRecipe(narrator)
	type voiced struct {
		c *store.Character
		r casting.Recipe
	}
	var vs []voiced
	for i := range chars {
		c := &chars[i]
		if c.VoiceRecipe == "" {
			continue
		}
		r, perr := casting.ParseRecipe(c.VoiceRecipe)
		if perr != nil {
			continue
		}
		if err == nil {
			if d := casting.Distance(r, c.VoiceSpeed, nr, narratorSpeed); d < casting.NarratorDistance {
				out = append(out, CastingWarning{Kind: CastingWarningKindNarrator, CharacterId: c.ID,
					CharacterName: c.Name, Distance: float32(d)})
			}
		}
		for _, o := range vs {
			if d := casting.Distance(r, c.VoiceSpeed, o.r, o.c.VoiceSpeed); d < casting.MinDistance {
				out = append(out, CastingWarning{Kind: CastingWarningKindSimilar, CharacterId: c.ID,
					CharacterName: c.Name, OtherId: &o.c.ID, OtherName: &o.c.Name, Distance: float32(d)})
			}
		}
		vs = append(vs, voiced{c, r})
		if len(out) >= maxCastingWarnings {
			return out[:maxCastingWarnings]
		}
	}
	return out
}

func (s *Server) toCasting(ctx context.Context, b *store.Book) (Casting, error) {
	counts, err := s.Store.CastingCounts(ctx, b.ID)
	if err != nil {
		return Casting{}, err
	}
	chars, err := s.Store.VoicedCharacters(ctx, b.ID)
	if err != nil {
		return Casting{}, err
	}
	narrator, speed := casting.Narrator(b, s.DefaultVoice)
	out := Casting{
		Status:        CastingStatus(b.CastingStatus),
		Error:         nonEmpty(b.CastingError),
		FinishedAt:    b.CastingFinishedAt,
		MainCount:     counts.Main,
		MinorCount:    counts.Minor,
		VoicedCount:   counts.Voiced,
		MainLimit:     s.Caster.MainCharacters,
		NarratorVoice: narrator,
		NarratorSpeed: float32(speed),
		Prompt:        casting.PromptID(),
		Warnings:      castingWarnings(chars, narrator, speed),
	}
	if out.Status == "" {
		out.Status = CastingStatusNone
	}
	if out.MainLimit <= 0 {
		out.MainLimit = casting.DefaultMainCharacters
	}
	j, err := s.Store.PendingBookJob(ctx, casting.JobType, b.ID)
	if err != nil {
		return Casting{}, err
	}
	if j != nil {
		job := toJob(j)
		out.Job = &job
	} else if casting.Active(b) && time.Since(b.UpdatedAt) > time.Minute {
		// The job is gone (canceled, or lost): offer a new start. (Not
		// right after a start, which sets the status before it queues
		// the job.)
		const msg = "the casting job stopped; start the casting again"
		ok, err := s.Store.FailStaleCasting(ctx, b.ID, b.CastingStatus, b.UpdatedAt, msg)
		if err != nil {
			return Casting{}, err
		}
		if ok {
			out.Status, out.Error = CastingStatusFailed, nonEmpty(msg)
		}
	}
	return out, nil
}

// GetCasting reports the voice casting of a book.
func (s *Server) GetCasting(ctx context.Context, req GetCastingRequestObject) (GetCastingResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return GetCasting404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.toCasting(ctx, b)
	if err != nil {
		return nil, err
	}
	return GetCasting200JSONResponse(out), nil
}

// StartCasting queues the casting.
func (s *Server) StartCasting(ctx context.Context, req StartCastingRequestObject) (StartCastingResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return StartCasting404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	restart := req.Body != nil && req.Body.Restart != nil && *req.Body.Restart
	switch err := s.Caster.Start(ctx, b, restart); {
	case errors.Is(err, casting.ErrNotReady):
		return StartCasting409JSONResponse{Message: "the character analysis must finish first"}, nil
	case errors.Is(err, casting.ErrBusy):
		return StartCasting409JSONResponse{Message: "the casting is running; wait for it to finish"}, nil
	case err != nil:
		return nil, err
	}
	if b, err = s.Store.GetBook(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := s.toCasting(ctx, b)
	if err != nil {
		return nil, err
	}
	return StartCasting202JSONResponse(out), nil
}

// ListCharacterQuotes returns quotes of a character for previews.
func (s *Server) ListCharacterQuotes(ctx context.Context, req ListCharacterQuotesRequestObject) (ListCharacterQuotesResponseObject, error) {
	c, err := s.Store.GetCharacter(ctx, req.CharacterId)
	if isNotFound(err) {
		return ListCharacterQuotes404JSONResponse{ErrorJSONResponse(errMsg("character not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	limit := 10
	if req.Params.Limit != nil {
		limit = min(max(*req.Params.Limit, 1), 50)
	}
	as, err := s.Store.CharacterAliases(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(as[c.ID]))
	for _, a := range as[c.ID] {
		names = append(names, a.Alias)
	}
	qs, err := casting.Quotes(ctx, s.Store, c, names, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Quote, 0, len(qs))
	for _, q := range qs {
		out = append(out, Quote{Text: q.Text, ChapterId: q.Chapter.ID, ChapterIdx: q.Chapter.Idx,
			ChapterTitle: q.Chapter.Title})
	}
	return ListCharacterQuotes200JSONResponse(out), nil
}

// previewKey identifies a cached preview.
func previewKey(voice string, speed float64, text string) string {
	return voice + "|" + strconv.FormatFloat(speed, 'f', 2, 64) + "|" + text
}
