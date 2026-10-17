package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/assemble"
	"github.com/yoadey/audiobook-generator/internal/attribution"
	"github.com/yoadey/audiobook-generator/internal/auth"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
)

const defaultPreviewText = "It was a bright cold day in April, and the clocks were striking thirteen."

// UpdateBook changes narration settings.
func (s *Server) UpdateBook(ctx context.Context, req UpdateBookRequestObject) (UpdateBookResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return UpdateBook404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	touch, recast := false, false
	if v := req.Body.PrepareChapters; v != nil && *v != b.PrepareChapters {
		// The preparation changes what is said: renders made with (or
		// without) it are outdated. Touched before the change too, so a
		// failure after it is not lost to a retry.
		if err := s.Store.TouchBookAudio(ctx, b.ID); err != nil {
			return nil, err
		}
		fields["PrepareChapters"] = *v
		touch = true
	}
	if v := req.Body.NarratorVoice; v != nil {
		voice := strings.TrimSpace(*v)
		if _, err := casting.ParseRecipe(voice); voice != "" && err != nil {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg(
				`narratorVoice must be a voice like "af_heart" or a mix like "bf_emma(2)+af_bella(1)"`))}, nil
		}
		if msg := s.engineMissing(voice); msg != "" {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
		}
		fields["NarratorVoice"] = strings.Join(strings.Fields(voice), "")
		// Another engine: the characters are cast from its voices.
		old, _ := casting.Narrator(b, s.DefaultVoice)
		next, _ := casting.Narrator(&store.Book{NarratorVoice: voice}, s.DefaultVoice)
		recast = tts.EngineOf(old) != tts.EngineOf(next)
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
	if v := req.Body.PreviewText; v != nil {
		text, msg := previewText(*v)
		if msg != "" {
			return UpdateBook400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
		}
		fields["PreviewText"] = text
	}
	if len(fields) > 0 {
		if err := s.Store.UpdateBook(ctx, req.BookId, fields); err != nil {
			return nil, err
		}
	}
	if touch {
		if err := s.Store.TouchBookAudio(ctx, b.ID); err != nil {
			return nil, err
		}
	}
	b, err = s.Store.GetBook(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	if recast {
		if err := s.Store.ResetCastVoices(ctx, b.ID); err != nil {
			return nil, err
		}
		if b.AnalysisStatus == store.AnalysisDone && s.Caster != nil {
			if err := s.Caster.Start(ctx, b, false); err != nil {
				return nil, err
			}
			if b, err = s.Store.GetBook(ctx, req.BookId); err != nil {
				return nil, err
			}
		}
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
	} else if p, err := s.Store.GetProgress(ctx, b.ID, auth.Subject(ctx)); err != nil {
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
	return Progress{ChapterId: &p.ChapterID, PositionMs: p.PositionMs, SegmentId: p.SegmentID, UpdatedAt: &p.UpdatedAt}
}

// GetProgress returns the listening position.
func (s *Server) GetProgress(ctx context.Context, req GetProgressRequestObject) (GetProgressResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return GetProgress404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProgress(ctx, req.BookId, auth.Subject(ctx))
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
	p := &store.ListenProgress{BookID: b.ID, UserID: auth.Subject(ctx), ChapterID: c.ID,
		PositionMs: max(req.Body.PositionMs, 0)}
	if id := req.Body.SegmentId; id != nil {
		seg, err := s.Store.GetSegment(ctx, *id)
		if isNotFound(err) || err == nil && seg.ChapterID != c.ID {
			return SaveProgress400JSONResponse{ErrorJSONResponse(errMsg("segment not found in this chapter"))}, nil
		} else if err != nil {
			return nil, err
		}
		p.SegmentID = &seg.ID
	}
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
	pending, err := s.Store.PendingRenderJob(ctx, render.JobType, c.ID)
	if err != nil {
		return nil, err
	}
	return RenderChapter202JSONResponse(s.toChapter(b, c, pending != nil)), nil
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
	// A version the player started with, while it is kept.
	p := filepath.Join(s.DataDir, render.KeptPath(s.DataDir, c, req.Params.V))
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return GetChapterAudio404JSONResponse{ErrorJSONResponse(errMsg("no audio"))}, nil
	}
	return audioFile{r: requestFrom(ctx), path: p, name: fmt.Sprintf("%d.m4a", c.ID)}, nil
}

