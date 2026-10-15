package api

import (
	"context"
	"errors"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/store"
)

const analysisBusyMsg = "the character analysis of this book is running; pause it first"

// analysisBusy reports whether the analysis of a chapter's book is active
// (chapters and characters must not change under it).
func (s *Server) analysisBusy(ctx context.Context, chapterID int64) (bool, error) {
	_, b, err := s.chapterBook(ctx, chapterID)
	if isNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return analysis.Active(b), nil
}

// addAnalysisProgress fills in the share of analysed batches.
func (s *Server) addAnalysisProgress(ctx context.Context, b *Book) error {
	if b.AnalysisStatus == AnalysisStatusNone {
		return nil
	}
	st, err := s.Store.AnalysisStats(ctx, b.Id)
	if err != nil {
		return err
	}
	if st.Total > 0 {
		p := float32(st.Done) / float32(st.Total)
		b.AnalysisProgress = &p
	}
	return nil
}

func (s *Server) toAnalysis(ctx context.Context, b *store.Book) (Analysis, error) {
	st, err := s.Store.AnalysisStats(ctx, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	n, err := s.Store.CountCharacters(ctx, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	out := Analysis{
		Status:          AnalysisStatus(b.AnalysisStatus),
		Error:           nonEmpty(b.AnalysisError),
		StartedAt:       b.AnalysisStartedAt,
		FinishedAt:      b.AnalysisFinishedAt,
		BatchesTotal:    st.Total,
		BatchesDone:     st.Done,
		EstimatedTokens: st.EstimatedTokens + int64(st.Total*analysis.OverheadTokens),
		InputTokens:     st.InputTokens,
		OutputTokens:    st.OutputTokens,
		CharacterCount:  n,
		BatchTokens:     s.Analyzer.BatchTokens,
		Prompt:          analysis.PromptID(),
	}
	if out.Status == "" {
		out.Status = AnalysisStatusNone
	}
	if st.Total == 0 {
		est, err := s.Analyzer.EstimateBook(ctx, b.ID)
		if err != nil {
			return Analysis{}, err
		}
		out.BatchesTotal, out.EstimatedTokens = est.Batches, est.Tokens
	}
	j, err := s.Store.PendingBookJob(ctx, analysis.JobType, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	if j != nil {
		job := toJob(j)
		out.Job = &job
	}
	return out, nil
}

// GetAnalysis reports the character analysis of a book.
func (s *Server) GetAnalysis(ctx context.Context, req GetAnalysisRequestObject) (GetAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return GetAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return GetAnalysis200JSONResponse(out), nil
}

// StartAnalysis queues, resumes or restarts the analysis.
func (s *Server) StartAnalysis(ctx context.Context, req StartAnalysisRequestObject) (StartAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return StartAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	restart := req.Body != nil && req.Body.Restart != nil && *req.Body.Restart
	switch err := s.Analyzer.Start(ctx, b, restart); {
	case errors.Is(err, analysis.ErrBusy):
		return StartAnalysis409JSONResponse{Message: analysisBusyMsg}, nil
	case errors.Is(err, store.ErrInvalid):
		return StartAnalysis409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	if b, err = s.Store.GetBook(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return StartAnalysis202JSONResponse(out), nil
}

// PauseAnalysis stops the analysis after the current batch.
func (s *Server) PauseAnalysis(ctx context.Context, req PauseAnalysisRequestObject) (PauseAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return PauseAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Analyzer.Pause(ctx, b); err != nil {
		return nil, err
	}
	if b, err = s.Store.GetBook(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return PauseAnalysis200JSONResponse(out), nil
}

func toCharacter(c *store.Character, aliases []store.CharacterAlias) Character {
	as := make([]string, 0, len(aliases))
	for _, a := range aliases {
		as = append(as, a.Alias)
	}
	return Character{
		Id: c.ID, BookId: c.BookID, Name: c.Name, Aliases: as,
		Gender: Gender(c.Gender), AgeGroup: AgeGroup(c.AgeGroup), Role: c.Role,
		Description: c.Description, SpeechStyle: c.SpeechStyle, Importance: c.Importance,
		Speaks: c.Speaks, ChapterCount: c.ChapterCount, FirstChapterIdx: c.FirstChapterIdx,
		UserEdited: c.UserEdited,
	}
}

func (s *Server) characterDetail(ctx context.Context, id int64) (CharacterDetail, error) {
	c, err := s.Store.GetCharacter(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	as, err := s.Store.CharacterAliases(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	chs, err := s.Store.CharacterChapters(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	base := toCharacter(c, as[id])
	out := CharacterDetail{
		Id: base.Id, BookId: base.BookId, Name: base.Name, Aliases: base.Aliases, Gender: base.Gender,
		AgeGroup: base.AgeGroup, Role: base.Role, Description: base.Description, SpeechStyle: base.SpeechStyle,
		Importance: base.Importance, Speaks: base.Speaks, ChapterCount: base.ChapterCount,
		FirstChapterIdx: base.FirstChapterIdx, UserEdited: base.UserEdited,
		Chapters: make([]ChapterRef, 0, len(chs)),
	}
	for _, ch := range chs {
		out.Chapters = append(out.Chapters, ChapterRef{Id: ch.ID, Idx: ch.Idx, Title: ch.Title})
	}
	return out, nil
}

// ListCharacters returns a page of a book's characters.
func (s *Server) ListCharacters(ctx context.Context, req ListCharactersRequestObject) (ListCharactersResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return ListCharacters404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit, 50, 500)
	f := store.CharacterFilter{}
	if req.Params.Search != nil {
		f.Search = strings.TrimSpace(*req.Params.Search)
	}
	if req.Params.Sort != nil {
		f.Sort = string(*req.Params.Sort)
	}
	cs, total, err := s.Store.ListCharacters(ctx, req.BookId, f, offset, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(cs))
	for i := range cs {
		ids[i] = cs[i].ID
	}
	as, err := s.Store.CharacterAliases(ctx, ids...)
	if err != nil {
		return nil, err
	}
	out := CharacterPage{Items: make([]Character, 0, len(cs)), Total: total}
	for i := range cs {
		out.Items = append(out.Items, toCharacter(&cs[i], as[cs[i].ID]))
	}
	return ListCharacters200JSONResponse(out), nil
}

// GetCharacter returns a character with aliases and chapters.
func (s *Server) GetCharacter(ctx context.Context, req GetCharacterRequestObject) (GetCharacterResponseObject, error) {
	out, err := s.characterDetail(ctx, req.CharacterId)
	if isNotFound(err) {
		return GetCharacter404JSONResponse{ErrorJSONResponse(errMsg("character not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	return GetCharacter200JSONResponse(out), nil
}

// characterBook loads a character and tells whether its book's analysis
// is active.
func (s *Server) characterBook(ctx context.Context, id int64) (*store.Character, bool, error) {
	c, err := s.Store.GetCharacter(ctx, id)
	if err != nil {
		return nil, false, err
	}
	b, err := s.Store.GetBook(ctx, c.BookID)
	if err != nil {
		return nil, false, err
	}
	return c, analysis.Active(b), nil
}

// UpdateCharacter corrects a character's profile or aliases.
func (s *Server) UpdateCharacter(ctx context.Context, req UpdateCharacterRequestObject) (UpdateCharacterResponseObject, error) {
	c, _, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return UpdateCharacter404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	u := req.Body
	fields := map[string]any{"UserEdited": true}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if store.NormName(name) == "" {
			return UpdateCharacter400JSONResponse{ErrorJSONResponse(errMsg("name must contain letters or digits"))}, nil
		}
		fields["Name"], c.Name = name, name
	}
	if u.Gender != nil {
		fields["Gender"] = store.Gender(*u.Gender)
	}
	if u.AgeGroup != nil {
		fields["AgeGroup"] = store.AgeGroup(*u.AgeGroup)
	}
	if u.Role != nil {
		fields["Role"] = strings.TrimSpace(*u.Role)
	}
	if u.Description != nil {
		fields["Description"] = strings.TrimSpace(*u.Description)
	}
	if u.SpeechStyle != nil {
		fields["SpeechStyle"] = strings.TrimSpace(*u.SpeechStyle)
	}
	if u.Importance != nil {
		fields["Importance"] = *u.Importance
	}
	err = s.Store.Tx(ctx, func(tx *store.Store) error {
		if err := tx.UpdateCharacter(ctx, c.ID, fields); err != nil {
			return err
		}
		if u.Aliases != nil {
			return tx.SetCharacterAliases(ctx, c, *u.Aliases)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := s.characterDetail(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return UpdateCharacter200JSONResponse(out), nil
}

// DeleteCharacter removes a character.
func (s *Server) DeleteCharacter(ctx context.Context, req DeleteCharacterRequestObject) (DeleteCharacterResponseObject, error) {
	c, busy, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return DeleteCharacter404JSONResponse{ErrorJSONResponse(errMsg("character not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if busy {
		return DeleteCharacter409JSONResponse{Message: analysisBusyMsg}, nil
	}
	if err := s.Store.DeleteCharacter(ctx, c.ID); err != nil {
		return nil, err
	}
	return DeleteCharacter204Response{}, nil
}

// MergeCharacters folds characters into one.
func (s *Server) MergeCharacters(ctx context.Context, req MergeCharactersRequestObject) (MergeCharactersResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return MergeCharacters404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	if analysis.Active(b) {
		return MergeCharacters409JSONResponse{Message: analysisBusyMsg}, nil
	}
	target, err := s.Store.GetCharacter(ctx, req.Body.TargetId)
	if isNotFound(err) || (err == nil && target.BookID != b.ID) {
		return MergeCharacters404JSONResponse{Message: "character not found in this book"}, nil
	} else if err != nil {
		return nil, err
	}
	_, err = s.Store.MergeCharacters(ctx, target.ID, req.Body.SourceIds...)
	if errors.Is(err, store.ErrInvalid) {
		return MergeCharacters400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.UpdateCharacter(ctx, target.ID, map[string]any{"UserEdited": true}); err != nil {
		return nil, err
	}
	out, err := s.characterDetail(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	return MergeCharacters200JSONResponse(out), nil
}

// SplitCharacter moves aliases to a new character.
func (s *Server) SplitCharacter(ctx context.Context, req SplitCharacterRequestObject) (SplitCharacterResponseObject, error) {
	c, busy, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return SplitCharacter404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	if busy {
		return SplitCharacter409JSONResponse{Message: analysisBusyMsg}, nil
	}
	var aliases []string
	if req.Body.Aliases != nil {
		aliases = *req.Body.Aliases
	}
	created, err := s.Store.SplitCharacter(ctx, c.ID, req.Body.Name, aliases)
	if errors.Is(err, store.ErrInvalid) {
		return SplitCharacter400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.characterDetail(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	return SplitCharacter201JSONResponse(out), nil
}
