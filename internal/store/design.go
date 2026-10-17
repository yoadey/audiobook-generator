package store

import (
	"context"
	"strconv"
	"time"
)

// TransitionDesign moves a character's voice design to status to (with
// more fields) if it is in one of the from states; it reports whether it
// did.
func (s *Store) TransitionDesign(ctx context.Context, characterID int64, to DesignStatus, fields map[string]any,
	from ...DesignStatus,
) (bool, error) {
	upd := map[string]any{"design_status": to}
	for k, v := range fields {
		upd[k] = v
	}
	res := s.db.WithContext(ctx).Model(&Character{}).
		Where("id = ? AND design_status IN ?", characterID, from).Updates(upd)
	return res.RowsAffected == 1, res.Error
}

// AssignDesignedVoice gives a character the voice designed for it (as
// one the user chose) and marks the design done, if it is still running;
// the chapters it speaks in get the new voice.
func (s *Store) AssignDesignedVoice(ctx context.Context, characterID int64, voice, note string) (bool, error) {
	ok := false
	err := s.Tx(ctx, func(tx *Store) error {
		res := tx.db.Model(&Character{}).Where("id = ? AND design_status = ?", characterID, DesignRunning).
			Updates(map[string]any{
				"design_status": DesignDone, "design_error": "", "voice_recipe": voice, "voice_speed": 1,
				"voice_manual": true, "voice_note": note,
			})
		if res.Error != nil || res.RowsAffected != 1 {
			return res.Error
		}
		ok = true
		return tx.TouchCharacterVoices(ctx, characterID)
	})
	return ok, err
}

// PendingJobsOfType counts the queued and running jobs of a type.
func (s *Store) PendingJobsOfType(ctx context.Context, jobType string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Job{}).
		Where("type = ? AND status IN ?", jobType, []JobStatus{JobQueued, JobRunning}).Count(&n).Error
	return n, err
}

// SettleDesigns runs at startup: without voice design (enabled false) its
// pending jobs are canceled; characters left queued or running without a
// pending job (canceled, lost, design turned off) are marked failed so
// they can be designed again.
func (s *Store) SettleDesigns(ctx context.Context, jobType string, enabled bool) error {
	return s.Tx(ctx, func(tx *Store) error {
		pending := tx.db.Model(&Job{}).Where("type = ? AND status IN ?", jobType, []JobStatus{JobQueued, JobRunning})
		if !enabled {
			if err := pending.Updates(map[string]any{"status": JobCanceled, "finished_at": time.Now().UTC()}).
				Error; err != nil {
				return err
			}
		}
		var payloads []string
		if err := tx.db.Model(&Job{}).Where("type = ? AND status IN ?", jobType, []JobStatus{JobQueued, JobRunning}).
			Pluck("payload", &payloads).Error; err != nil {
			return err
		}
		live := make(map[int64]bool, len(payloads))
		for _, p := range payloads {
			if id, err := strconv.ParseInt(p, 10, 64); err == nil {
				live[id] = true
			}
		}
		var busy []int64
		if err := tx.db.Model(&Character{}).Where("design_status IN ?", []DesignStatus{DesignQueued, DesignRunning}).
			Pluck("id", &busy).Error; err != nil {
			return err
		}
		var orphans []int64
		for _, id := range busy {
			if !live[id] {
				orphans = append(orphans, id)
			}
		}
		if len(orphans) == 0 {
			return nil
		}
		msg := "interrupted: design again"
		if !enabled {
			msg = "voice design is no longer configured"
		}
		return tx.db.Model(&Character{}).Where("id IN ?", orphans).
			Updates(map[string]any{"design_status": DesignFailed, "design_error": msg}).Error
	})
}