// ListVoices lists the voice catalogue as the TTS server offers it.
func (s *Server) ListVoices(ctx context.Context, _ ListVoicesRequestObject) (ListVoicesResponseObject, error) {
	vs, err := s.Voices.Voices(ctx)
	if err != nil {
		return ListVoices502JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	}
	out := make([]Voice, 0, len(vs))
	for _, v := range vs {
		if !v.Available && !v.InCatalog {
			continue
		}
		out = append(out, Voice{
			Id: v.ID, Language: v.Accent, Gender: VoiceGender(v.Gender), Grade: nonEmpty(v.Grade),
			Traits: v.Traits, Available: v.Available, InCatalog: v.InCatalog,
		})
	}
	return ListVoices200JSONResponse(out), nil
}

// engineMissing tells why a voice cannot be used: its engine is not
// configured ("" when it can, or the TTS cannot tell).
func (s *Server) engineMissing(voice string) string {
	voice = strings.TrimSpace(voice)
	c, ok := s.TTS.(interface{ Configured(voice string) bool })
	if voice == "" || !ok || c.Configured(voice) {
		return ""
	}
	engine, _, isEngine := tts.SplitVoice(voice)
	if !isEngine {
		return "Kokoro is not part of this deployment: choose a voice of another engine"
	}
	return fmt.Sprintf("the engine %q of this voice is not configured (ABG_TTS_ENGINES)", engine)
}

// EngineLister lists the speech engines besides Kokoro (the TTS may not).
type EngineLister interface {
	Engines(ctx context.Context) []tts.Engine
}

// ListTTSEngines lists the configured engines with their voices.
func (s *Server) ListTTSEngines(ctx context.Context, _ ListTTSEnginesRequestObject) (ListTTSEnginesResponseObject, error) {
	out := []TTSEngine{}
	if l, ok := s.TTS.(EngineLister); ok {
		for _, e := range l.Engines(ctx) {
			voices := e.Voices
			if voices == nil {
				voices = []string{}
			}
			out = append(out, TTSEngine{
				Name: e.Name, Kind: TTSEngineKind(e.Kind), Variant: nonEmpty(e.Variant), Emotions: e.Emotions,
				Speed: e.Speed, Voices: voices, DefaultVoice: nonEmpty(e.DefaultVoice), Error: nonEmpty(e.Err),
			})
		}
	}
	return ListTTSEngines200JSONResponse(out), nil
}

