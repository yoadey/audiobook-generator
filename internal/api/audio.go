package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// voiceRecipeRe matches a Kokoro voice or weighted mix: "af_heart",
// "bf_emma(2)+af_bella(1)".
var voiceRecipeRe = regexp.MustCompile(`^[a-z]{2}_[a-z0-9]+(\([0-9]+(\.[0-9]+)?\))?(\+[a-z]{2}_[a-z0-9]+(\([0-9]+(\.[0-9]+)?\))?)*$`)

const defaultPreviewText = "It was a bright cold day in April, and the clocks were striking thirteen."

// UpdateBook changes narration settings.
func (s *Server) UpdateBook(ctx context.Context, req UpdateBookRequestObject) (UpdateBookResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return UpdateBook404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if v := req.Body.NarratorVoice; v != nil {
		voice := strings.TrimSpace(*v)
		if voice != "" && !voiceRecipeRe.MatchString(voice) {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg(
				`narratorVoice must be a voice like "af_heart" or a mix like "bf_emma(2)+af_bella(1)"`))}, nil
		}
		fields["NarratorVoice"] = voice
	}
	if v := req.Body.NarratorSpeed; v != nil {
		if *v < 0.5 || *v > 2 {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg("narratorSpeed must be between 0.5 and 2"))}, nil
		}
		fields["NarratorSpeed"] = float64(*v)
	}
	if v := req.Body.Lookahead; v != nil {
		if *v < 0 || *v > 50 {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg("lookahead must be between 0 and 50"))}, nil
		}
		fields["Lookahead"] = *v
	}
	if len(fields) > 0 {
		if err := s.Store.UpdateBook(ctx, req.BookId, fields); err != nil {
			return nil, err
		}
	}
	b, err := s.Store.GetBook(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	return UpdateBook200JSONResponse(s.toBook(b)), nil
}

// RenderBookChapters queues the next chapters of a book.
func (s *Server) RenderBookChapters(ctx context.Context, req RenderBookChaptersRequestObject) (RenderBookChaptersResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return RenderBookChapters404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	from := 0
	if req.Body.FromChapterId != nil {
		c, err := s.Store.GetChapter(ctx, *req.Body.FromChapterId)
		if err != nil || c.BookID != b.ID {
			return RenderBookChapters404JSONResponse{ErrorJSONResponse(errMsg("chapter not found in this book"))}, nil
		}
		from = c.Idx
	} else if p, err := s.Store.GetProgress(ctx, b.ID); err != nil {
		return nil, err
	} else if p != nil {
		if c, err := s.Store.GetChapter(ctx, p.ChapterID); err == nil {
			from = c.Idx
		}
	}
	n, err := s.Scheduler.RenderAhead(ctx, b, from, min(max(req.Body.Count, 1), 500))
	if err != nil {
		return nil, err
	}
	return RenderBookChapters202JSONResponse(RenderResult{Queued: n}), nil
}

func toProgress(p *store.ListenProgress) Progress {
	if p == nil {
		return Progress{}
	}
	return Progress{ChapterId: &p.ChapterID, PositionMs: p.PositionMs, UpdatedAt: &p.UpdatedAt}
}

