package api

import (
	"context"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// GetChapterTimings returns when each segment is spoken in a render of the
// chapter: recorded while rendering, or estimated for older renders.
func (s *Server) GetChapterTimings(ctx context.Context, req GetChapterTimingsRequestObject) (GetChapterTimingsResponseObject, error) {
	c, err := s.Store.GetChapter(ctx, req.ChapterId)
	if isNotFound(err) || (err == nil && (c.AudioPath == "" || c.AudioStatus == store.AudioNone)) {
		return GetChapterTimings404JSONResponse{ErrorJSONResponse(errMsg("no audio"))}, nil
	} else if err != nil {
		return nil, err
	}
	t, path, exact, err := render.LoadTimings(ctx, s.Store, s.DataDir, c, req.Params.V)
	if err != nil {
		return nil, err
	}
	out := ChapterTimings{ChapterId: c.ID, Version: render.AudioVersion(path), Exact: exact,
		Cues: make([]Cue, 0, len(t.Cues))}
	for _, q := range t.Cues {
		c := Cue{SegmentId: q.SegmentID, StartMs: q.StartMs, EndMs: q.EndMs}
		if len(q.Words) > 0 {
			words := make([][]int64, len(q.Words))
			for i, w := range q.Words {
				words[i] = []int64{w[0], w[1]}
			}
			c.Words = &words
		}
		out.Cues = append(out.Cues, c)
	}
	return GetChapterTimings200JSONResponse(out), nil
}
