package render

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/lexicon"
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
	return plan(ch, paras, segs, func(*store.Segment) (string, float64) { return "af_heart", 1 }, lexicon.Compile(nil))
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
	second := int64(300 + 100 + phraseEllipsis.Milliseconds())
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
