package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TransitionPrep moves a chapter's preparation to status to (with extra
// fields) if it is in one of the states from; it reports whether it did.
func (s *Store) TransitionPrep(ctx context.Context, chapterID int64, to PrepStatus, fields map[string]any,
	from ...PrepStatus,
) (bool, error) {
	upd := map[string]any{"prep_status": to}
	for k, v := range fields {
		upd[k] = v
	}
	res := s.db.WithContext(ctx).Model(&Chapter{}).Where("id = ? AND prep_status IN ?", chapterID, from).Updates(upd)
	return res.RowsAffected == 1, res.Error
}

// SpokenForm is how a range [Start, End) of a segment's text (runes) is
// read aloud ("Lv. 5" → "level five").
type SpokenForm struct {
	Start int    `json:"a"`
	End   int    `json:"b"`
	Say   string `json:"t"`
}

// Pause is an extra pause in a segment before rune Offset (len(text):
// after the segment).
type Pause struct {
	Offset int
	Ms     int
}

// MaxPauseMs bounds a pause of the preparation.
const MaxPauseMs = 3000

// SegmentPrep is the preparation of one segment.
type SegmentPrep struct {
	SegmentID int64
	Pauses    []Pause
	SpokenAs  []SpokenForm
}

// Preparation is what one part of a chapter's preparation found.
type Preparation struct {
	// Moods of narration by paragraph id.
	Moods map[int64]string
	// Flows lists the paragraphs that go on from the one before.
	Flows    []int64
	Segments []SegmentPrep
	// Suggestions for the book's lexicon (Term, Replacement, Note).
	Suggestions []LexiconEntry
}

// ErrStalePrep reports that a running preparation was reset, or the
// chapter restructured, while it waited for an answer.
var ErrStalePrep = errors.New("the preparation was reset meanwhile")