// GetProgress returns the listening position.
func (s *Server) GetProgress(ctx context.Context, req GetProgressRequestObject) (GetProgressResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return GetProgress404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProgress(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	return GetProgress200JSONResponse(toProgress(p)), nil
}

// SaveProgress stores the listening position and tops up the lookahead.
func (s *Server) SaveProgress(ctx context.Context, req SaveProgressRequestObject) (SaveProgressResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return SaveProgress404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	c, err := s.Store.GetChapter(ctx, req.Body.ChapterId)
	if err != nil || c.BookID != b.ID {
		return SaveProgress400JSONResponse{ErrorJSONResponse(errMsg("chapter not found in this book"))}, nil
	}
	p := &store.ListenProgress{BookID: b.ID, ChapterID: c.ID, PositionMs: max(req.Body.PositionMs, 0)}
	if err := s.Store.SaveProgress(ctx, p); err != nil {
		return nil, err
	}
	if _, err := s.Scheduler.EnsureAhead(ctx, b); err != nil {
		s.Log.WarnContext(ctx, "lookahead", "book", b.ID, "err", err)
	}
	return SaveProgress200JSONResponse(toProgress(p)), nil
}

// RenderChapter queues one chapter.
func (s *Server) RenderChapter(ctx context.Context, req RenderChapterRequestObject) (RenderChapterResponseObject, error) {
	c, b, err := s.chapterBook(ctx, req.ChapterId)
	if isNotFound(err) {
		return RenderChapter404JSONResponse{ErrorJSONResponse(errMsg("chapter not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	force := req.Params.Force != nil && *req.Params.Force
	if _, err := s.Scheduler.RenderChapter(ctx, b, c, force); err != nil {
		return nil, err
	}
	c, err = s.Store.GetChapter(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return RenderChapter202JSONResponse(s.toChapter(b, c)), nil
}

// audioFile serves a file with Range support (http.ServeContent).
type audioFile struct {
	r    *http.Request
	path string
	name string
}

func (a audioFile) VisitGetChapterAudioResponse(w http.ResponseWriter) error {
	f, err := os.Open(a.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, a.r, a.name, st.ModTime(), f)
	return nil
}

// GetChapterAudio streams the chapter's audio.
func (s *Server) GetChapterAudio(ctx context.Context, req GetChapterAudioRequestObject) (GetChapterAudioResponseObject, error) {
	c, err := s.Store.GetChapter(ctx, req.ChapterId)
	if isNotFound(err) || (err == nil && (c.AudioPath == "" || c.AudioStatus == store.AudioNone)) {
		return GetChapterAudio404JSONResponse{ErrorJSONResponse(errMsg("no audio"))}, nil
	} else if err != nil {
		return nil, err
	}
	p := filepath.Join(s.DataDir, c.AudioPath)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return GetChapterAudio404JSONResponse{ErrorJSONResponse(errMsg("no audio"))}, nil
	}
	return audioFile{r: requestFrom(ctx), path: p, name: fmt.Sprintf("%d.m4a", c.ID)}, nil
}

var languages = map[byte]string{
	'a': "en-US", 'b': "en-GB", 'e': "es", 'f': "fr", 'h': "hi", 'i': "it",
	'j': "ja", 'p': "pt-BR", 'z': "zh",
}

// ListVoices lists the TTS server's voices.
func (s *Server) ListVoices(ctx context.Context, _ ListVoicesRequestObject) (ListVoicesResponseObject, error) {
	vs, err := s.TTS.Voices(ctx)
	if err != nil {
		return ListVoices502JSONResponse{ErrorJSONResponse(errMsg("TTS server: " + err.Error()))}, nil
	}
	out := make([]Voice, 0, len(vs))
	for _, v := range vs {
		voice := Voice{Id: v.ID, Language: "unknown", Gender: VoiceGenderUnknown, Grade: nonEmpty(v.OverallGrade)}
		if len(v.ID) >= 3 && v.ID[2] == '_' {
			if l, ok := languages[v.ID[0]]; ok {
				voice.Language = l
			}
			switch v.ID[1] {
			case 'f':
				voice.Gender = VoiceGenderFemale
			case 'm':
				voice.Gender = VoiceGenderMale
			}
		}
		out = append(out, voice)
	}
	return ListVoices200JSONResponse(out), nil
}

// PreviewVoice speaks a sample.
func (s *Server) PreviewVoice(ctx context.Context, req PreviewVoiceRequestObject) (PreviewVoiceResponseObject, error) {
	voice := strings.TrimSpace(req.Body.Voice)
	if !voiceRecipeRe.MatchString(voice) {
		return PreviewVoice400JSONResponse{ErrorJSONResponse(errMsg("invalid voice"))}, nil
	}
	text := defaultPreviewText
	if req.Body.Text != nil && strings.TrimSpace(*req.Body.Text) != "" {
		text = strings.TrimSpace(*req.Body.Text)
	}
	speed := 1.0
	if req.Body.Speed != nil {
		speed = min(max(float64(*req.Body.Speed), 0.5), 2)
	}
	data, err := s.TTS.Synthesize(ctx, tts.Speech{Text: text, Voice: voice, Speed: speed, Format: "mp3"})
	if err != nil {
		return PreviewVoice502JSONResponse{Message: "TTS server: " + err.Error()}, nil
	}
	return PreviewVoice200AudiompegResponse{
		Body: strings.NewReader(string(data)), ContentLength: int64(len(data)),
	}, nil
}

// RetryJob queues a failed job again.
func (s *Server) RetryJob(ctx context.Context, req RetryJobRequestObject) (RetryJobResponseObject, error) {
	j, err := s.Store.GetJob(ctx, req.JobId)
	if isNotFound(err) {
		return RetryJob404JSONResponse{ErrorJSONResponse(errMsg("job not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.RetryFailedJob(ctx, j.ID); errors.Is(err, store.ErrInvalid) {
		return RetryJob409JSONResponse{Message: "only failed or canceled jobs can be retried"}, nil
	} else if err != nil {
		return nil, err
	}
	if j.Type == render.JobType && j.ChapterID != nil {
		if err := s.Store.SetAudioStatus(ctx, store.AudioQueued, "", *j.ChapterID); err != nil {
			return nil, err
		}
	}
	if w, ok := s.Jobs.(interface{ Wake(queue string) }); ok {
		w.Wake(j.Queue)
	}
	j, err = s.Store.GetJob(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return RetryJob200JSONResponse(toJob(j)), nil
}

// CancelJob cancels a queued job.
func (s *Server) CancelJob(ctx context.Context, req CancelJobRequestObject) (CancelJobResponseObject, error) {
	j, err := s.Store.GetJob(ctx, req.JobId)
	if isNotFound(err) {
		return CancelJob404JSONResponse{ErrorJSONResponse(errMsg("job not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.CancelJob(ctx, j.ID); errors.Is(err, store.ErrInvalid) {
		return CancelJob409JSONResponse{Message: "only queued jobs can be canceled"}, nil
	} else if err != nil {
		return nil, err
	}
	if j.Type == render.JobType && j.ChapterID != nil {
		if err := s.Store.SetAudioStatus(ctx, store.AudioNone, "", *j.ChapterID); err != nil {
			return nil, err
		}
	}
	j, err = s.Store.GetJob(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return CancelJob200JSONResponse(toJob(j)), nil
}
