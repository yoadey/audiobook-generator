package render

import (
	"encoding/binary"
	"math"
	"strings"
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
type engines string // the engine's fingerprint

func (engines) Expressive(v string) bool { return v[:3] == "cb:" }
func (engines) TakesSpeed(v string) bool { return v[:3] != "cb:" }
func (e engines) Fingerprint(v string) string {
	if v[:3] == "cb:" {
		return string(e)
	}
	return ""
}
func (engines) JoinLimit(v string) int {
	if v[:3] == "cb:" {
		return 120
	}
	return 0
}

func TestJoin(t *testing.T) {
	ps, ss := chapter(
		// One voice: the whole paragraph in one request.
		[]string{"q:“Are you open?”", "n:she asked.", "q:“I’ve been walking since dawn.”"},
		// Two voices: each voice's run of pieces.
		[]string{"q:“We’re open,”", "n:Erin said.", "n:She smiled."},
		// A thought keeps its pause; a long paragraph splits at the limit.
		[]string{"n:Rain fell.", "t:Not again,", "n:she thought."},
		[]string{"n:" + strings.Repeat("Word ", 15) + "end.", "n:" + strings.Repeat("Word ", 15) + "end.",
			"n:Last."},
	)
	ch := &store.Chapter{Title: "One"}
	voiceOf := func(s *store.Segment) (string, float64) {
		if s != nil && s.ParagraphID == 2 && s.QuoteRef > 0 {
			return "cb:erin.wav", 1
		}
		return "cb:emily.wav", 1
	}
	items := plan(ch, ps, ss, voiceOf, lexicon.Compile(nil), engines("v1"))
	var texts []string
	for _, it := range items {
		texts = append(texts, it.text)
	}
	assert.Equal(t, []string{
		"One",
		"Are you open? she asked. I’ve been walking since dawn.",
		"We’re open,", "Erin said. She smiled.",
		"Rain fell.", "Not again,", "she thought.",
		strings.Repeat("Word ", 15) + "end.", strings.Repeat("Word ", 15) + "end. Last.",
	}, texts)
	assert.Equal(t, []part{{1, 13}, {2, 10}, {3, 29}}, items[1].parts)
	assert.Equal(t, betweenParas, items[1].pause, "the paragraph's pause after it")
	assert.Nil(t, items[2].parts)

	// Kokoro's pieces are never joined.
	kokoro := plan(ch, ps, ss, func(*store.Segment) (string, float64) { return "af_heart", 1 },
		lexicon.Compile(nil), engines("v1"))
	assert.Len(t, kokoro, len(ss)+1)

	// Cues: one per segment, the joined item's time split by length.
	bytes := make([]int64, len(items))
	for i := range bytes {
		bytes[i] = 2 * 24000 // one second each
	}
	cs := cues(items, bytes, nil, func(int64) string { return "" })
	require.Len(t, cs, len(ss)+1)
	assert.Equal(t, []int64{1, 2, 3}, []int64{cs[1].SegmentID, cs[2].SegmentID, cs[3].SegmentID})
	assert.Equal(t, cs[1].EndMs, cs[2].StartMs)
	assert.Equal(t, cs[2].EndMs, cs[3].StartMs)
	assert.Equal(t, cs[1].StartMs+1000, cs[3].EndMs)
	assert.InDelta(t, 1000*13/52, cs[1].EndMs-cs[1].StartMs, 1)
	assert.InDelta(t, 1000*10/52, cs[2].EndMs-cs[2].StartMs, 1)
	for i, c := range cs {
		assert.Less(t, c.StartMs, c.EndMs, "cue %d", i)
		if i > 0 {
			assert.LessOrEqual(t, cs[i-1].EndMs, c.StartMs, "cue %d", i)
		}
	}

	// A segment split at an ellipsis, its last phrase joined to the next
	// segment: still one cue per segment.
	ps, ss = chapter([]string{"n:I just… I don’t know.", "n:Then rain."})
	items = plan(ch, ps, ss, voiceOf, lexicon.Compile(nil), engines("v1"))
	require.Len(t, items, 3)
	assert.Equal(t, "I don’t know. Then rain.", items[2].text)
	cs = cues(items, bytes[:3], nil, func(int64) string { return "" })
	require.Len(t, cs, 3)
	assert.Equal(t, []int64{0, 1, 2}, []int64{cs[0].SegmentID, cs[1].SegmentID, cs[2].SegmentID})
	assert.Equal(t, cs[1].EndMs, cs[2].StartMs)
}

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
	}, lexicon.Compile(nil), engines("v1"))
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

	// The style and the engine's settings are part of the cache key.
	calm := shout
	calm.style = &style{emotion: "happy"}
	assert.NotEqual(t, shout.file("d", 0), calm.file("d", 0))
	louder := shout
	louder.engine = "|v2"
	assert.NotEqual(t, shout.file("d", 0), louder.file("d", 0))
	assert.Empty(t, said.engine)
}

