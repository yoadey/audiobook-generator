package feed

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/segment"
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
// A segment's cue is split into its sentences (timed by their length), so
// apps show short lines.
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
		if q.EndMs <= q.StartMs || cueText(s) == "" {
			continue
		}
		for _, l := range lines(s, q.StartMs, q.EndMs, q.Words) {
			n++
			_, _ = fmt.Fprintf(&b, "\n%d\n%s --> %s\n%s\n", n, vttTime(l.from), vttTime(l.to), cueText(l.text))
		}
	}
	return b.Bytes()
}

type line struct {
	text     string
	from, to int64
}

// lines splits a cue's text into sentences over [from, to), each starting
// with its first word when the TTS reported word times ([rune offset,
// ms]), else timed by its share of the characters.
func lines(text string, from, to int64, words [][2]int64) []line {
	sentences := segment.Sentences(text)
	total := 0
	for _, s := range sentences {
		total += utf8.RuneCountInString(s)
	}
	if len(sentences) < 2 || total == 0 {
		return []line{{text, from, to}}
	}
	out := make([]line, 0, len(sentences))
	at, done, offset := from, 0, 0
	rest := text
	for i, s := range sentences {
		if k := strings.Index(rest, s); k >= 0 {
			offset += utf8.RuneCountInString(rest[:k])
			rest = rest[k:]
		}
		if i > 0 {
			// The start of the sentence's first timed word.
			for _, w := range words {
				if int(w[0]) >= offset {
					if w[1] > out[i-1].from && w[1] < to {
						out[i-1].to, at = w[1], w[1]
					}
					break
				}
			}
		}
		done += utf8.RuneCountInString(s)
		end := max(at, from+(to-from)*int64(done)/int64(total))
		if i == len(sentences)-1 {
			end = to
		}
		out = append(out, line{s, at, end})
		at = end
		offset += utf8.RuneCountInString(s)
		rest = rest[len(s):]
	}
	return out
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
