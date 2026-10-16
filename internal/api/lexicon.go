package api

import (
	"context"
	"errors"

	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/store"
)

func toLexiconEntry(e *store.LexiconEntry) LexiconEntry {
	return LexiconEntry{Id: e.ID, BookId: e.BookID, Term: e.Term, Replacement: e.Replacement, Phonemes: e.IsPhonemes}
}

// validateLexicon normalizes and checks an entry ("" when valid).
func validateLexicon(e *store.LexiconEntry) string {
	e.Term, e.Replacement = lexicon.Normalize(e.Term, e.Replacement)
	if err := lexicon.Validate(e.Term, e.Replacement, e.IsPhonemes); err != nil {
		return err.Error()
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
	if err := s.touchTerms(ctx, e.BookID, e.Term); err != nil {
		return nil, err
	}
	if err := s.Store.SaveLexiconEntry(ctx, e); errors.Is(err, store.ErrConflict) {
		return CreateLexiconEntry409JSONResponse{Message: err.Error()}, nil
	} else if err != nil {
		return nil, err
	}
	s.touchTermsAgain(ctx, e.BookID, e.Term)
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
	if err := s.touchTerms(ctx, e.BookID, oldTerm, e.Term); err != nil {
		return nil, err
	}
	if err := s.Store.SaveLexiconEntry(ctx, e); errors.Is(err, store.ErrConflict) {
		return UpdateLexiconEntry409JSONResponse{Message: err.Error()}, nil
	} else if err != nil {
		return nil, err
	}
	s.touchTermsAgain(ctx, e.BookID, oldTerm, e.Term)
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
	if err := s.touchTerms(ctx, e.BookID, e.Term); err != nil {
		return nil, err
	}
	if err := s.Store.DeleteLexiconEntry(ctx, e.ID); err != nil && !isNotFound(err) {
		return nil, err
	}
	s.touchTermsAgain(ctx, e.BookID, e.Term)
	return DeleteLexiconEntry204Response{}, nil
}

// touchTerms outdates the chapters that speak one of the terms. Changes
// call it before writing the entry (a failure changes nothing, so a retry
// works) and again after it (touchTermsAgain): a render that started in
// between used the old pronunciation with the new revision.
func (s *Server) touchTerms(ctx context.Context, bookID int64, terms ...string) error {
	var es []store.LexiconEntry
	for _, t := range terms {
		// A replacement no text contains, so any match changes the text.
		es = append(es, store.LexiconEntry{Term: t, Replacement: "\x00"})
	}
	l := lexicon.Compile(es)
	if l == nil {
		return nil
	}
	return s.Store.TouchMatchingChapters(ctx, bookID, l.Matches)
}

func (s *Server) touchTermsAgain(ctx context.Context, bookID int64, terms ...string) {
	if err := s.touchTerms(ctx, bookID, terms...); err != nil {
		s.Log.ErrorContext(ctx, "outdate chapters after a lexicon change", "book", bookID, "err", err)
	}
}
