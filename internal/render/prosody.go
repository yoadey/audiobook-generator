package render

import (
	"encoding/binary"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	samples := make([]int16, len(pcm)/2)
	if _, err := binary.Decode(pcm, binary.LittleEndian, samples); err != nil {
		return
	}
	peak := 0
	for _, v := range samples {
		peak = max(peak, int(v), -int(v))
	}
	if peak > 0 {
		gain = min(gain, 0.97*math.MaxInt16/float64(peak))
	}
	for i, v := range samples {
		samples[i] = int16(math.Round(max(min(float64(v)*gain, math.MaxInt16), math.MinInt16)))
	}
	_, _ = binary.Encode(pcm, binary.LittleEndian, samples)
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
		// plain: the usual pause between segments or paragraphs, nothing
		// a listener should hear (an engine may read on instead).
		plain := false
		switch it.gap {
		case gapPhrase:
			if it.pause == 0 {
				it.pause = phraseEllipsis
			}
			continue
		case gapSegment:
			it.pause, plain = withinPara, true
			if it.thought || next != nil && next.thought {
				it.pause, plain = afterThought, false
			}
		case gapPara:
			it.pause, plain = betweenParas, true
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
			it.pause, plain = max(it.pause, afterTrailing), false
		case cut:
			// Interrupted speech: the next speaker cuts in.
			if it.quote {
				it.pause, plain = afterCut, false
			}
		}
		if it.emotion == "sad" {
			it.pause, plain = it.pause+afterSad, false
		}
		if it.extra > 0 {
			it.pause, plain = max(it.pause, it.extra), false
		}
		it.plain = plain
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

// phrases splits a segment's text (runes) at an ellipsis within it ("I
// just… I don't know"), where Kokoro barely pauses, and at extra breaks:
// rune offsets where a phrase starts after a pause of the given length.
func phrases(r []rune, breaks map[int]time.Duration) []phrase {
	var out []phrase
	at := 0
	cutAt := func(i int, d time.Duration) {
		if i <= at || i >= len(r) || strings.TrimSpace(string(r[at:i])) == "" {
			return
		}
		out = append(out, phrase{from: at, to: i, pause: d})
		at = i
	}
	for i := 0; i < len(r); i++ {
		if d, ok := breaks[i]; ok {
			cutAt(i, d)
		}
		if r[i] != '…' && (r[i] != '.' || i+2 >= len(r) || r[i+1] != '.' || r[i+2] != '.') {
			continue
		}
		j := i + 1
		for j < len(r) && r[j] == '.' {
			j++
		}
		// Followed by a space and more words (not an ellipsis at the end or
		// before a closing mark).
		if j+1 < len(r) && unicode.IsSpace(r[j]) && strings.IndexFunc(string(r[j:]), unicode.IsLetter) >= 0 {
			// A pause asked for there (after the spaces) is the same one.
			k := j
			for k < len(r) && unicode.IsSpace(r[k]) {
				k++
			}
			cutAt(j, max(phraseEllipsis, breaks[k]))
		}
		i = j - 1
	}
	if strings.TrimSpace(string(r[at:])) != "" {
		out = append(out, phrase{from: at, to: len(r)})
	}
	return out
}

// phrase is the runes [from, to) of a segment's text and the pause after.
type phrase struct {
	from, to int
	pause    time.Duration
}

// moods are how the narrator reads by the mood of a paragraph (from the
// chapter preparation), subtler than the emotions of speech.
var moods = map[string]prosody{
	"calm":       {0.97, 1},
	"tense":      {1.04, 1},
	"action":     {1.06, 1.04},
	"somber":     {0.94, 0.94},
	"warm":       {0.98, 1},
	"light":      {1.03, 1},
	"mysterious": {0.95, 0.95},
}

// directed is a segment's text for the TTS with the chapter preparation
// applied: its spoken forms replace their spots, its pauses split it into
// phrases (extra: the pause it asks for after the segment).
type directed struct {
	phrases []string
	pauses  []time.Duration
	extra   time.Duration
}

// direct prepares a segment's text (already without its surrounding
// marks, which started lead runes into the segment's text).
func direct(s *store.Segment, text string, lead int) directed {
	r := []rune(text)
	var breaks map[int]time.Duration
	var forms []store.SpokenForm
	var out directed
	if s != nil {
		n := utf8.RuneCountInString(s.Text)
		for _, p := range store.DecodePauses(s.Pauses, n) {
			at := p.Offset - lead
			d := time.Duration(p.Ms) * time.Millisecond
			switch {
			case at >= len(r):
				out.extra = max(out.extra, d)
			case at > 0:
				if breaks == nil {
					breaks = map[int]time.Duration{}
				}
				// The pause goes before the spaces after the words.
				for at < len(r) && unicode.IsSpace(r[at]) {
					at++
				}
				breaks[at] = max(breaks[at], d)
			}
		}
		for _, f := range store.DecodeSpokenAs(s.SpokenAs, n) {
			f.Start -= lead
			f.End -= lead
			if f.Start >= 0 && f.End <= len(r) {
				forms = append(forms, f)
			}
		}
	}
	for _, ph := range phrases(r, breaks) {
		var b strings.Builder
		at := ph.from
		for _, f := range forms {
			if f.Start < ph.from || f.End > ph.to {
				continue // across a break: left as it is
			}
			b.WriteString(string(r[at:f.Start]))
			b.WriteString(f.Say)
			at = f.End
		}
		b.WriteString(string(r[at:ph.to]))
		out.phrases = append(out.phrases, strings.TrimSpace(b.String()))
		out.pauses = append(out.pauses, ph.pause)
	}
	return out
}
