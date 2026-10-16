package api

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/store"
)

func toLexiconEntry(e *store.LexiconEntry) LexiconEntry {
	return LexiconEntry{Id: e.ID, BookId: e.BookID, Term: e.Term, Replacement: e.Replacement, Phonemes: e.IsPhonemes}
}

// validateLexicon checks an entry; phonemes go into Kokoro's
// "[word](/phonemes/)" notation, so they must not contain its brackets,
// and a text replacement must not start one.
func validateLexicon(e *store.LexiconEntry) string {
	e.Term, e.Replacement = strings.Join(strings.Fields(e.Term), " "), strings.TrimSpace(e.Replacement)
	switch {
	case e.Term == "" || utf8.RuneCountInString(e.Term) > 100:
		return "term must have 1 to 100 characters"
	case strings.IndexFunc(e.Term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0:
		return "term must contain letters or digits"
	case e.Replacement == "" || utf8.RuneCountInString(e.Replacement) > 200:
		return "replacement must have 1 to 200 characters"
	case e.IsPhonemes && strings.ContainsAny(e.Replacement, "[]()/"):
		return "phonemes must not contain [ ] ( ) or /"
	case !e.IsPhonemes && strings.ContainsAny(e.Replacement, "[]"):
		return "replacement must not contain [ or ]"
	}
	return ""
}

// ListLexicon returns a book's pronunciations.
func (s *Server) ListLexicon(ctx context.Context, req ListLexiconRequestObject) (ListLexiconResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return ListLexicon404JSONResponse{ErrorJSONResponse(errMsg("book not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	es, err := s.Store.ListLexicon(ctx, req.BookId)
	if err != nil {
		return nil, err
	}
	out := make([]LexiconEntry, 0, len(es))
	for i := range es {
		out = append(out, toLexiconEntry(&es[i]))
	}
	return ListLexicon200JSONResponse(out), nil
}

// CreateLexiconEntry adds a pronunciation.
func (s *Server) CreateLexiconEntry(ctx context.Context, req CreateLexiconEntryRequestObject) (CreateLexiconEntryResponseObject, error) {
	if _, err := s.Store.GetBook(ctx, req.BookId); isNotFound(err) {
		return CreateLexiconEntry404JSONResponse{Message: "book not found"}, nil
	} else if err != nil {
		return nil, err
	}
	e := &store.LexiconEntry{BookID: req.BookId, Term: req.Body.Term, Replacement: req.Body.Replacement,
		IsPhonemes: req.Body.Phonemes != nil && *req.Body.Phonemes}
	if msg := validateLexicon(e); msg != "" {
		return CreateLexiconEntry400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	if err := s.Store.SaveLexiconEntry(ctx, e); errors.Is(err, store.ErrConflict) {
		return CreateLexiconEntry409JSONResponse{Message: err.Error()}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.TouchTermChapters(ctx, e.BookID, e.Term); err != nil {
		return nil, err
	}
	return CreateLexiconEntry201JSONResponse(toLexiconEntry(e)), nil
}

// UpdateLexiconEntry changes a pronunciation.
func (s *Server) UpdateLexiconEntry(ctx context.Context, req UpdateLexiconEntryRequestObject) (UpdateLexiconEntryResponseObject, error) {
	e, err := s.Store.GetLexiconEntry(ctx, req.EntryId)
	if isNotFound(err) {
		return UpdateLexiconEntry404JSONResponse{Message: "entry not found"}, nil
	} else if err != nil {
		return nil, err
	}
	oldTerm := e.Term
	if req.Body.Term != nil {
		e.Term = *req.Body.Term
	}
	if req.Body.Replacement != nil {
		e.Replacement = *req.Body.Replacement
	}
	if req.Body.Phonemes != nil {
		e.IsPhonemes = *req.Body.Phonemes
	}
	if msg := validateLexicon(e); msg != "" {
		return UpdateLexiconEntry400JSONResponse{ErrorJSONResponse(errMsg(msg))}, nil
	}
	if err := s.Store.SaveLexiconEntry(ctx, e); errors.Is(err, store.ErrConflict) {
		return UpdateLexiconEntry409JSONResponse{Message: err.Error()}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.TouchTermChapters(ctx, e.BookID, oldTerm, e.Term); err != nil {
		return nil, err
	}
	return UpdateLexiconEntry200JSONResponse(toLexiconEntry(e)), nil
}

// DeleteLexiconEntry removes a pronunciation.
func (s *Server) DeleteLexiconEntry(ctx context.Context, req DeleteLexiconEntryRequestObject) (DeleteLexiconEntryResponseObject, error) {
	e, err := s.Store.GetLexiconEntry(ctx, req.EntryId)
	if isNotFound(err) {
		return DeleteLexiconEntry404JSONResponse{ErrorJSONResponse(errMsg("entry not found"))}, nil
	} else if err != nil {
		return nil, err
	}
	if err := s.Store.DeleteLexiconEntry(ctx, e.ID); err != nil && !isNotFound(err) {
		return nil, err
	}
	if err := s.Store.TouchTermChapters(ctx, e.BookID, e.Term); err != nil {
		return nil, err
	}
	return DeleteLexiconEntry204Response{}, nil
}
