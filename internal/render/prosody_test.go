package render

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/lexicon"
	"github.com/yoadey/audiobook-generator/internal/prepare"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// chapter builds paragraphs of segments: each paragraph a list of
// "n:text" (narration), "q:text" (quote), "t:text" (thought), "e:emotion:text"
// (quote with emotion), or "---" (scene break).
func chapter(paras ...[]string) ([]store.Paragraph, []store.Segment) {
	var ps []store.Paragraph
	var ss []store.Segment
	quote := 0
	for i, p := range paras {
		para := store.Paragraph{ID: int64(i + 1), Kind: store.ParagraphText}
		if len(p) == 1 && p[0] == "---" {
			para.Kind = store.ParagraphSceneBreak
			ps = append(ps, para)
			continue
		}
		ps = append(ps, para)
		for _, s := range p {
			seg := store.Segment{ID: int64(len(ss) + 1), ParagraphID: para.ID, Kind: store.SegmentNarration}
			switch s[:2] {
			case "q:", "t:", "e:":
				quote++
				seg.QuoteRef, seg.Kind = quote, store.SegmentQuote
				if s[0] == 't' {
					seg.Kind = store.SegmentThought
				}
				if s[0] == 'e' {
					rest := s[2:]
					for j := range rest {
						if rest[j] == ':' {
							seg.Emotion, s = rest[:j], "x:"+rest[j+1:]
							break
						}
					}
				}
			}
			seg.Text = s[2:]
			ss = append(ss, seg)
		}
	}
	return ps, ss
}

func planOf(paras []store.Paragraph, segs []store.Segment) []item {
	ch := &store.Chapter{Title: "Ignored"}
	paras = append([]store.Paragraph{{ID: 99, Kind: store.ParagraphHeading}}, paras...)
	return plan(ch, paras, segs, func(*store.Segment) (string, float64) { return "af_heart", 1 }, lexicon.Compile(nil), nil)
}

func TestPauses(t *testing.T) {
	ps, ss := chapter(
		[]string{"q:“Where were you?”"},
		[]string{"q:“Out.”"},
		[]string{"n:She sighed.", "t:Not again,", "n:she thought."},
		[]string{"q:“I wonder…”", "n:he said."},
		[]string{"q:“But I—”"},
		[]string{"e:sad:“It is over.”"},
		[]string{"---"},
		[]string{"n:Morning came."},
	)
	items := planOf(ps, ss)
	var got []time.Duration
	for _, it := range items {
		got = append(got, it.pause)
	}
	assert.Equal(t, []time.Duration{
		dialogueTurn,  // between two lines of speech
		betweenParas,  // before narration
		afterThought,  // before a thought
		afterThought,  // after it
		betweenParas,  // paragraph end
		afterTrailing, // trailing off
		betweenParas,
		afterCut,   // cut off
		sceneBreak, // (no extra for sadness)
		tail,
	}, got)
}

func TestEmotionProsody(t *testing.T) {
	ps, ss := chapter([]string{"e:whispering:“Quiet.”", "n:she said.", "e:shouting:“Run!”", "t:Too late,"})
	items := planOf(ps, ss)
	require.Len(t, items, 4)
	assert.Equal(t, 0.93, items[0].speed)
	assert.Equal(t, 0.6, items[0].gain)
	assert.Equal(t, 1.0, items[1].speed, "narration is neutral")
	assert.Equal(t, 1.0, items[1].gain)
	assert.Equal(t, 1.3, items[2].gain)
	assert.InDelta(t, 0.85, items[3].gain, 1e-9, "thoughts are softer")
}

// engines: voices "cb:…" are an expressive engine that paces itself.
type engines struct{}

func (engines) Expressive(v string) bool { return v[:3] == "cb:" }
func (engines) TakesSpeed(v string) bool { return v[:3] != "cb:" }

func TestExpressiveEngine(t *testing.T) {
	ps, ss := chapter([]string{"e:shouting:“Run!”", "n:she said."}, []string{"t:Too late,", "n:he thought."})
	ps[1].Mood = "tense"
	ch := &store.Chapter{Title: "Ignored"}
	paras := append([]store.Paragraph{{ID: 99, Kind: store.ParagraphHeading}}, ps...)
	items := plan(ch, paras, ss, func(s *store.Segment) (string, float64) {
		if s != nil && s.QuoteRef > 0 {
			return "cb:anna.wav", 1.2
		}
		return "af_heart", 1.1
	}, lexicon.Compile(nil), engines{})
	require.Len(t, items, 4)
	shout, said, thought, narration := items[0], items[1], items[2], items[3]
	assert.Equal(t, &style{emotion: "shouting"}, shout.style)
	assert.Equal(t, 1.0, shout.speed, "the engine paces itself")
	assert.Equal(t, 1.3, shout.gain, "loudness is still ours")
	assert.Equal(t, tts.Speech{Text: "Run!", Voice: "cb:anna.wav", Speed: 1, Format: "pcm", Emotion: "shouting"},
		shout.speech())
	assert.Nil(t, said.style, "Kokoro gets pace instead")
	assert.Equal(t, 1.1, said.speed)
	assert.Equal(t, &style{thought: true}, thought.style)
	assert.InDelta(t, 1.04*1.1, narration.speed, 0.006, "Kokoro narration in a tense mood")

	// The style is part of the cache key.
	calm := shout
	calm.style = &style{emotion: "happy"}
	assert.NotEqual(t, shout.file("d", 0), calm.file("d", 0))
}

