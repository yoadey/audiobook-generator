package api

import (
	"context"
	"errors"

	"github.com/yoadey/audiobook-generator/internal/prepare"
	"github.com/yoadey/audiobook-generator/internal/store"
)

func (s *Server) toPreparation(ctx context.Context, c *store.Chapter) (ChapterPreparation, error) {
	st, err := s.Store.PrepStats(ctx, c.ID)
	if err != nil {
		return ChapterPreparation{}, err
	}
	out := ChapterPreparation{
		Status:      PrepStatus(c.PrepStatus),
		Error:       nonEmpty(c.PrepError),
		Prompt:      nonEmpty(c.PrepPrompt),
		PreparedAt:  c.PreparedAt,
		Moods:       st.Moods,
		Pauses:      st.Pauses,
		SpokenForms: st.SpokenAs,
	}
	if out.Status == "" {
		out.Status = PrepStatusNone
	}
	j, err := s.Store.PendingChapterJob(ctx, prepare.JobType, c.ID)
	if err != nil {
		return ChapterPreparation{}, err
	}
	if j != nil {
		job := toJob(j)
		out.Job = &job
	}
	return out, nil
}

// StartPreparation queues the preparation of a chapter for reading aloud.
func (s *Server) StartPreparation(ctx context.Context, req StartPreparationRequestObject) (StartPreparationResponseObject, error) {
	c, b, err := s.chapterBook(ctx, req.ChapterId)
	if isNotFound(err) {
		return StartPreparation404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if c.Excluded {
		return StartPreparation409JSONResponse{Message: "the chapter is not narrated"}, nil
	}
	force := req.Body != nil && req.Body.Force != nil && *req.Body.Force
	switch err := s.Preparer.Start(ctx, b, c, force, manualAttributionPriority); {
	case errors.Is(err, prepare.ErrBusy):
		return StartPreparation409JSONResponse{Message: "the chapter is being prepared; wait for it to finish"}, nil
	case err != nil:
		return nil, err
	}
	if c, err = s.Store.GetChapter(ctx, c.ID); err != nil {
		return nil, err
	}
	out, err := s.toPreparation(ctx, c)
	if err != nil {
		return nil, err
	}
	return StartPreparation202JSONResponse(out), nil
}
