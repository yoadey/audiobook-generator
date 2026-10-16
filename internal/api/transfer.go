package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/yoadey/audiobook-generator/internal/importer"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/transfer"
)

type exportStream struct {
	s    *Server
	ctx  context.Context
	book *store.Book
}

func (e exportStream) VisitExportBookResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": fileName(e.book.Title) + ".abg.zip"}))
	if err := transfer.Export(e.ctx, e.s.Store, e.s.DataDir, e.book.ID, w); err != nil {
		// The status is sent already: the client sees a broken zip.
		e.s.Log.ErrorContext(e.ctx, "export failed", "book", e.book.ID, "err", err)
		panic(http.ErrAbortHandler)
	}
	return nil
}

// ExportBook streams a book's export.
func (s *Server) ExportBook(ctx context.Context, req ExportBookRequestObject) (ExportBookResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return ExportBook404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if b.Status == store.BookImporting || b.Status == store.BookFailed {
		return ExportBook409JSONResponse{Message: "only imported books can be exported"}, nil
	}
	return exportStream{s: s, ctx: ctx, book: b}, nil
}

// ImportBook stores an uploaded export and queues its restore.
func (s *Server) ImportBook(ctx context.Context, req ImportBookRequestObject) (ImportBookResponseObject, error) {
	bad := func(msg string) (ImportBookResponseObject, error) {
		return ImportBook400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	up, err := s.saveUpload(req.Body, "import-*.zip")
	if err != nil {
		return nil, err
	}
	if up.Status == http.StatusRequestEntityTooLarge {
		return ImportBook413JSONResponse{Message: up.Msg}, nil
	} else if up.Status != 0 {
		return bad(up.Msg)
	}
	tmp := up.Path
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmp)
		}
	}()
	h, err := transfer.ReadHeader(tmp)
	if err != nil {
		return bad(err.Error())
	}
	b := &store.Book{
		Title: h.Book.Title, Author: h.Book.Author, Language: h.Book.Language, Status: store.BookImporting,
		NarratorSpeed: 1, Lookahead: 3,
	}
	if err := s.Store.CreateBook(ctx, b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(s.DataDir, importer.BookDir(b.ID)), 0o750); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(s.DataDir, transfer.RestorePath(b.ID))); err != nil {
		return nil, err
	}
	keep = true
	if err := s.Jobs.Enqueue(ctx, &store.Job{Type: transfer.JobType, BookID: &b.ID, Priority: 10}); err != nil {
		return nil, err
	}
	s.Log.InfoContext(ctx, "book export uploaded", "book", b.ID, "title", b.Title)
	return ImportBook202JSONResponse(s.toBook(b)), nil
}

// upload is a stored multipart file, or a client error (Status, Msg).
type upload struct {
	Path     string
	FileName string
	Status   int
	Msg      string
}

// saveUpload stores the multipart field "file" in a temporary file below
// the books directory.
func (s *Server) saveUpload(body *multipart.Reader, pattern string) (upload, error) {
	tooLarge := fmt.Sprintf("file is larger than the limit of %d MiB", s.MaxUploadBytes>>20)
	var part *multipart.Part
	for part == nil {
		p, err := body.NextPart()
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return upload{Status: http.StatusRequestEntityTooLarge, Msg: tooLarge}, nil
		case errors.Is(err, io.EOF):
			return upload{Status: http.StatusBadRequest, Msg: `missing form field "file"`}, nil
		case err != nil:
			return upload{Status: http.StatusBadRequest, Msg: "invalid multipart body: " + err.Error()}, nil
		}
		if p.FormName() == "file" {
			part = p
		}
	}
	booksDir := filepath.Join(s.DataDir, "books")
	if err := os.MkdirAll(booksDir, 0o750); err != nil {
		return upload{}, err
	}
	tmp, err := os.CreateTemp(booksDir, pattern)
	if err != nil {
		return upload{}, err
	}
	_, err = io.Copy(tmp, part)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		_ = os.Remove(tmp.Name())
		return upload{Status: http.StatusRequestEntityTooLarge, Msg: tooLarge}, nil
	case err != nil:
		_ = os.Remove(tmp.Name())
		return upload{}, fmt.Errorf("store upload: %w", err)
	}
	return upload{Path: tmp.Name(), FileName: part.FileName()}, nil
}
