package api

import (
	"context"
	"errors"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func toChapter(c *store.Chapter) Chapter {
	return Chapter{
		Id:         c.ID,
		BookId:     c.BookID,
		VolumeId:   c.VolumeID,
		Idx:        c.Idx,
		Title:      c.Title,
		WordCount:  c.WordCount,
		QuoteCount: c.QuoteCount,
		Excluded:   c.Excluded,
		Status:     ChapterStatus(c.Status),
	}
}

// ListVolumes returns a book's volumes.
func (s *Server) ListVolumes(ctx context.Context, req ListVolumesRequestObject) (ListVolumesResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return ListVolumes404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	vols, err := s.Store.ListVolumes(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	out := make([]Volume, 0, len(vols))
	for _, v := range vols {
		out = append(out, Volume{
			Id: v.ID, Idx: v.Idx, Title: v.Title, ChapterCount: v.ChapterCount, WordCount: v.WordCount,
		})
	}
	return ListVolumes200JSONResponse(out), nil
}

// ListChapters returns a book's chapters.
func (s *Server) ListChapters(ctx context.Context, req ListChaptersRequestObject) (ListChaptersResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return ListChapters404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit, 10000, 10000)
	cs, total, err := s.Store.ListChapters(ctx, req.BookId, store.ChapterFilter{VolumeID: req.Params.VolumeId}, offset, limit)
	if err != nil {
		return nil, err
	}
	out := ChapterPage{Items: make([]Chapter, 0, len(cs)), Total: total}
	for i := range cs {
		out.Items = append(out.Items, toChapter(&cs[i]))
	}
	return ListChapters200JSONResponse(out), nil
}

// GetChapter returns a chapter with its text.
func (s *Server) GetChapter(ctx context.Context, req GetChapterRequestObject) (GetChapterResponseObject, error) {
	c, err := s.Store.GetChapter(ctx, req.ChapterId)
	if isNotFound(err) {
		return GetChapter404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	paras, segs, err := s.Store.ChapterText(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	prev, next, err := s.Store.AdjacentChapters(ctx, c)
	if err != nil {
		return nil, err
	}

	byPara := make(map[int64][]Segment, len(paras))
	for _, sg := range segs {
		byPara[sg.ParagraphID] = append(byPara[sg.ParagraphID], Segment{
			Id: sg.ID, Idx: sg.Idx, Kind: SegmentKind(sg.Kind), QuoteRef: sg.QuoteRef, Text: sg.Text,
		})
	}
	ch := toChapter(c)
	out := ChapterDetail{
		Id: ch.Id, BookId: ch.BookId, VolumeId: ch.VolumeId, Idx: ch.Idx, Title: ch.Title,
		WordCount: ch.WordCount, QuoteCount: ch.QuoteCount, Excluded: ch.Excluded, Status: ch.Status,
		Paragraphs: make([]Paragraph, 0, len(paras)),
	}
	if prev != 0 {
		out.PrevChapterId = &prev
	}
	if next != 0 {
		out.NextChapterId = &next
	}
	for _, p := range paras {
		ss := byPara[p.ID]
		if ss == nil {
			ss = []Segment{}
		}
		out.Paragraphs = append(out.Paragraphs, Paragraph{
			Id: p.ID, Idx: p.Idx, Kind: ParagraphKind(p.Kind), Text: p.Text, Segments: ss,
		})
	}
	return GetChapter200JSONResponse(out), nil
}

// UpdateChapter renames or (un)excludes a chapter.
func (s *Server) UpdateChapter(ctx context.Context, req UpdateChapterRequestObject) (UpdateChapterResponseObject, error) {
	u := store.ChapterUpdate{Excluded: req.Body.Excluded}
	if req.Body.Title != nil {
		t := strings.TrimSpace(*req.Body.Title)
		if t == "" {
			return UpdateChapter400JSONResponse{ErrorJSONResponse(errMsg("title must not be empty"))}, nil
		}
		u.Title = &t
	}
	c, err := s.Store.UpdateChapter(ctx, req.ChapterId, u)
	if isNotFound(err) {
		return UpdateChapter404JSONResponse{Message: "chapter not found"}, nil
	} else if err != nil {
		return nil, err
	}
	return UpdateChapter200JSONResponse(toChapter(c)), nil
}

// MergeChapterWithNext appends the following chapter.
func (s *Server) MergeChapterWithNext(ctx context.Context, req MergeChapterWithNextRequestObject) (MergeChapterWithNextResponseObject, error) {
	c, err := s.Store.MergeChapterWithNext(ctx, req.ChapterId)
	switch {
	case isNotFound(err):
		return MergeChapterWithNext404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	case errors.Is(err, store.ErrInvalid):
		return MergeChapterWithNext409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	return MergeChapterWithNext200JSONResponse(toChapter(c)), nil
}

// SplitChapter starts a new chapter at a paragraph.
func (s *Server) SplitChapter(ctx context.Context, req SplitChapterRequestObject) (SplitChapterResponseObject, error) {
	title := strings.TrimSpace(req.Body.Title)
	if title == "" {
		return SplitChapter400JSONResponse{ErrorJSONResponse(errMsg("title must not be empty"))}, nil
	}
	c, err := s.Store.SplitChapter(ctx, req.ChapterId, req.Body.ParagraphId, title)
	switch {
	case isNotFound(err):
		return SplitChapter404JSONResponse{Message: "chapter not found"}, nil
	case errors.Is(err, store.ErrInvalid):
		return SplitChapter409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	return SplitChapter200JSONResponse(toChapter(c)), nil
}
