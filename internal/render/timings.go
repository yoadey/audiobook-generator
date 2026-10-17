package render

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/audio"
	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// Cue is when a segment is spoken in a chapter's audio. SegmentID 0 is the
// chapter title read out before a chapter without a heading.
type Cue struct {
	SegmentID int64 `json:"s"`
	StartMs   int64 `json:"a"`
	EndMs     int64 `json:"b"`
	// Words are when the segment's words start: [rune offset in its text,
	// ms], for the words the TTS reported (none from other servers).
	Words [][2]int64 `json:"w,omitempty"`
}

// Timings are the cues of one render, stored next to its audio file.
type Timings struct {
	Cues []Cue `json:"cues"`
}

// TimingsPath is the timings file of an audio file (relative paths).
func TimingsPath(audioPath string) string {
	return strings.TrimSuffix(audioPath, ".m4a") + ".json"
}

// ReadTimings loads the timings of an audio file (relative to dataDir);
// os.ErrNotExist when the render has none (made before timings existed).
func ReadTimings(dataDir, audioPath string) (*Timings, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, TimingsPath(audioPath))) //nolint:gosec // data dir + stored path
	if err != nil {
		return nil, err
	}
	var t Timings
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// RemoveAudio deletes an audio file and its timings (relative to dataDir);
// files already gone are no error.
func RemoveAudio(dataDir, audioPath string) error {
	var errs []error
	for _, p := range []string{audioPath, TimingsPath(audioPath)} {
		if err := os.Remove(filepath.Join(dataDir, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RemoveVersions deletes every version of a chapter's audio and its
// timings (relative audioPath of any version), e.g. when its text changed.
func RemoveVersions(dataDir, audioPath string) error {
	chapter, _, ok := strings.Cut(filepath.Base(audioPath), "-")
	if !ok {
		return RemoveAudio(dataDir, audioPath) // audio from before versioned files
	}
	files, err := filepath.Glob(filepath.Join(dataDir, filepath.Dir(audioPath), chapter+"-*"))
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cues lists when each segment is spoken, given the PCM length of each
// item in bytes, in the layout writePCM produces, and the words the TTS
// reported per item; textOf is the text of a segment (0: the title).
func cues(items []item, pieceBytes []int64, words [][]tts.Word, textOf func(int64) string) []Cue {
	out := make([]Cue, 0, len(items))
	var spoken []timedWord
	flush := func() {
		if n := len(out); n > 0 && len(spoken) > 0 {
			out[n-1].Words = alignWords(textOf(out[n-1].SegmentID), spoken)
		}
		spoken = spoken[:0]
	}
	at := leadIn
	for i, it := range items {
		d := pcmDuration(pieceBytes[i])
		// The phrases of a segment are one cue.
		if n := len(out); n > 0 && out[n-1].SegmentID == it.segmentID && items[i-1].gap == gapPhrase {
			out[n-1].EndMs = (at + d).Milliseconds()
		} else {
			flush()
			out = append(out, Cue{SegmentID: it.segmentID, StartMs: at.Milliseconds(), EndMs: (at + d).Milliseconds()})
		}
		if i < len(words) {
			for _, w := range words[i] {
				ms := at.Milliseconds() + int64(w.Start*1000)
				spoken = append(spoken, timedWord{w.Text, min(ms, (at + d).Milliseconds())})
			}
		}
		at += d + it.pause
	}
	flush()
	return out
}

func pcmDuration(n int64) time.Duration {
	// 16-bit mono at the TTS sample rate.
	return time.Duration(n&^1) * time.Second / time.Duration(2*audio.SampleRate)
}

// EstimateTimings spreads a chapter's audio duration over its segments by
// their length, for renders made before timings were recorded. Pauses are
// known exactly; only the speech in between is estimated.
func EstimateTimings(ch *store.Chapter, paras []store.Paragraph, segs []store.Segment, duration time.Duration) *Timings {
	// Such renders had no prepared pauses.
	withoutPreparation(paras, segs)
	items := plan(ch, paras, segs, func(*store.Segment) (string, float64) { return "", 1 }, lexicon.Compile(nil))
	if len(items) == 0 {
		return &Timings{Cues: []Cue{}}
	}
	pauses := leadIn
	chars := 0
	for _, it := range items {
		pauses += it.pause
		chars += utf8.RuneCountInString(it.text)
	}
	speech := duration - pauses
	scale := 1.0
	if speech <= 0 {
		// Shorter than its pauses (should not happen): shrink everything.
		speech = duration / 2
		scale = float64(duration-speech) / float64(pauses)
	}
	out := make([]Cue, 0, len(items))
	at := time.Duration(float64(leadIn) * scale)
	for i, it := range items {
		d := time.Duration(float64(speech) * float64(utf8.RuneCountInString(it.text)) / float64(max(chars, 1)))
		if n := len(out); n > 0 && out[n-1].SegmentID == it.segmentID && items[i-1].gap == gapPhrase {
			out[n-1].EndMs = (at + d).Milliseconds()
		} else {
			out = append(out, Cue{SegmentID: it.segmentID, StartMs: at.Milliseconds(), EndMs: (at + d).Milliseconds()})
		}
		at += d + time.Duration(float64(it.pause)*scale)
	}
	return &Timings{Cues: out}
}

var versionRe = regexp.MustCompile(`^[0-9a-z]{1,20}$`)

// KeptPath is the audio file of a chapter's render version while it is
// kept, else the current one (relative paths).
func KeptPath(dataDir string, c *store.Chapter, version *string) string {
	if v := version; v != nil && versionRe.MatchString(*v) && *v != AudioVersion(c.AudioPath) {
		old := VersionPath(c.BookID, c.ID, *v)
		if _, err := os.Stat(filepath.Join(dataDir, old)); err == nil {
			return old
		}
	}
	return c.AudioPath
}

// LoadTimings loads the timings of a chapter's audio: of version while it
// is kept, else of the current render (path is the audio they belong to).
// A render without recorded timings gets an estimate (exact false).
func LoadTimings(ctx context.Context, st *store.Store, dataDir string, c *store.Chapter, version *string) (
	t *Timings, path string, exact bool, err error,
) {
	path = KeptPath(dataDir, c, version)
	t, err = ReadTimings(dataDir, path)
	if err == nil {
		return t, path, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", false, err
	}
	if path != c.AudioPath {
		// An older render without timings: its duration is unknown, so
		// the current render's timings (path tells which they are).
		path = c.AudioPath
		if t, err = ReadTimings(dataDir, path); err == nil {
			return t, path, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", false, err
		}
	}
	paras, segs, err := st.ChapterText(ctx, c.ID)
	if err != nil {
		return nil, "", false, err
	}
	return EstimateTimings(c, paras, segs, time.Duration(c.AudioDurationMs)*time.Millisecond), path, false, nil
}