// PreviewVoice speaks a sample; recent previews are cached.
func (s *Server) PreviewVoice(ctx context.Context, req PreviewVoiceRequestObject) (PreviewVoiceResponseObject, error) {
	voice := strings.TrimSpace(req.Body.Voice)
	if _, err := casting.ParseRecipe(voice); err != nil {
		return PreviewVoice400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	}
	if msg := s.engineMissing(voice); msg != "" {
		return PreviewVoice400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	var text string
	if req.Body.Text != nil {
		text = strings.TrimSpace(*req.Body.Text)
	}
	if utf8.RuneCountInString(text) > maxPreviewText {
		return PreviewVoice400JSONResponse{ErrorJSONResponse(errMsg("text must be at most 500 characters"))}, nil
	}
	if text == "" {
		var err error
		if text, err = s.samplePreviewText(ctx, req.Body.BookId); err != nil {
			return nil, err
		}
	}
	speed := 1.0
	if req.Body.Speed != nil {
		speed = min(max(float64(*req.Body.Speed), 0.5), 2)
	}
	if req.Body.BookId != nil {
		entries, err := s.Store.ListLexicon(ctx, *req.Body.BookId)
		if err != nil {
			return nil, err
		}
		text = lexicon.Compile(entries).Apply(text)
	}
	emotion := ""
	if e := req.Body.Emotion; e != nil && *e != EmotionNeutral {
		if !e.Valid() {
			return PreviewVoice400JSONResponse{ErrorJSONResponse(errMsg("unknown emotion"))}, nil
		}
		emotion = string(*e)
	}
	key := previewKey(voice+"|"+emotion, speed, text)
	data, ok := s.previews.get(key)
	if !ok {
		var err error
		data, err = s.TTS.Synthesize(ctx, tts.Speech{Text: text, Voice: voice, Speed: speed, Format: "mp3", Emotion: emotion})
		if err != nil {
			return PreviewVoice502JSONResponse{Message: "TTS server: " + err.Error()}, nil
		}
		s.previews.put(key, data)
	}
	return PreviewVoice200AudiompegResponse{
		Body: bytes.NewReader(data), ContentLength: int64(len(data)),
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
	if j.Type == render.JobType && j.ChapterID != nil {
		if p, err := s.Store.PendingRenderJob(ctx, render.JobType, *j.ChapterID); err != nil {
			return nil, err
		} else if p != nil {
			return RetryJob409JSONResponse{Message: "the chapter is already queued for rendering"}, nil
		}
	}
	if j.Type == voicedesign.JobType {
		if msg, err := s.retryDesign(ctx, j); err != nil || msg != "" {
			if err != nil {
				return nil, err
			}
			return RetryJob409JSONResponse{Message: msg}, nil
		}
	}
	if err := s.Store.RetryFailedJob(ctx, j.ID); errors.Is(err, store.ErrInvalid) {
		if j.Type == voicedesign.JobType {
			s.undoRetryDesign(ctx, j)
		}
		return RetryJob409JSONResponse{Message: "only failed or canceled jobs can be retried"}, nil
	} else if err != nil {
		return nil, err
	}
	if j.Type == render.JobType && j.ChapterID != nil {
		// Existing audio stays playable while it is rendered again.
		if c, err := s.Store.GetChapter(ctx, *j.ChapterID); err == nil && c.AudioPath == "" {
			if err := s.Store.SetAudioStatus(ctx, store.AudioQueued, "", *j.ChapterID); err != nil {
				return nil, err
			}
		}
	}
	if j.Type == attribution.JobType && j.ChapterID != nil {
		if _, err := s.Store.TransitionAttribution(ctx, *j.ChapterID, store.AttributionQueued,
			map[string]any{"attribution_error": ""}, store.AttributionFailed); err != nil {
			return nil, err
		}
		// Canceled before: from the start.
		if _, err := s.Store.TransitionAttribution(ctx, *j.ChapterID, store.AttributionQueued,
			map[string]any{"attribution_error": "", "attribution_part": 0}, store.AttributionNone); err != nil {
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
	if j.Type == attribution.JobType && j.ChapterID != nil {
		// Back to "not attributed": the next render queues it again.
		if _, err := s.Store.TransitionAttribution(ctx, *j.ChapterID, store.AttributionNone, nil,
			store.AttributionQueued, store.AttributionRunning); err != nil {
			return nil, err
		}
	}
	if j.Type == render.JobType && j.ChapterID != nil {
		// A forced or stale re-render may have been canceled: existing
		// audio stays playable.
		status := store.AudioNone
		if c, err := s.Store.GetChapter(ctx, *j.ChapterID); err == nil && c.AudioPath != "" {
			status = store.AudioReady
		}
		if err := s.Store.SetAudioStatus(ctx, status, "", *j.ChapterID); err != nil {
			return nil, err
		}
	}
	if j.Type == voicedesign.JobType {
		// Unless a later design of the character is still pending.
		if id, err := strconv.ParseInt(j.Payload, 10, 64); err == nil {
			if n, err := s.Store.PendingJobs(ctx, voicedesign.JobType, j.Payload); err != nil {
				return nil, err
			} else if n == 0 {
				if _, err := s.Store.TransitionDesign(ctx, id, store.DesignFailed,
					map[string]any{"design_error": "canceled"}, store.DesignQueued, store.DesignRunning); err != nil {
					return nil, err
				}
			}
		}
	}
	if j.Type == assemble.JobType && j.BookID != nil {
		if volumeID, err := strconv.ParseInt(j.Payload, 10, 64); err == nil {
			if err := s.Store.SetDownloadStatus(ctx, *j.BookID, volumeID, store.DownloadFailed, "canceled"); err != nil &&
				!isNotFound(err) {
				return nil, err
			}
		}
	}
	j, err = s.Store.GetJob(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return CancelJob200JSONResponse(toJob(j)), nil
}