// ApplyPreparation stores what a part of a chapter's preparation found and
// records that the paragraphs before doneParas are prepared — only if the
// preparation still runs and is at fromParas (else ErrStalePrep, nothing
// is written). Suggested terms the lexicon has (in any state) are left
// out. It bumps the chapter's voice revision.
func (s *Store) ApplyPreparation(ctx context.Context, chapterID int64, p Preparation, fromParas, doneParas int) error {
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		var ch Chapter
		if err := db.Select("id, book_id").Take(&ch, chapterID).Error; err != nil {
			return notFound(err)
		}
		res := db.Model(&Chapter{}).
			Where("id = ? AND prep_status = ? AND prep_part = ?", chapterID, PrepRunning, fromParas).
			Updates(map[string]any{"prep_part": doneParas, "voice_rev": gorm.Expr("voice_rev + 1")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrStalePrep
		}
		for id, mood := range p.Moods {
			if err := db.Model(&Paragraph{}).Where("id = ? AND chapter_id = ?", id, chapterID).
				Update("mood", mood).Error; err != nil {
				return err
			}
		}
		if len(p.Flows) > 0 {
			if err := db.Model(&Paragraph{}).Where("id IN ? AND chapter_id = ?", p.Flows, chapterID).
				Update("flows", true).Error; err != nil {
				return err
			}
		}
		for _, sp := range p.Segments {
			if err := db.Model(&Segment{}).Where("id = ? AND chapter_id = ?", sp.SegmentID, chapterID).
				Updates(map[string]any{"pauses": EncodePauses(sp.Pauses), "spoken_as": EncodeSpokenAs(sp.SpokenAs)}).
				Error; err != nil {
				return err
			}
		}
		for _, e := range p.Suggestions {
			taken, err := tx.LexiconTermTaken(ctx, ch.BookID, 0, e.Term)
			if err != nil {
				return err
			}
			if taken {
				continue
			}
			e.ID, e.BookID, e.Status = 0, ch.BookID, LexiconSuggested
			if err := db.Create(&e).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ResetPreparation forgets what the preparation of chapters found (the
// lexicon keeps its suggestions) so they are prepared again.
func (s *Store) ResetPreparation(ctx context.Context, chapterIDs ...int64) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	return s.Tx(ctx, func(tx *Store) error {
		if err := tx.ClearPreparation(ctx, chapterIDs...); err != nil {
			return err
		}
		return tx.db.Model(&Chapter{}).Where("id IN ?", chapterIDs).Updates(map[string]any{
			"prep_status": PrepNone, "prep_error": "", "prep_part": 0, "prepared_at": nil,
			"voice_rev": gorm.Expr("voice_rev + 1"),
		}).Error
	})
}

// ClearPreparation removes the moods, pauses and spoken forms of chapters
// (left over from an earlier preparation when one starts from scratch: a
// merged, split or restored chapter keeps them).
func (s *Store) ClearPreparation(ctx context.Context, chapterIDs ...int64) error {
	db := s.db.WithContext(ctx)
	if err := db.Model(&Paragraph{}).Where("chapter_id IN ? AND (mood <> '' OR flows = ?)", chapterIDs, true).
		Updates(map[string]any{"mood": "", "flows": false}).Error; err != nil {
		return err
	}
	return db.Model(&Segment{}).Where("chapter_id IN ? AND (pauses <> '' OR spoken_as <> '')", chapterIDs).
		Updates(map[string]any{"pauses": "", "spoken_as": ""}).Error
}

// MarkPrepared finishes a running preparation.
func (s *Store) MarkPrepared(ctx context.Context, chapterID int64, prompt string) (bool, error) {
	return s.TransitionPrep(ctx, chapterID, PrepDone, map[string]any{
		"prepared_at": time.Now().UTC(), "prep_error": "", "prep_prompt": prompt,
	}, PrepRunning)
}

// PrepCounts sums up what a chapter's preparation found.
type PrepCounts struct {
	Moods, Pauses, SpokenAs, Flows int
}

// PrepStats counts the moods, pauses, spoken forms and flowing paragraphs
// of a chapter.
func (s *Store) PrepStats(ctx context.Context, chapterID int64) (PrepCounts, error) {
	db := s.db.WithContext(ctx)
	var out PrepCounts
	var moods int64
	if err := db.Model(&Paragraph{}).Where("chapter_id = ? AND mood <> ''", chapterID).Count(&moods).Error; err != nil {
		return out, err
	}
	out.Moods = int(moods)
	var flows int64
	if err := db.Model(&Paragraph{}).Where("chapter_id = ? AND flows = ?", chapterID, true).Count(&flows).Error; err != nil {
		return out, err
	}
	out.Flows = int(flows)
	var segs []Segment
	if err := db.Select("id, text, pauses, spoken_as").
		Where("chapter_id = ? AND (pauses <> '' OR spoken_as <> '')", chapterID).Find(&segs).Error; err != nil {
		return out, err
	}
	for _, sg := range segs {
		n := len([]rune(sg.Text))
		out.Pauses += len(DecodePauses(sg.Pauses, n))
		out.SpokenAs += len(DecodeSpokenAs(sg.SpokenAs, n))
	}
	return out, nil
}

// EncodePauses stores pauses as JSON [[offset, ms],…] ("" for none).
func EncodePauses(ps []Pause) string {
	if len(ps) == 0 {
		return ""
	}
	out := make([][2]int, len(ps))
	for i, p := range ps {
		out[i] = [2]int{p.Offset, p.Ms}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

// DecodePauses reads pauses stored by EncodePauses for a text of n runes;
// malformed ones are dropped.
func DecodePauses(s string, n int) []Pause {
	if s == "" {
		return nil
	}
	var raw [][2]int
	if json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	var out []Pause
	for _, r := range raw {
		if r[0] > 0 && r[0] <= n && r[1] > 0 && r[1] <= MaxPauseMs {
			out = append(out, Pause{r[0], r[1]})
		}
	}
	return out
}

// EncodeSpokenAs stores spoken forms as JSON ("" for none).
func EncodeSpokenAs(fs []SpokenForm) string {
	if len(fs) == 0 {
		return ""
	}
	data, err := json.Marshal(fs)
	if err != nil {
		return ""
	}
	return string(data)
}

// MaxSpokenRunes bounds the text of a spoken form.
const MaxSpokenRunes = 200

// DecodeSpokenAs reads spoken forms stored by EncodeSpokenAs for a text of
// n runes; malformed or overlapping ones are dropped.
func DecodeSpokenAs(s string, n int) []SpokenForm {
	if s == "" {
		return nil
	}
	var raw []SpokenForm
	if json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	var out []SpokenForm
	end := 0
	for _, f := range raw {
		say := strings.TrimSpace(f.Say)
		if f.Start < end || f.Start >= f.End || f.End > n || say == "" || len([]rune(say)) > MaxSpokenRunes {
			continue
		}
		f.Say = say
		out = append(out, f)
		end = f.End
	}
	return out
}
