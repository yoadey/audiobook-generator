package store

import "context"

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