func TestPhrases(t *testing.T) {
	ps, ss := chapter([]string{"q:“I just… I don’t know. Wait...”"})
	items := planOf(ps, ss)
	require.Len(t, items, 2)
	assert.Equal(t, "I just…", items[0].text)
	assert.Equal(t, phraseEllipsis, items[0].pause)
	assert.Equal(t, "I don’t know. Wait…", items[1].text)
	assert.Equal(t, items[0].segmentID, items[1].segmentID)

	// Cues: one per segment; words at their offsets in the segment.
	words := [][]tts.Word{
		{{Text: "I", Start: 0}, {Text: "just", Start: 0.05}, {Text: "…", Start: 0.08}},
		{{Text: "I", Start: 0}, {Text: "don't", Start: 0.02}, {Text: "know", Start: 0.04}, {Text: "Wait", Start: 0.07}},
	}
	text := ss[0].Text
	cs := cues(items, []int64{4800, 4800}, words, func(int64) string { return text })
	require.Len(t, cs, 1)
	assert.Equal(t, int64(300), cs[0].StartMs)
	second := 300 + 100 + phraseEllipsis.Milliseconds()
	assert.Equal(t, second+100, cs[0].EndMs)
	assert.Equal(t, [][2]int64{{1, 300}, {3, 350}, {9, second}, {11, second + 20}, {17, second + 40}, {23, second + 70}},
		cs[0].Words)
}

func TestAlignWords(t *testing.T) {
	// "12" is read as "twelve" and has no match; the rest stays in step.
	text := "On May 12, Ryoka’s friend left."
	got := alignWords(text, []timedWord{
		{"On", 0}, {"May", 100}, {"twelve", 200}, {",", 300}, {"Ryoka's", 400}, {"friend", 500}, {"left", 600}, {".", 700},
	})
	assert.Equal(t, [][2]int64{{0, 0}, {3, 100}, {11, 400}, {19, 500}, {26, 600}}, got)
	assert.Nil(t, alignWords("Hello", nil))
}

func TestApplyGain(t *testing.T) {
	pcm := make([]byte, 4)
	binary.LittleEndian.PutUint16(pcm, uint16(int16(20000)))
	v := int16(-10000)
	binary.LittleEndian.PutUint16(pcm[2:], uint16(v))
	applyGain(pcm, 2) // would clip: limited to just below full scale
	a := int16(binary.LittleEndian.Uint16(pcm))
	b := int16(binary.LittleEndian.Uint16(pcm[2:]))
	assert.InDelta(t, 0.97*math.MaxInt16, float64(a), 1)
	assert.InDelta(t, -0.97*math.MaxInt16/2, float64(b), 1)
	applyGain(pcm, 0.5)
	assert.InDelta(t, float64(a)/2, float64(int16(binary.LittleEndian.Uint16(pcm))), 1)
}

func TestDirectedText(t *testing.T) {
	// “At Lv. 5 he stopped. The door was open.” with a spoken form for
	// "Lv. 5", a pause after "stopped." and one after the segment.
	s := &store.Segment{QuoteRef: 1, Kind: store.SegmentQuote, Text: "“At Lv. 5 he stopped. The door was open.”"}
	s.SpokenAs = store.EncodeSpokenAs([]store.SpokenForm{{Start: 4, End: 9, Say: "level five"}})
	s.Pauses = store.EncodePauses([]store.Pause{{Offset: 21, Ms: 800}, {Offset: 41, Ms: 1000}})
	d := direct(s, speakable(s.Text), 1)
	assert.Equal(t, []string{"At level five he stopped.", "The door was open."}, d.phrases)
	assert.Equal(t, []time.Duration{800 * time.Millisecond, 0}, d.pauses)
	assert.Equal(t, time.Second, d.extra)

	// In a plan: the segment's pause after it outlasts the paragraph's.
	ps := []store.Paragraph{{ID: 1, Kind: store.ParagraphText}, {ID: 2, Kind: store.ParagraphText, Mood: "tense"}}
	s.ParagraphID, s.ID = 1, 1
	n := store.Segment{ID: 2, ParagraphID: 2, Kind: store.SegmentNarration, Text: "He ran."}
	items := planOf(ps, []store.Segment{*s, n})
	require.Len(t, items, 3)
	assert.Equal(t, 800*time.Millisecond, items[0].pause)
	assert.Equal(t, time.Second, items[1].pause)
	assert.Equal(t, 1.04, items[2].speed, "narration in a tense paragraph")

	// Broken data is ignored.
	s.SpokenAs, s.Pauses = `[{"a":3,"b":99,"t":"x"}]`, "nonsense"
	d = direct(s, speakable(s.Text), 1)
	assert.Equal(t, []string{"At Lv. 5 he stopped. The door was open."}, d.phrases)
}

func TestMoodsCovered(t *testing.T) {
	for _, m := range prepare.Moods {
		assert.Contains(t, moods, m)
	}
}
