package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// ScriptSegment is a piece of a paragraph as the speaker attribution
// splits it: narration, or a piece of a voiced span (Quote > 0; the
// pieces of one span share it, numbered within the ScriptParagraph).
type ScriptSegment struct {
	Kind     SegmentKind
	Voicing  string
	Text     string
	Emphasis string
	Quote    int
	// Speaker (nil: the narrator reads it), how it is said and how sure
	// the attribution is.
	CharacterID *int64
	Emotion     string
	Direction   string
	Confidence  float64
}

// ScriptParagraph is a paragraph split anew by the speaker attribution,
// with how the narrator reads it.
type ScriptParagraph struct {
	ParagraphID int64
	Direction   string
	Segments    []ScriptSegment
}

// ApplyScript stores a part of a speaker attribution that splits the
// paragraphs itself: each listed paragraph gets the given segments in
// place of its own, unless the user chose a speaker in it (then it is
// kept as it is). Segments are numbered anew in reading order, quotations
// too (the segments kept keep their IDs, so timings and listening
// positions stay valid). Like ApplyAttribution it records that the
// paragraphs before doneParas are attributed, only while the attribution
// still runs and is at fromParas (else ErrStale, nothing is written), and
// bumps the voice revision. A preparation that already marked replaced
// segments (pauses, spoken forms) is reset: its marks referred to them.
func (s *Store) ApplyScript(ctx context.Context, chapterID int64, ps []ScriptParagraph, fromParas, doneParas int,
) error {
	return s.Tx(ctx, func(tx *Store) error {
		db := tx.db
		res := db.Model(&Chapter{}).
			Where("id = ? AND attribution_status = ? AND attribution_part = ?", chapterID, AttributionRunning, fromParas).
			Updates(map[string]any{"attribution_part": doneParas, "voice_rev": gorm.Expr("voice_rev + 1")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrStale
		}
		exist, err := existingSpeakers(db, chapterID, ps)
		if err != nil {
			return err
		}
		var paras []Paragraph
		if err := db.Where("chapter_id = ?", chapterID).Order("idx").Find(&paras).Error; err != nil {
			return err
		}
		var segs []Segment
		if err := db.Where("chapter_id = ?", chapterID).Order("idx").Find(&segs).Error; err != nil {
			return err
		}
		byPara := map[int64][]Segment{}
		manual := map[int64]bool{}
		for _, sg := range segs {
			byPara[sg.ParagraphID] = append(byPara[sg.ParagraphID], sg)
			manual[sg.ParagraphID] = manual[sg.ParagraphID] || sg.SpeakerManual
		}
		inChapter := map[int64]bool{}
		for _, p := range paras {
			inChapter[p.ID] = true
		}
		replace := map[int64]*ScriptParagraph{}
		for i := range ps {
			p := &ps[i]
			if !inChapter[p.ParagraphID] {
				return fmt.Errorf("paragraph %d is not in chapter %d: %w", p.ParagraphID, chapterID, ErrStale)
			}
			if err := db.Model(&Paragraph{ID: p.ParagraphID}).Update("direction", p.Direction).Error; err != nil {
				return err
			}
			if !manual[p.ParagraphID] {
				replace[p.ParagraphID] = p
			}
		}

		// The chapter's segments in reading order, with quotations
		// numbered by first appearance.
		var (
			kept      []Segment // moved only
			relabeled []Segment // split as before, labeled anew
			created   []Segment
			dropped   []int64
			prepLost  bool
		)
		refs := map[string]int{}
		ref := func(key string) int {
			if r, ok := refs[key]; ok {
				return r
			}
			refs[key] = len(refs) + 1
			return refs[key]
		}
		idx := 0
		for _, p := range paras {
			sp := replace[p.ID]
			if sp == nil {
				for _, sg := range byPara[p.ID] {
					old := sg
					sg.Idx = idx
					if sg.QuoteRef > 0 {
						sg.QuoteRef = ref(fmt.Sprintf("o%d", old.QuoteRef))
					}
					if sg.Idx != old.Idx || sg.QuoteRef != old.QuoteRef {
						kept = append(kept, sg)
					}
					idx++
				}
				continue
			}
			old := byPara[p.ID]
			if sameSplit(old, sp.Segments) {
				// Split as before: the segments keep their IDs (and marks).
				for k, ns := range sp.Segments {
					sg := old[k]
					sg.Idx, sg.QuoteRef = idx, 0
					sg.Voicing, sg.Emotion, sg.Direction, sg.Confidence, sg.SpeakerCharacterID = "", "", "", 0, nil
					if ns.Quote > 0 {
						sg.QuoteRef = ref(fmt.Sprintf("n%d-%d", p.ID, ns.Quote))
						label(&sg, ns, exist)
					}
					relabeled = append(relabeled, sg)
					idx++
				}
				continue
			}
			for _, sg := range old {
				dropped = append(dropped, sg.ID)
				prepLost = prepLost || sg.Pauses != "" || sg.SpokenAs != ""
			}
			for _, ns := range sp.Segments {
				sg := Segment{
					ChapterID: chapterID, ParagraphID: p.ID, Idx: idx, Kind: ns.Kind, Text: ns.Text,
					Emphasis: ns.Emphasis,
				}
				if ns.Quote > 0 {
					sg.QuoteRef = ref(fmt.Sprintf("n%d-%d", p.ID, ns.Quote))
					label(&sg, ns, exist)
				}
				created = append(created, sg)
				idx++
			}
		}
		if len(dropped) > 0 {
			if err := db.Where("id IN ?", dropped).Delete(&Segment{}).Error; err != nil {
				return err
			}
		}
		for _, sg := range relabeled {
			if err := db.Model(&Segment{ID: sg.ID}).Updates(map[string]any{
				"idx": sg.Idx, "quote_ref": sg.QuoteRef, "voicing": sg.Voicing, "emotion": sg.Emotion,
				"direction": sg.Direction, "confidence": sg.Confidence, "speaker_character_id": sg.SpeakerCharacterID,
			}).Error; err != nil {
				return err
			}
		}
		for _, sg := range kept {
			if err := db.Model(&Segment{ID: sg.ID}).
				Updates(map[string]any{"idx": sg.Idx, "quote_ref": sg.QuoteRef}).Error; err != nil {
				return err
			}
		}
		if len(created) > 0 {
			if err := db.CreateInBatches(created, 200).Error; err != nil {
				return err
			}
		}
		if prepLost {
			if err := tx.ClearPreparation(ctx, chapterID); err != nil {
				return err
			}
			if err := db.Model(&Chapter{}).Where("id = ? AND prep_status <> ?", chapterID, PrepNone).
				Updates(map[string]any{
					"prep_status": PrepNone, "prep_error": "", "prep_part": 0, "prepared_at": nil,
				}).Error; err != nil {
				return err
			}
		}
		return refreshChapterStats(db, chapterID)
	})
}

// sameSplit reports whether a paragraph is split as it was: the same
// pieces of the same kinds.
func sameSplit(old []Segment, segs []ScriptSegment) bool {
	if len(old) != len(segs) {
		return false
	}
	for i, sg := range segs {
		if old[i].Kind != sg.Kind || old[i].Text != sg.Text {
			return false
		}
	}
	return true
}

// label gives a voiced segment the labels of its passage; a speaker that
// no longer exists is dropped.
func label(sg *Segment, ns ScriptSegment, exist map[int64]bool) {
	sg.Voicing, sg.Emotion, sg.Direction, sg.Confidence = ns.Voicing, ns.Emotion, ns.Direction, ns.Confidence
	if ns.CharacterID != nil && exist[*ns.CharacterID] {
		id := *ns.CharacterID
		sg.SpeakerCharacterID = &id
	} else if ns.CharacterID != nil {
		sg.Confidence = 0 // deleted or merged meanwhile
	}
}

// existingSpeakers reports which speakers named in a script still exist
// in the chapter's book (not deleted or merged meanwhile).
func existingSpeakers(db *gorm.DB, chapterID int64, ps []ScriptParagraph) (map[int64]bool, error) {
	var ids []int64
	for _, p := range ps {
		for _, sg := range p.Segments {
			if sg.CharacterID != nil {
				ids = append(ids, *sg.CharacterID)
			}
		}
	}
	exist := map[int64]bool{}
	if len(ids) == 0 {
		return exist, nil
	}
	var found []int64
	if err := db.Model(&Character{}).Where("id IN ? AND book_id = (?)", ids,
		db.Session(&gorm.Session{NewDB: true}).Model(&Chapter{}).Select("book_id").Where("id = ?", chapterID)).
		Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	for _, id := range found {
		exist[id] = true
	}
	return exist, nil
}