func TestJoinPassages(t *testing.T) {
	ps, ss := chapter(
		[]string{"n:The rain fell."},
		[]string{"n:It fell all night."}, // flows on
		[]string{"n:Nobody came."},       // flows on (made to trail off below)
		[]string{"n:Then morning…"},
		[]string{"n:The sun rose."}, // flows on: after a trailing line still not
		[]string{"q:“Hello?”", "n:she called."},
		[]string{"e:angry:“Go away!”", "n:he said."},
	)
	for _, i := range []int{1, 2, 4, 5} {
		ps[i].Flows = true
	}
	ss[2].Text = "Nobody came…"
	ch := &store.Chapter{Title: "One"}
	// Everything in the narrator's voice (no character voices, unknown
	// speakers): quotations without an emotion are read like narration.
	items := plan(ch, ps, ss, func(*store.Segment) (string, float64) { return "cb:emily.wav", 1 },
		lexicon.Compile(nil), engines("v1"))
	var texts []string
	for _, it := range items {
		texts = append(texts, it.text)
	}
	assert.Equal(t, []string{
		"One",
		"The rain fell. It fell all night. Nobody came…", // across flowing paragraphs
		"Then morning…",
		"The sun rose. Hello? she called.", // the narrator reads the quotation
		"Go away!", "he said.",             // an emotion of its own stays apart
	}, texts)

	// Without the preparation's flows: paragraph by paragraph.
	for i := range ps {
		ps[i].Flows = false
	}
	items = plan(ch, ps, ss, func(*store.Segment) (string, float64) { return "cb:emily.wav", 1 },
		lexicon.Compile(nil), engines("v1"))
	assert.Len(t, items, 9)
	assert.Equal(t, "Hello? she called.", items[6].text)

	// Narrator only: speakers and emotions are dropped.
	ss[len(ss)-2].SpeakerCharacterID = new(int64)
	narratorOnly(ss)
	assert.Nil(t, ss[len(ss)-2].SpeakerCharacterID)
	assert.Empty(t, ss[len(ss)-2].Emotion)
}

func TestFlowsAndNarratorThoughts(t *testing.T) {
	// A paragraph with nothing to say between two others: no reading on
	// across it, whichever is marked.
	ps, ss := chapter([]string{"n:The rain fell."}, []string{"n:◇◇◇"}, []string{"n:Morning came."})
	ss[1].Text = "…"
	ps[1].Flows, ps[2].Flows = true, true
	ch := &store.Chapter{Title: "One"}
	cb := func(*store.Segment) (string, float64) { return "cb:emily.wav", 1 }
	items := plan(ch, ps, ss, cb, lexicon.Compile(nil), engines("v1"))
	var texts []string
	for _, it := range items {
		texts = append(texts, it.text)
	}
	assert.NotContains(t, texts, "The rain fell. Morning came.")

	// A thought the narrator reads in a paragraph with a mood stays softer.
	ps, ss = chapter([]string{"n:She sat down.", "t:Why me?"})
	ps[0].Mood = "calm"
	items = plan(ch, ps, ss, func(*store.Segment) (string, float64) { return "af_heart", 1 },
		lexicon.Compile(nil), nil)
	require.Len(t, items, 3)
	assert.InDelta(t, 0.85, items[2].gain, 1e-9)
	assert.InDelta(t, 0.97*0.98, items[2].speed, 0.006)
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
