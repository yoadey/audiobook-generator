package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/feed"
	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

func (s *Server) toBook(b *store.Book) Book {
	voice, speed := render.NarratorVoice(b, s.DefaultVoice)
	return Book{
		NarratorVoice:   voice,
		Engine:          tts.EngineOf(voice),
		NarratorSpeed:   float32(speed),
		Lookahead:       b.Lookahead,
		PreviewText:     b.PreviewText,
		PrepareChapters: b.PrepareChapters,
		Id:              b.ID,
		Title:           b.Title,
		Author:          b.Author,
		Language:        b.Language,
		Status:          BookStatus(b.Status),
		Progress:        float32(b.Progress),
		Error:           nonEmpty(b.Error),
		WordCount:       b.WordCount,
		ChapterCount:    b.ChapterCount,
		HasCover:        b.CoverPath != "",
		AnalysisStatus:  AnalysisStatus(cmp.Or(b.AnalysisStatus, store.AnalysisNone)),
		CastingStatus:   CastingStatus(cmp.Or(b.CastingStatus, store.CastingNone)),
		FeedPath:        nonEmpty(feed.Path(s.FeedSecret, b.ID)),
		CreatedAt:       b.CreatedAt,
		UpdatedAt:       b.UpdatedAt,
	}
}

// ListBooks returns a page of books.
func (s *Server) ListBooks(ctx context.Context, req ListBooksRequestObject) (ListBooksResponseObject, error) {
	offset, limit := page(req.Params.Offset, req.Params.Limit, 50, 500)
	books, total, err := s.Store.ListBooks(ctx, offset, limit)
	if err != nil {
		return nil, err
	}
	out := BookPage{Items: make([]Book, 0, len(books)), Total: total}
	for i := range books {
		bk := s.toBook(&books[i])
		if err := s.addAnalysisProgress(ctx, &bk); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, bk)
	}
	return ListBooks200JSONResponse(out), nil
}

// GetBook returns one book.
func (s *Server) GetBook(ctx context.Context, req GetBookRequestObject) (GetBookResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return GetBook404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	out := s.toBook(b)
	if err := s.addAnalysisProgress(ctx, &out); err != nil {
		return nil, err
	}
	return GetBook200JSONResponse(out), nil
}

// UploadBook stores an uploaded EPUB and queues its import.
func (s *Server) UploadBook(ctx context.Context, req UploadBookRequestObject) (UploadBookResponseObject, error) {
	tooLarge := func() (UploadBookResponseObject, error) {
		return UploadBook413JSONResponse{
			Message: fmt.Sprintf("file is larger than the limit of %d MiB", s.MaxUploadBytes>>20),
		}, nil
	}
	bad := func(msg string) (UploadBookResponseObject, error) {
		return UploadBook400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}

	up, err := s.saveUpload(req.Body, "upload-*.epub")
	if err != nil {
		return nil, err
	}
	if up.Status == http.StatusRequestEntityTooLarge {
		return tooLarge()
	} else if up.Status != 0 {
		return bad(up.Msg)
	}
	tmpPath := up.Path
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()

	eb, f, err := importer.OpenEPUB(tmpPath)
	if err != nil {
		return bad("not a valid EPUB: " + err.Error())
	}
	_ = f.Close()

	title := strings.TrimSpace(eb.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(up.FileName), filepath.Ext(up.FileName))
	}
	if title == "" {
		title = "Untitled"
	}
	b := &store.Book{
		Title: title, Author: eb.Author, Language: eb.Language, Status: store.BookImporting,
		NarratorSpeed: 1, Lookahead: 3,
	}
	if err := s.Store.CreateBook(ctx, b); err != nil {
		return nil, err
	}
	b.EpubPath = importer.SourcePath(b.ID)
	if err := os.MkdirAll(filepath.Join(s.DataDir, importer.BookDir(b.ID)), 0o750); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, filepath.Join(s.DataDir, b.EpubPath)); err != nil {
		return nil, err
	}
	keep = true
	if err := s.Store.UpdateBook(ctx, b.ID, map[string]any{"EpubPath": b.EpubPath}); err != nil {
		return nil, err
	}
	if err := s.Jobs.Enqueue(ctx, &store.Job{Type: importer.JobType, BookID: &b.ID, Priority: 10}); err != nil {
		return nil, err
	}
	s.Log.InfoContext(ctx, "book uploaded", "book", b.ID, "title", b.Title)
	return UploadBook202JSONResponse(s.toBook(b)), nil
}

// DeleteBook removes a book, its data and its files.
func (s *Server) DeleteBook(ctx context.Context, req DeleteBookRequestObject) (DeleteBookResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return DeleteBook404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if b.Status == store.BookImporting {
		return DeleteBookdefaultJSONResponse{
			StatusCode: http.StatusConflict,
			Body:       errMsg("the book is still being imported; delete it afterwards"),
		}, nil
	}
	if busy, err := s.Analyzer.Busy(ctx, b); err != nil {
		return nil, err
	} else if busy {
		return DeleteBookdefaultJSONResponse{StatusCode: http.StatusConflict, Body: errMsg(analysisBusyMsg)}, nil
	}
	chapterIDs, err := s.Store.ChapterIDs(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	if err := s.Store.DeleteBook(ctx, b.ID); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(s.DataDir, importer.BookDir(b.ID))); err != nil {
		s.Log.WarnContext(ctx, "remove book files", "book", b.ID, "err", err)
	}
	s.removeRenderCache(ctx, chapterIDs...)
	return DeleteBook204Response{}, nil
}

// GetBookCover serves the cover image.
func (s *Server) GetBookCover(ctx context.Context, req GetBookCoverRequestObject) (GetBookCoverResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	// A cover stored before types were checked is only served if it is a
	// plain image (an SVG or HTML "cover" would run as script).
	if isNotFound(err) || (err == nil && (b.CoverPath == "" || !importer.AllowedCoverType(b.CoverMediaType))) {
		return GetBookCover404JSONResponse{ErrorJSONResponse(errMsg("no cover"))}, nil
	} else if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.DataDir, b.CoverPath))
	if errors.Is(err, os.ErrNotExist) {
		return GetBookCover404JSONResponse{ErrorJSONResponse(errMsg("no cover"))}, nil
	} else if err != nil {
		return nil, err
	}
	return GetBookCover200ImageResponse{
		Body:          strings.NewReader(string(data)),
		ContentType:   b.CoverMediaType,
		ContentLength: int64(len(data)),
	}, nil
}
