package api

import (
	"cmp"
	"context"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// toChapter converts a chapter; b (the chapter's book) decides whether the
// audio is stale, rendering whether a render of it is pending.
func (s *Server) toChapter(b *store.Book, c *store.Chapter, rendering bool) Chapter {
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

		AudioRerendering: rendering && c.AudioStatus == store.AudioReady,
		AudioVersion:     nonEmpty(render.AudioVersion(c.AudioPath)),

		PovCharacterId: c.POVCharacterID,

		AttributionStatus: AttributionStatus(cmp.Or(c.AttributionStatus, store.AttributionNone)),
		AttributionError:  nonEmpty(c.AttributionError),
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
		if err := render.RemoveVersions(s.DataDir, p); err != nil {
			s.Log.WarnContext(ctx, "remove audio", "path", p, "err", err)
		}
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
	rendering, err := s.Store.PendingRenderChapters(ctx, render.JobType, req.BookId)
	if err != nil {
		return nil, err
	}
	out := ChapterPage{Items: make([]Chapter, 0, len(cs)), Total: total}
	for i := range cs {
		out.Items = append(out.Items, s.toChapter(b, &cs[i], rendering[cs[i].ID]))
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
	out, err := s.chapterDetail(ctx, b, c)
	if err != nil {
		return nil, err
	}
	return GetChapter200JSONResponse(out), nil
}

// chapterDetail builds a chapter with its text, characters and speakers.
func (s *Server) chapterDetail(ctx context.Context, b *store.Book, c *store.Chapter) (ChapterDetail, error) {
	paras, segs, err := s.Store.ChapterText(ctx, c.ID)
	if err != nil {
		return ChapterDetail{}, err
	}
	prev, next, err := s.Store.AdjacentChapters(ctx, c)
	if err != nil {
		return ChapterDetail{}, err
	}

	byPara := make(map[int64][]Segment, len(paras))
	var speakerIDs []int64
	seen := map[int64]bool{}
	for _, sg := range segs {
		seg := Segment{
			Id: sg.ID, Idx: sg.Idx, Kind: SegmentKind(sg.Kind), QuoteRef: sg.QuoteRef, Text: sg.Text,
			SpeakerId: sg.SpeakerCharacterID, Confidence: float32(sg.Confidence), SpeakerManual: sg.SpeakerManual,
		}
		if e := Emotion(sg.Emotion); e.Valid() {
			seg.Emotion = &e
		}
		if spans := store.DecodeSpans(sg.Emphasis, utf8.RuneCountInString(sg.Text)); len(spans) > 0 {
			emph := make([][]int, len(spans))
			for i, sp := range spans {
				emph[i] = []int{sp[0], sp[1]}
			}
			seg.Emphasis = &emph
		}
		if id := sg.SpeakerCharacterID; id != nil && !seen[*id] {
			seen[*id] = true
			speakerIDs = append(speakerIDs, *id)
		}
		byPara[sg.ParagraphID] = append(byPara[sg.ParagraphID], seg)
	}
	pending, err := s.Store.PendingRenderJob(ctx, render.JobType, c.ID)
	if err != nil {
		return ChapterDetail{}, err
	}
	ch := s.toChapter(b, c, pending != nil)
	out := ChapterDetail{
		Id: ch.Id, BookId: ch.BookId, VolumeId: ch.VolumeId, Idx: ch.Idx, Title: ch.Title,
		WordCount: ch.WordCount, QuoteCount: ch.QuoteCount, Excluded: ch.Excluded, Status: ch.Status,
		AudioStatus: ch.AudioStatus, AudioDurationMs: ch.AudioDurationMs, AudioError: ch.AudioError,
		AudioStale: ch.AudioStale, AudioRerendering: ch.AudioRerendering, AudioVersion: ch.AudioVersion,
		Narration:      ch.Narration,
		PovCharacterId: ch.PovCharacterId, AttributionStatus: ch.AttributionStatus, AttributionError: ch.AttributionError,
		Paragraphs: make([]Paragraph, 0, len(paras)),
	}
	att, err := s.toAttribution(ctx, c)
	if err != nil {
		return ChapterDetail{}, err
	}
	out.Attribution = &att
	speakers, err := s.Store.GetCharacters(ctx, speakerIDs...)
	if err != nil {
		return ChapterDetail{}, err
	}
	sp := make([]CharacterRef, 0, len(speakers))
	for _, x := range speakers {
		sp = append(sp, CharacterRef{Id: x.ID, Name: x.Name})
	}
	out.Speakers = &sp
	chars, err := s.Store.ChapterCharacters(ctx, c.ID)
	if err != nil {
		return ChapterDetail{}, err
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
	return out, nil
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
	return UpdateChapter200JSONResponse(s.toChapter(nil, c, false)), nil
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
	if msg, err := s.chapterEditBlocked(ctx, req.ChapterId); err != nil {
		return nil, err
	} else if msg != "" {
		return MergeChapterWithNext409JSONResponse{Message: msg}, nil
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
	if nextAudio != "" {
		if err := render.RemoveVersions(s.DataDir, nextAudio); err != nil {
			s.Log.WarnContext(ctx, "remove audio", "path", nextAudio, "err", err)
		}
	}
	if nextID != 0 {
		s.removeRenderCache(ctx, nextID)
	}
	c, err = s.Store.GetChapter(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return MergeChapterWithNext200JSONResponse(s.toChapter(nil, c, false)), nil
}

// SplitChapter starts a new chapter at a paragraph.
func (s *Server) SplitChapter(ctx context.Context, req SplitChapterRequestObject) (SplitChapterResponseObject, error) {
	title := strings.TrimSpace(req.Body.Title)
	if title == "" {
		return SplitChapter400JSONResponse{ErrorJSONResponse(errMsg("title must not be empty"))}, nil
	}
	if msg, err := s.chapterEditBlocked(ctx, req.ChapterId); err != nil {
		return nil, err
	} else if msg != "" {
		return SplitChapter409JSONResponse{Message: msg}, nil
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
	return SplitChapter200JSONResponse(s.toChapter(nil, c, false)), nil
}
