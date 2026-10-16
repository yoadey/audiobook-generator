package api

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/analysis"
	"github.com/yoadey/audiobook-generator/internal/casting"
	"github.com/yoadey/audiobook-generator/internal/store"
)

const (
	analysisBusyMsg    = "the character analysis of this book is running; pause it first (and let the current request finish)"
	analysisPendingMsg = "the character analysis of this book is not finished; chapters can be merged or split before it starts or once it is done (or after a restart)"
)

// chapterEditBlocked explains why a chapter's book must not be
// restructured now ("" when it may): chapters must not change under a
// running analysis, nor while planned batches still refer to them.
func (s *Server) chapterEditBlocked(ctx context.Context, chapterID int64) (string, error) {
	_, b, err := s.chapterBook(ctx, chapterID)
	if isNotFound(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if busy, err := s.Analyzer.Busy(ctx, b); err != nil || busy {
		return analysisBusyMsg, err
	}
	if unfinished, err := s.Analyzer.Unfinished(ctx, b.ID); err != nil || unfinished {
		return analysisPendingMsg, err
	}
	return "", nil
}

// addAnalysisProgress fills in the share of analysed batches.
func (s *Server) addAnalysisProgress(ctx context.Context, b *Book) error {
	if b.AnalysisStatus == AnalysisStatusNone {
		return nil
	}
	st, err := s.Store.AnalysisStats(ctx, b.Id)
	if err != nil {
		return err
	}
	if st.Total > 0 {
		p := float32(st.Done) / float32(st.Total)
		b.AnalysisProgress = &p
	}
	return nil
}

func (s *Server) toAnalysis(ctx context.Context, b *store.Book) (Analysis, error) {
	st, err := s.Store.AnalysisStats(ctx, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	n, err := s.Store.CountCharacters(ctx, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	out := Analysis{
		Status:          AnalysisStatus(b.AnalysisStatus),
		Error:           nonEmpty(b.AnalysisError),
		StartedAt:       b.AnalysisStartedAt,
		FinishedAt:      b.AnalysisFinishedAt,
		BatchesTotal:    st.Total,
		BatchesDone:     st.Done,
		EstimatedTokens: st.EstimatedTokens + int64(st.Total*analysis.OverheadTokens),
		InputTokens:     st.InputTokens,
		OutputTokens:    st.OutputTokens,
		CharacterCount:  n,
		BatchTokens:     s.Analyzer.BatchTokens,
		Prompt:          analysis.PromptID(),
	}
	if out.Status == "" {
		out.Status = AnalysisStatusNone
	}
	if st.Total == 0 {
		est, err := s.Analyzer.EstimateBook(ctx, b.ID)
		if err != nil {
			return Analysis{}, err
		}
		out.BatchesTotal, out.EstimatedTokens = est.Batches, est.Tokens
	}
	j, err := s.Store.PendingBookJob(ctx, analysis.JobType, b.ID)
	if err != nil {
		return Analysis{}, err
	}
	if j != nil {
		job := toJob(j)
		out.Job = &job
	} else if analysis.Active(b) && time.Since(b.UpdatedAt) > time.Minute {
		// The job stopped (e.g. it saw a pause just before a resume):
		// report it as paused, so it can be resumed. (Not right after a
		// start, which sets the status before it queues the job.)
		ok, err := s.Store.PauseStaleAnalysis(ctx, b.ID, b.AnalysisStatus, b.UpdatedAt)
		if err != nil {
			return Analysis{}, err
		}
		if ok {
			out.Status = AnalysisStatusPaused
		}
	}
	return out, nil
}

// GetAnalysis reports the character analysis of a book.
func (s *Server) GetAnalysis(ctx context.Context, req GetAnalysisRequestObject) (GetAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return GetAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return GetAnalysis200JSONResponse(out), nil
}

// StartAnalysis queues, resumes or restarts the analysis.
func (s *Server) StartAnalysis(ctx context.Context, req StartAnalysisRequestObject) (StartAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return StartAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	restart := req.Body != nil && req.Body.Restart != nil && *req.Body.Restart
	switch err := s.Analyzer.Start(ctx, b, restart); {
	case errors.Is(err, analysis.ErrBusy):
		return StartAnalysis409JSONResponse{Message: analysisBusyMsg}, nil
	case errors.Is(err, store.ErrInvalid):
		return StartAnalysis409JSONResponse{Message: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	if b, err = s.Store.GetBook(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return StartAnalysis202JSONResponse(out), nil
}

// PauseAnalysis stops the analysis after the current batch.
func (s *Server) PauseAnalysis(ctx context.Context, req PauseAnalysisRequestObject) (PauseAnalysisResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return PauseAnalysis404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Analyzer.Pause(ctx, b); err != nil {
		return nil, err
	}
	if b, err = s.Store.GetBook(ctx, b.ID); err != nil {
		return nil, err
	}
	out, err := s.toAnalysis(ctx, b)
	if err != nil {
		return nil, err
	}
	return PauseAnalysis200JSONResponse(out), nil
}

// validateCharacterUpdate checks what the OpenAPI schema promises (the
// server does not validate requests against it).
func validateCharacterUpdate(u *CharacterUpdate) string {
	tooLong := func(s *string, n int) bool { return s != nil && utf8.RuneCountInString(*s) > n }
	switch {
	case u.Gender != nil && !u.Gender.Valid():
		return "gender must be female, male or unknown"
	case u.AgeGroup != nil && !u.AgeGroup.Valid():
		return "ageGroup must be child, teen, adult, elderly or unknown"
	case u.Importance != nil && (*u.Importance < 1 || *u.Importance > 5):
		return "importance must be between 1 and 5"
	case tooLong(u.Name, 120), tooLong(u.Role, 120), tooLong(u.SpeechStyle, 200), tooLong(u.Description, 1000):
		return "a field is too long"
	case u.Aliases != nil && len(*u.Aliases) > 200:
		return "too many aliases"
	case u.Tier != nil && !u.Tier.Valid():
		return "tier must be main, minor or auto"
	case u.VoiceSpeed != nil && (*u.VoiceSpeed < 0.5 || *u.VoiceSpeed > 2):
		return "voiceSpeed must be between 0.5 and 2"
	}
	if u.Voice != nil && strings.TrimSpace(*u.Voice) != "" {
		if _, err := casting.ParseRecipe(*u.Voice); err != nil {
			return err.Error()
		}
	}
	if u.Aliases != nil {
		for _, a := range *u.Aliases {
			if utf8.RuneCountInString(a) > 120 {
				return "an alias is too long"
			}
		}
	}
	return ""
}

func toCharacter(c *store.Character, aliases []store.CharacterAlias, v casting.Voice) Character {
	as := make([]string, 0, len(aliases))
	for _, a := range aliases {
		as = append(as, a.Alias)
	}
	return Character{
		Id: c.ID, BookId: c.BookID, Name: c.Name, Aliases: as,
		Gender: Gender(c.Gender), AgeGroup: AgeGroup(c.AgeGroup), Role: c.Role,
		Description: c.Description, SpeechStyle: c.SpeechStyle, Importance: c.Importance,
		Speaks: c.Speaks, ChapterCount: c.ChapterCount, FirstChapterIdx: c.FirstChapterIdx,
		UserEdited: c.UserEdited, Tier: Tier(cmp.Or(c.Tier, store.TierMinor)), TierManual: c.TierManual,
		Voice: v.Recipe, VoiceSpeed: float32(v.Speed), VoiceSource: CharacterVoiceSource(v.Source),
		VoiceManual: c.VoiceManual, VoiceNote: c.VoiceNote,
	}
}

func (s *Server) characterDetail(ctx context.Context, id int64) (CharacterDetail, error) {
	c, err := s.Store.GetCharacter(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	as, err := s.Store.CharacterAliases(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	chs, err := s.Store.CharacterChapters(ctx, id)
	if err != nil {
		return CharacterDetail{}, err
	}
	voices, err := s.characterVoices(ctx, c.BookID)
	if err != nil {
		return CharacterDetail{}, err
	}
	base := toCharacter(c, as[id], voices(c))
	out := CharacterDetail{
		Id: base.Id, BookId: base.BookId, Name: base.Name, Aliases: base.Aliases, Gender: base.Gender,
		AgeGroup: base.AgeGroup, Role: base.Role, Description: base.Description, SpeechStyle: base.SpeechStyle,
		Importance: base.Importance, Speaks: base.Speaks, ChapterCount: base.ChapterCount,
		FirstChapterIdx: base.FirstChapterIdx, UserEdited: base.UserEdited,
		Tier: base.Tier, TierManual: base.TierManual, Voice: base.Voice, VoiceSpeed: base.VoiceSpeed,
		VoiceSource: CharacterDetailVoiceSource(base.VoiceSource), VoiceManual: base.VoiceManual,
		VoiceNote: base.VoiceNote, Chapters: make([]ChapterRef, 0, len(chs)),
	}
	for _, ch := range chs {
		out.Chapters = append(out.Chapters, ChapterRef{Id: ch.ID, Idx: ch.Idx, Title: ch.Title})
	}
	return out, nil
}

// ListCharacters returns a page of a book's characters.
func (s *Server) ListCharacters(ctx context.Context, req ListCharactersRequestObject) (ListCharactersResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return ListCharacters404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit, 50, 500)
	f := store.CharacterFilter{}
	if req.Params.Search != nil {
		f.Search = strings.TrimSpace(*req.Params.Search)
	}
	if req.Params.Sort != nil {
		f.Sort = string(*req.Params.Sort)
	}
	if req.Params.Tier != nil {
		if !req.Params.Tier.Valid() {
			return ListCharacters400JSONResponse{ErrorJSONResponse(errMsg("tier must be main or minor"))}, nil
		}
		f.Tier = store.Tier(*req.Params.Tier)
	}
	cs, total, err := s.Store.ListCharacters(ctx, req.BookId, f, offset, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(cs))
	for i := range cs {
		ids[i] = cs[i].ID
	}
	as, err := s.Store.CharacterAliases(ctx, ids...)
	if err != nil {
		return nil, err
	}
	voices := s.bookVoices(ctx, b)
	out := CharacterPage{Items: make([]Character, 0, len(cs)), Total: total}
	for i := range cs {
		out.Items = append(out.Items, toCharacter(&cs[i], as[cs[i].ID], voices(&cs[i])))
	}
	return ListCharacters200JSONResponse(out), nil
}

// GetCharacter returns a character with aliases and chapters.
func (s *Server) GetCharacter(ctx context.Context, req GetCharacterRequestObject) (GetCharacterResponseObject, error) {
	out, err := s.characterDetail(ctx, req.CharacterId)
	if isNotFound(err) {
		return GetCharacter404JSONResponse{ErrorJSONResponse(errMsg("character not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	return GetCharacter200JSONResponse(out), nil
}

// characterBook loads a character and tells whether its book's analysis
// is active.
func (s *Server) characterBook(ctx context.Context, id int64) (*store.Character, bool, error) {
	c, err := s.Store.GetCharacter(ctx, id)
	if err != nil {
		return nil, false, err
	}
	b, err := s.Store.GetBook(ctx, c.BookID)
	if err != nil {
		return nil, false, err
	}
	busy, err := s.Analyzer.Busy(ctx, b)
	return c, busy, err
}

// UpdateCharacter corrects a character's profile or aliases.
func (s *Server) UpdateCharacter(ctx context.Context, req UpdateCharacterRequestObject) (UpdateCharacterResponseObject, error) {
	c, busy, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return UpdateCharacter404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	u := req.Body
	// Analysis batches do not touch voices, so they may change while it
	// runs (its duplicate merges keep the voice of the merged character).
	voiceOnly := u.Name == nil && u.Aliases == nil && u.Gender == nil && u.AgeGroup == nil && u.Role == nil &&
		u.Description == nil && u.SpeechStyle == nil && u.Importance == nil
	if busy && !voiceOnly {
		// A running batch would overwrite the correction with its snapshot.
		return UpdateCharacter409JSONResponse{Message: analysisBusyMsg}, nil
	}
	if msg := validateCharacterUpdate(req.Body); msg != "" {
		return UpdateCharacter400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	fields := map[string]any{}
	if !voiceOnly {
		fields["UserEdited"] = true
	}
	if msg := voiceFields(c, u, fields); msg != "" {
		return UpdateCharacter400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	if u.Name != nil {
		name := strings.TrimSpace(*u.Name)
		if store.NormName(name) == "" {
			return UpdateCharacter400JSONResponse{ErrorJSONResponse(errMsg("name must contain letters or digits"))}, nil
		}
		fields["Name"] = name
	}
	if u.Gender != nil {
		fields["Gender"] = store.Gender(*u.Gender)
	}
	if u.AgeGroup != nil {
		fields["AgeGroup"] = store.AgeGroup(*u.AgeGroup)
	}
	if u.Role != nil {
		fields["Role"] = strings.TrimSpace(*u.Role)
	}
	if u.Description != nil {
		fields["Description"] = strings.TrimSpace(*u.Description)
	}
	if u.SpeechStyle != nil {
		fields["SpeechStyle"] = strings.TrimSpace(*u.SpeechStyle)
	}
	if u.Importance != nil {
		fields["Importance"] = *u.Importance
	}
	oldName := c.Name
	if u.Name != nil {
		c.Name = strings.TrimSpace(*u.Name)
	}
	err = s.Store.Tx(ctx, func(tx *store.Store) error {
		if err := tx.UpdateCharacter(ctx, c.ID, fields); err != nil {
			return err
		}
		if u.Aliases != nil {
			if err := tx.SetCharacterAliases(ctx, c, *u.Aliases); err != nil {
				return err
			}
		}
		// The old name stays known, so the analysis still recognises it.
		_, err := tx.AddCharacterAliases(ctx, c, oldName)
		return err
	})
	if isNotFound(err) {
		// Merged away by the analysis meanwhile.
		return UpdateCharacter404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	// The change is saved; a casting that cannot be queued is not its error.
	// A tier left to the casting again is ranked by it.
	auto := u.Tier != nil && *u.Tier == CharacterUpdateTierAuto
	if err := s.castIfNeeded(ctx, c.ID, auto); err != nil {
		s.Log.WarnContext(ctx, "queue casting", "character", c.ID, "err", err)
	}
	out, err := s.characterDetail(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return UpdateCharacter200JSONResponse(out), nil
}

// voiceFields adds the tier and voice changes of an update to fields; it
// returns a message for an update that cannot be applied.
func voiceFields(c *store.Character, u *CharacterUpdate, fields map[string]any) string {
	recipe, manual := c.VoiceRecipe, c.VoiceManual
	if u.Voice != nil {
		if v := strings.TrimSpace(*u.Voice); v == "" {
			// Back to the casting: a main character is cast again.
			recipe, manual = "", false
			fields["VoiceSpeed"] = 1.0
		} else {
			r, _ := casting.ParseRecipe(v)
			recipe, manual = r.String(), true
		}
		fields["VoiceNote"] = ""
	}
	if u.Tier != nil {
		switch *u.Tier {
		case CharacterUpdateTierAuto:
			fields["TierManual"] = false
		default:
			fields["Tier"], fields["TierManual"] = store.Tier(*u.Tier), true
			if store.Tier(*u.Tier) == store.TierMinor && !manual {
				recipe = "" // minor characters speak with pool voices
			}
		}
	}
	// A cast voice no longer fits a character whose gender changed.
	if u.Gender != nil && store.Gender(*u.Gender) != c.Gender && !manual {
		recipe = ""
	}
	if u.VoiceSpeed != nil {
		if recipe == "" {
			return "voiceSpeed needs a voice of the character's own"
		}
		fields["VoiceSpeed"], manual = float64(*u.VoiceSpeed), true
	}
	if recipe != c.VoiceRecipe {
		fields["VoiceRecipe"] = recipe
		if recipe == "" {
			fields["VoiceNote"] = ""
			if u.VoiceSpeed == nil {
				fields["VoiceSpeed"] = 1.0
			}
		}
	}
	if manual != c.VoiceManual {
		fields["VoiceManual"] = manual
	}
	return ""
}

// castIfNeeded starts the casting for a main character without a voice
// (promoted, or its voice reset), once the analysis has finished.
func (s *Server) castIfNeeded(ctx context.Context, id int64, always bool) error {
	c, err := s.Store.GetCharacter(ctx, id)
	if err != nil {
		return err
	}
	if !always && (c.Tier != store.TierMain || c.VoiceRecipe != "") {
		return nil
	}
	b, err := s.Store.GetBook(ctx, c.BookID)
	if err != nil {
		return err
	}
	if err := s.Caster.Start(ctx, b, false); err != nil && !errors.Is(err, casting.ErrNotReady) {
		return err
	}
	return nil
}

// DeleteCharacter removes a character.
func (s *Server) DeleteCharacter(ctx context.Context, req DeleteCharacterRequestObject) (DeleteCharacterResponseObject, error) {
	c, busy, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return DeleteCharacter404JSONResponse{ErrorJSONResponse(errMsg("character not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if busy {
		return DeleteCharacter409JSONResponse{Message: analysisBusyMsg}, nil
	}
	if err := s.Store.DeleteCharacter(ctx, c.ID); err != nil {
		return nil, err
	}
	return DeleteCharacter204Response{}, nil
}

// MergeCharacters folds characters into one.
func (s *Server) MergeCharacters(ctx context.Context, req MergeCharactersRequestObject) (MergeCharactersResponseObject, error) {
	b, err := s.Store.GetBook(ctx, req.BookId)
	if isNotFound(err) {
		return MergeCharacters404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	if busy, err := s.Analyzer.Busy(ctx, b); err != nil {
		return nil, err
	} else if busy {
		return MergeCharacters409JSONResponse{Message: analysisBusyMsg}, nil
	}
	if len(req.Body.SourceIds) == 0 || len(req.Body.SourceIds) > 100 {
		return MergeCharacters400JSONResponse{ErrorJSONResponse(errMsg("sourceIds needs 1 to 100 characters"))}, nil
	}
	target, err := s.Store.GetCharacter(ctx, req.Body.TargetId)
	if isNotFound(err) || (err == nil && target.BookID != b.ID) {
		return MergeCharacters404JSONResponse{Message: "character not found in this book"}, nil
	} else if err != nil {
		return nil, err
	}
	_, err = s.Store.MergeCharacters(ctx, target.ID, req.Body.SourceIds...)
	if errors.Is(err, store.ErrInvalid) {
		return MergeCharacters400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.UpdateCharacter(ctx, target.ID, map[string]any{"UserEdited": true}); err != nil {
		return nil, err
	}
	// The merge may have dropped a voice that no longer fits.
	if err := s.castIfNeeded(ctx, target.ID, false); err != nil {
		s.Log.WarnContext(ctx, "queue casting", "character", target.ID, "err", err)
	}
	out, err := s.characterDetail(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	return MergeCharacters200JSONResponse(out), nil
}

// SplitCharacter moves aliases to a new character.
func (s *Server) SplitCharacter(ctx context.Context, req SplitCharacterRequestObject) (SplitCharacterResponseObject, error) {
	c, busy, err := s.characterBook(ctx, req.CharacterId)
	if isNotFound(err) {
		return SplitCharacter404JSONResponse{Message: "character not found"}, nil
	} else if err != nil {
		return nil, err
	}
	if busy {
		return SplitCharacter409JSONResponse{Message: analysisBusyMsg}, nil
	}
	var aliases []string
	if req.Body.Aliases != nil {
		aliases = *req.Body.Aliases
	}
	if utf8.RuneCountInString(req.Body.Name) > 120 || len(aliases) > 200 {
		return SplitCharacter400JSONResponse{ErrorJSONResponse(errMsg("name or aliases too long"))}, nil
	}
	created, err := s.Store.SplitCharacter(ctx, c.ID, req.Body.Name, aliases)
	if errors.Is(err, store.ErrInvalid) {
		return SplitCharacter400JSONResponse{ErrorJSONResponse(errMsg(err.Error()))}, nil
	} else if err != nil {
		return nil, err
	}
	out, err := s.characterDetail(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	return SplitCharacter201JSONResponse(out), nil
}
