package api

import (
	"context"
	"time"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func toJob(j *store.Job) Job {
	return Job{
		Id:          j.ID,
		Type:        j.Type,
		Status:      JobStatus(j.Status),
		BookId:      j.BookID,
		ChapterId:   j.ChapterID,
		Attempts:    j.Attempts,
		MaxAttempts: j.MaxAttempts,
		Progress:    float32(j.Progress),
		Error:       nonEmpty(j.Error),
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
		StartedAt:   j.StartedAt,
		NotBefore:   notBefore(j),
		FinishedAt:  j.FinishedAt,
	}
}

// ListJobs returns background jobs.
func (s *Server) ListJobs(ctx context.Context, req ListJobsRequestObject) (ListJobsResponseObject, error) {
	offset, limit := page(req.Params.Offset, req.Params.Limit, 50, 500)
	f := store.JobFilter{BookID: req.Params.BookId}
	if req.Params.Status != nil {
		for _, st := range *req.Params.Status {
			f.Statuses = append(f.Statuses, store.JobStatus(st))
		}
	}
	js, total, err := s.Store.ListJobs(ctx, f, offset, limit)
	if err != nil {
		return nil, err
	}
	out := JobPage{Items: make([]Job, 0, len(js)), Total: total}
	for i := range js {
		out.Items = append(out.Items, toJob(&js[i]))
	}
	return ListJobs200JSONResponse(out), nil
}

// notBefore reports the start time of queued jobs that wait (backoff or
// deferred), nil otherwise.
func notBefore(j *store.Job) *time.Time {
	if j.Status != store.JobQueued || !j.NotBefore.After(time.Now()) {
		return nil
	}
	t := j.NotBefore
	return &t
}
