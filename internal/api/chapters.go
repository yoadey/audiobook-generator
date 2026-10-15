package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// toChapter converts a chapter; b (the chapter's book) decides whether the
// audio is stale.
func (s *Server) toChapter(b *store.Book, c *store.Chapter) Chapter {
	out := Chapter{
		Id:         c.ID,
		BookId:     c.BookID,
		VolumeId:   c.VolumeID,
		Idx:        c.Idx,
		Title:      c.Title,
		WordCount:  c.WordCount,
		QuoteCount: c.QuoteCount,
		Excluded:   c.Excluded,
		Status:     ChapterStatus(c.Status),

		AudioStatus: AudioStatus(c.AudioStatus),
		AudioError:  nonEmpty(c.AudioError),
		AudioStale:  b != nil && s.Scheduler != nil && s.Scheduler.Stale(b, c),

		PovCharacterId: c.POVCharacterID,
	}
	if c.Narration != "" {
		n := Narration(c.Narration)
		out.Narration = &n
	}
	if c.AudioStatus == store.AudioReady {
		out.AudioDurationMs = &c.AudioDurationMs
	}
	if out.AudioStatus == "" {
		out.AudioStatus = AudioStatusNone
	}
	return out
}

// chapterBook loads a chapter and its book.
func (s *Server) chapterBook(ctx context.Context, chapterID int64) (*store.Chapter, *store.Book, error) {
	c, err := s.Store.GetChapter(ctx, chapterID)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Store.GetBook(ctx, c.BookID)
	if err != nil {
		return nil, nil, err
	}
	return c, b, nil
}

// dropAudio forgets and deletes the audio of chapters whose text changed.
func (s *Server) dropAudio(ctx context.Context, chapterIDs ...int64) {
	s.removeRenderCache(ctx, chapterIDs...)
	paths, err := s.Store.ClearChapterAudio(ctx, chapterIDs...)
	if err != nil {
		s.Log.WarnContext(ctx, "clear chapter audio", "err", err)
		return
	}
	for _, p := range paths {
		s.removeFile(ctx, p)
	}
}

// removeRenderCache deletes the synthesized pieces of chapters whose text
// changed or that are gone.
func (s *Server) removeRenderCache(ctx context.Context, chapterIDs ...int64) {
	for _, id := range chapterIDs {
		if err := os.RemoveAll(render.CacheDir(s.DataDir, id)); err != nil {
			s.Log.WarnContext(ctx, "remove render cache", "chapter", id, "err", err)
		}
	}
}

// removeFile deletes a file below the data dir (no-op for "").
func (s *Server) removeFile(ctx context.Context, rel string) {
	if rel == "" {
		return
	}
	if err := os.Remove(filepath.Join(s.DataDir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.Log.WarnContext(ctx, "remove file", "path", rel, "err", err)
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
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return ListChapters404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit, 10000, 10000)
	cs, total, err := s.Store.ListChapters(ctx, req.BookId, store.ChapterFilter{
		VolumeID: req.Params.VolumeId, CharacterID: req.Params.CharacterId,
	}, offset, limit)
	if err != nil {
		return nil, err
	}
	out := ChapterPage{Items: make([]Chapter, 0, len(cs)), Total: total}
	for i := range cs {
		out.Items = append(out.Items, s.toChapter(b, &cs[i]))
	}
	return ListChapters200JSONResponse(out), nil
}

// GetChapter returns a chapter with its text.
func (s *Server) GetChapter(ctx context.Context, req GetChapterRequestObject) (GetChapterResponseObject, error) {
	c, b, err := s.chapterBook(ctx, req.ChapterId)
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
	ch := s.toChapter(b, c)
	out := ChapterDetail{
		Id: ch.Id, BookId: ch.BookId, VolumeId: ch.VolumeId, Idx: ch.Idx, Title: ch.Title,
		WordCount: ch.WordCount, QuoteCount: ch.QuoteCount, Excluded: ch.Excluded, Status: ch.Status,
		AudioStatus: ch.AudioStatus, AudioDurationMs: ch.AudioDurationMs, AudioError: ch.AudioError,
		AudioStale: ch.AudioStale, Narration: ch.Narration, PovCharacterId: ch.PovCharacterId,
		Paragraphs: make([]Paragraph, 0, len(paras)),
	}
	chars, err := s.Store.ChapterCharacters(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	refs := make([]CharacterRef, 0, len(chars))
	for _, ch := range chars {
		refs = append(refs, CharacterRef{Id: ch.ID, Name: ch.Name})
	}
	out.Characters = &refs
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
	return UpdateChapter200JSONResponse(s.toChapter(nil, c)), nil
}

// MergeChapterWithNext appends the following chapter.
func (s *Server) MergeChapterWithNext(ctx context.Context, req MergeChapterWithNextRequestObject) (MergeChapterWithNextResponseObject, error) {
	// The next chapter disappears with the merge: remember its files.
	var (
		nextAudio string
		nextID    int64
	)
	if cur, err := s.Store.GetChapter(ctx, req.ChapterId); err == nil {
		if _, id, err := s.Store.AdjacentChapters(ctx, cur); err == nil && id != 0 {
			if next, err := s.Store.GetChapter(ctx, id); err == nil {
				nextAudio, nextID = next.AudioPath, id
			}
		}
	}
	if busy, err := s.analysisBusy(ctx, req.ChapterId); err != nil {
		return nil, err
	} else if busy {
		return MergeChapterWithNext409JSONResponse{Message: analysisBusyMsg}, nil
	}
	c, err := s.Store.MergeChapterWithNext(ctx, req.ChapterId)
	switch {
	case isNotFound(err):
		return MergeChapterWithNext404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	case errors.Is(err, store.ErrInvalid):
		return MergeChapterWithNext409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	s.dropAudio(ctx, c.ID)
	s.removeFile(ctx, nextAudio)
	if nextID != 0 {
		s.removeRenderCache(ctx, nextID)
	}
	c, err = s.Store.GetChapter(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return MergeChapterWithNext200JSONResponse(s.toChapter(nil, c)), nil
}

// SplitChapter starts a new chapter at a paragraph.
func (s *Server) SplitChapter(ctx context.Context, req SplitChapterRequestObject) (SplitChapterResponseObject, error) {
	title := strings.TrimSpace(req.Body.Title)
	if title == "" {
		return SplitChapter400JSONResponse{ErrorJSONResponse(errMsg("title must not be empty"))}, nil
	}
	if busy, err := s.analysisBusy(ctx, req.ChapterId); err != nil {
		return nil, err
	} else if busy {
		return SplitChapter409JSONResponse{Message: analysisBusyMsg}, nil
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
	s.dropAudio(ctx, req.ChapterId)
	return SplitChapter200JSONResponse(s.toChapter(nil, c)), nil
}
