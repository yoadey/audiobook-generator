package feed

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// serveTranscript serves a chapter's text as WebVTT, one cue per segment,
// timed like the render version (podcast apps show it along the audio).
func (h *Handler) serveTranscript(w http.ResponseWriter, r *http.Request, c *store.Chapter, version string) {
	t, _, _, err := render.LoadTimings(r.Context(), h.Store, h.DataDir, c, &version)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	_, segs, err := h.Store.ChapterText(r.Context(), c.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	_, _ = w.Write(WebVTT(c, segs, t))
}

// WebVTT renders timings as a WebVTT transcript with the segments' text.
func WebVTT(c *store.Chapter, segs []store.Segment, t *render.Timings) []byte {
	text := make(map[int64]string, len(segs))
	for _, s := range segs {
		text[s.ID] = s.Text
	}
	var b bytes.Buffer
	b.WriteString("WEBVTT\n")
	n := 0
	for _, q := range t.Cues {
		s := c.Title
		if q.SegmentID != 0 {
			s = text[q.SegmentID]
		}
		s = cueText(s)
		if s == "" || q.EndMs <= q.StartMs {
			continue
		}
		n++
		_, _ = fmt.Fprintf(&b, "\n%d\n%s --> %s\n%s\n", n, vttTime(q.StartMs), vttTime(q.EndMs), s)
	}
	return b.Bytes()
}

// cueText makes text safe for a cue: one line, no markup, no "-->".
func cueText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
	return s
}

func vttTime(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60, ms%1000)
}
