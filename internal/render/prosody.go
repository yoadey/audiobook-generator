package render

import (
	"encoding/binary"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/yoadey/audiobook-generator/internal/store"
)

// Kokoro has no emotion control; a quote's emotion (from the attribution)
// is given by pace and loudness, kept subtle so voices stay natural.
type prosody struct {
	speed float64 // factor on the voice's speed
	gain  float64 // factor on the samples
}

var neutral = prosody{1, 1}

var emotions = map[string]prosody{
	"happy":      {1.04, 1.03},
	"sad":        {0.92, 0.88},
	"angry":      {1.06, 1.15},
	"afraid":     {1.07, 0.95},
	"surprised":  {1.05, 1.08},
	"whispering": {0.93, 0.6},
	"shouting":   {1.04, 1.3},
	"sarcastic":  {0.96, 1},
}

// thoughtProsody: thoughts are a little softer than speech.
var thoughtProsody = prosody{0.98, 0.85}

// prosodyOf is how a segment is spoken (nil: the chapter title).
func prosodyOf(s *store.Segment) prosody {
	if s == nil || s.QuoteRef == 0 {
		return neutral
	}
	p, ok := emotions[s.Emotion]
	if !ok {
		p = neutral
	}
	if s.Kind == store.SegmentThought {
		p.speed *= thoughtProsody.speed
		p.gain *= thoughtProsody.gain
	}
	return p
}

// applyGain scales 16-bit PCM in place, never louder than just below full
// scale (a shout does not clip).
func applyGain(pcm []byte, gain float64) {
	if gain == 1 || len(pcm) < 2 {
		return
	}
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		peak = max(peak, v, -v)
	}
	if peak > 0 {
		gain = min(gain, 0.97*math.MaxInt16/float64(peak))
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) * gain
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(math.Round(max(min(v, math.MaxInt16), math.MinInt16)))))
	}
}

// gap is what follows a piece of speech; plan decides the kind, pauses
// the length (which depends on the pieces on both sides).
type gap int

const (
	gapPhrase  gap = iota // within a segment, at a break (an ellipsis, a marked pause)
	gapSegment            // between segments of a paragraph
	gapPara
	gapHeading
	gapScene
	gapTail
)

// Pauses by context.
const (
	afterThought   = 250 * time.Millisecond // around a thought in a paragraph
	afterTrailing  = 450 * time.Millisecond // after speech that trails off (…)
	afterCut       = 60 * time.Millisecond  // after speech that is cut off (—)
	dialogueTurn   = 300 * time.Millisecond // between paragraphs of only speech
	afterSad       = 150 * time.Millisecond // extra after a sad line
	phraseEllipsis = 350 * time.Millisecond
)

// pauses sets the pause after every item from its gap and its neighbours.
func pauses(items []item) {
	for i := range items {
		it := &items[i]
		var next *item
		if i+1 < len(items) {
			next = &items[i+1]
		}
		switch it.gap {
		case gapPhrase:
			if it.pause == 0 {
				it.pause = phraseEllipsis
			}
			continue
		case gapSegment:
			it.pause = withinPara
			if it.thought || next != nil && next.thought {
				it.pause = afterThought
			}
		case gapPara:
			it.pause = betweenParas
			if it.dialogue && next != nil && next.dialogue {
				it.pause = dialogueTurn
			}
		case gapHeading:
			it.pause = afterHeading
		case gapScene:
			it.pause = sceneBreak
		case gapTail:
			it.pause = tail
			continue
		}
		if it.gap > gapPara {
			continue
		}
		switch ending(it.text) {
		case trailing:
			it.pause = max(it.pause, afterTrailing)
		case cut:
			// Interrupted speech: the next speaker cuts in.
			if it.quote {
				it.pause = afterCut
			}
		}
		if it.emotion == "sad" {
			it.pause += afterSad
		}
	}
}

type end int

const (
	plain end = iota
	trailing
	cut
)

// ending tells how a piece of speech ends: trailing off ("I wonder…") or
// cut off ("But I—").
func ending(s string) end {
	s = strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("”’\"'»›)", r) })
	switch {
	case strings.HasSuffix(s, "…") || strings.HasSuffix(s, "..."):
		return trailing
	case strings.HasSuffix(s, "—") || strings.HasSuffix(s, "–") || strings.HasSuffix(s, "--"):
		return cut
	}
	return plain
}

// phrases splits a segment's text at an ellipsis within it ("I just… I
// don't know"), where Kokoro barely pauses, and at extra breaks: rune
// offsets where a phrase starts after a pause of the given length.
func phrases(text string, breaks map[int]time.Duration) []phrase {
	r := []rune(text)
	var out []phrase
	at := 0
	cutAt := func(i int, d time.Duration) {
		if s := strings.TrimSpace(string(r[at:i])); s != "" && i < len(r) {
			out = append(out, phrase{text: s, pause: d})
			at = i
		}
	}
	for i := 0; i < len(r); i++ {
		if d, ok := breaks[i]; ok && i > 0 {
			cutAt(i, d)
		}
		if r[i] != '…' && !(r[i] == '.' && i+2 < len(r) && r[i+1] == '.' && r[i+2] == '.') {
			continue
		}
		j := i + 1
		for j < len(r) && r[j] == '.' {
			j++
		}
		// Followed by a space and more words (not an ellipsis at the end or
		// before a closing mark).
		if j+1 < len(r) && unicode.IsSpace(r[j]) && strings.IndexFunc(string(r[j:]), unicode.IsLetter) >= 0 {
			cutAt(j, phraseEllipsis)
		}
		i = j - 1
	}
	if s := strings.TrimSpace(string(r[at:])); s != "" {
		out = append(out, phrase{text: s})
	}
	return out
}

type phrase struct {
	text  string
	pause time.Duration // after it
}
