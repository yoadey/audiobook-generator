package api

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/assemble"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// downloads lists a book's parts with the state of their files.
func (s *Server) downloads(ctx context.Context, bookID int64) ([]Download, error) {
	b, err := s.Store.GetBook(ctx, bookID)
	if err != nil {
		return nil, err
	}
	chapters, _, err := s.Store.ListChapters(ctx, bookID, store.ChapterFilter{}, 0, 1<<30)
	if err != nil {
		return nil, err
	}
	vols, err := s.Store.ListVolumes(ctx, bookID)
	if err != nil {
		return nil, err
	}
	files, err := s.Store.ListDownloads(ctx, bookID)
	if err != nil {
		return nil, err
	}
	byVolume := make(map[int64]*store.Download, len(files))
	for i := range files {
		byVolume[files[i].VolumeID] = &files[i]
	}
	pending, err := s.Assembler.Pending(ctx, bookID)
	if err != nil {
		return nil, err
	}
	out := []Download{}
	for _, p := range assemble.Parts(b, chapters, vols) {
		if len(p.Chapters) == 0 {
			continue
		}
		d := Download{
			VolumeId: p.VolumeID, Title: p.Title, ChapterCount: len(p.Chapters), Status: DownloadStatusNone,
		}
		for i := range p.Chapters {
			c := &p.Chapters[i]
			if c.AudioPath != "" && !s.Scheduler.Stale(b, c) {
				d.RenderedCount++
			}
		}
		if f := byVolume[p.VolumeID]; f != nil {
			d.Status = DownloadStatus(f.Status)
			d.Error = nonEmpty(f.Error)
			d.HasFile = f.Path != ""
			if d.HasFile {
				d.SizeBytes = &f.Size
				d.DurationMs = &f.DurationMs
				d.Outdated = f.SourceKey != assemble.SourceKey(&p)
			}
		}
		if j := pending[p.VolumeID]; j != nil {
			job := toJob(j)
			d.Job = &job
		}
		out = append(out, d)
	}
	return out, nil
}

// ListDownloads lists the book's M4B downloads.
func (s *Server) ListDownloads(ctx context.Context, req ListDownloadsRequestObject) (ListDownloadsResponseObject, error) {
	out, err := s.downloads(ctx, req.BookId)
	if isNotFound(err) {
		return ListDownloads404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	return ListDownloads200JSONResponse(out), nil
}

// AssembleDownload queues the M4B of a volume.
func (s *Server) AssembleDownload(ctx context.Context, req AssembleDownloadRequestObject) (AssembleDownloadResponseObject, error) {
	switch err := s.Assembler.Start(ctx, req.BookId, req.VolumeId); {
	case isNotFound(err):
		return AssembleDownload404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	case errors.Is(err, assemble.ErrNoPart):
		return AssembleDownload404JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	case err != nil:
		return nil, err
	}
	out, err := s.downloads(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	for _, d := range out {
		if d.VolumeId == req.VolumeId {
			return AssembleDownload202JSONResponse(d), nil
		}
	}
	return AssembleDownload404JSONResponse{ErrorJSONResponse(errMsg(assemble.ErrNoPart.Error()))}, nil
}

type downloadFile struct {
	r    *http.Request
	path string
	name string
}

func (d downloadFile) VisitGetDownloadFileResponse(w http.ResponseWriter) error {
	f, err := os.Open(d.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.name}))
	http.ServeContent(w, d.r, "", st.ModTime(), f)
	return nil
}

// GetDownloadFile serves an assembled M4B.
func (s *Server) GetDownloadFile(ctx context.Context, req GetDownloadFileRequestObject) (GetDownloadFileResponseObject, error) {
	notFound := GetDownloadFile404JSONResponse{ErrorJSONResponse(errMsg("no file"))}
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return notFound, nil
	} else if err != nil {
		return nil, err
	}
	d, err := s.Store.GetDownload(ctx, req.BookId, req.VolumeId)
	if isNotFound(err) || err == nil && d.Path == "" {
		return notFound, nil
	} else if err != nil {
		return nil, err
	}
	p := filepath.Join(s.DataDir, d.Path)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return notFound, nil
	}
	name := b.Title
	if d.VolumeID != 0 {
		vols, err := s.Store.ListVolumes(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		for _, v := range vols {
			if v.ID == d.VolumeID && v.Title != "" && v.Title != b.Title {
				name += " - " + v.Title
			}
		}
	}
	return downloadFile{r: requestFrom(ctx), path: p, name: fileName(name) + ".m4b"}, nil
}

// fileName makes a title safe as a file name.
func fileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 150 {
		s = string(r[:150])
	}
	if s == "" {
		return "audiobook"
	}
	return s
}
