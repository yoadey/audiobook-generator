package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/api"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/llm/llmtest"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
)

var (
	paraRe   = regexp.MustCompile(`(?m)^\[p(\d+)\] (.*)$`)
	listedRe = regexp.MustCompile(`(?m)^c(\d+) \| (\w+)`)
	// speechRe: a run in curly quotes, perhaps left open at the end of
	// the paragraph or opened before it.
	speechRe = regexp.MustCompile(`“?([^“”]+)”`)
)

// attributionAnswer finds the speech in curly quotes of every numbered
// paragraph: the first of a paragraph with "said Anna" is Anna's, the
// rest Tom's.
func attributionAnswer(r llmtest.Request) llmtest.Reply {
	ms := r.Messages()
	msg, _ := ms[len(ms)-1]["content"].(string)
	if !strings.Contains(msg, "Paragraphs to annotate") {
		return castingAnswer(r)
	}
	ids := map[string]string{}
	for _, m := range listedRe.FindAllStringSubmatch(msg, -1) {
		ids[m[2]] = "c" + m[1]
	}
	var vs []map[string]any
	for _, m := range paraRe.FindAllStringSubmatch(msg, -1) {
		n, _ := strconv.Atoi(m[1])
		text := m[2]
		var runs []string
		if i := strings.Index(text, "“"); i >= 0 && !strings.Contains(text, "”") {
			runs = append(runs, text[i+len("“"):]) // goes on in the next paragraph
		}
		for _, q := range speechRe.FindAllStringSubmatch(text, -1) {
			if strings.HasPrefix(q[0], "“") || strings.Index(text, q[0]) == 0 {
				runs = append(runs, q[1])
			}
		}
		for k, run := range runs {
			who := ids["Tom"]
			if k == 0 && strings.Contains(text, "said Anna") {
				who = ids["Anna"]
			}
			vs = append(vs, map[string]any{
				"p": n, "start": run, "end": run, "kind": "speech", "speaker": who, "emotion": "neutral",
				"direction": "calm and friendly", "confidence": 0.5,
			})
		}
	}
	raw, _ := json.Marshal(map[string]any{"voices": vs, "narration": []any{}})
	return llmtest.Reply{Content: string(raw)}
}

func TestAttributionFlow(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		c := newAPI(t, s, 10<<20)
		fake := llmtest.New(t)
		fake.Respond = attributionAnswer
		p := &store.LLMProvider{Name: "fake", Enabled: true, BaseURL: fake.BaseURL(), Model: "m"}
		llm.Normalize(p)
		require.NoError(t, s.CreateLLMProvider(ctx, p))

		data, err := epubtest.Sample()
		require.NoError(t, err)
		var book api.Book
		require.Equal(t, http.StatusAccepted, c.upload("sample.epub", data, &book))
		waitImported(t, c, book.Id)
		id := itoa(book.Id)
		ids, err := s.ChapterIDs(ctx, book.Id)
		require.NoError(t, err)
		ch1 := "/api/chapters/" + itoa(ids[1])

		var e api.Error
		require.Equal(t, http.StatusConflict, c.json(http.MethodPost, ch1+"/attribution", nil, &e))
		assert.Contains(t, e.Message, "analysis must finish")

		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, "/api/books/"+id+"/analysis", nil, nil))
		waitCasting(t, c, id)

		var att api.ChapterAttribution
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, ch1+"/attribution", nil, &att))
		var d api.ChapterDetail
		require.Eventually(t, func() bool {
			require.Equal(t, http.StatusOK, c.json(http.MethodGet, ch1, nil, &d))
			return d.AttributionStatus == api.AttributionStatusDone
		}, 10*time.Second, 20*time.Millisecond)
		require.NotNil(t, d.Attribution)
		// The speech that runs over two paragraphs is a passage in each.
		assert.Equal(t, 4, d.Attribution.Quotes)
		assert.Equal(t, 4, d.Attribution.Attributed)
		assert.Equal(t, 4, d.Attribution.Unsure, "confidence 0.5 is flagged")
		assert.Equal(t, "attribute_chapter@v3", *d.Attribution.Prompt)
		require.NotNil(t, d.Speakers)
		names := map[int64]string{}
		for _, sp := range *d.Speakers {
			names[sp.Id] = sp.Name
		}
		byRef := map[int]api.Segment{}
		for _, para := range d.Paragraphs {
			for _, sg := range para.Segments {
				if sg.QuoteRef > 0 {
					byRef[sg.QuoteRef] = sg
				}
			}
		}
		require.NotNil(t, byRef[1].SpeakerId)
		assert.Equal(t, "Anna", names[*byRef[1].SpeakerId])
		assert.Equal(t, "“Good morning,”", byRef[1].Text)
		require.NotNil(t, byRef[1].Direction)
		assert.Equal(t, "calm and friendly", *byRef[1].Direction)
		require.NotNil(t, byRef[1].Voicing)
		assert.Equal(t, api.VoicingSpeech, *byRef[1].Voicing)
		assert.Equal(t, "Tom", names[*byRef[3].SpeakerId])
		tomID := *byRef[3].SpeakerId

		// Chosen by hand: the narrator reads quote 1, Tom says quote 2 angrily.
		// (Fresh structs: decoding into a used one keeps absent pointer fields.)
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, ch1+"/quotes/1", map[string]any{"narrator": true}, nil))
		d = api.ChapterDetail{}
		require.Equal(t, http.StatusOK, c.json(http.MethodPatch, ch1+"/quotes/2",
			map[string]any{"speakerId": tomID, "emotion": "angry"}, &d))
		for _, para := range d.Paragraphs {
			for _, sg := range para.Segments {
				switch sg.QuoteRef {
				case 1:
					assert.Nil(t, sg.SpeakerId)
					assert.True(t, sg.SpeakerManual)
				case 2:
					assert.Equal(t, tomID, *sg.SpeakerId)
					assert.Equal(t, api.EmotionAngry, *sg.Emotion)
					assert.InDelta(t, 1, sg.Confidence, 1e-6)
				}
			}
		}
		assert.Equal(t, 2, d.Attribution.Unsure, "only quotes 3 and 4 are still unsure")
		for _, bad := range []map[string]any{
			{"speakerId": tomID, "narrator": true}, {"emotion": "bored"}, {"speakerId": 999999},
		} {
			require.Equal(t, http.StatusBadRequest, c.json(http.MethodPatch, ch1+"/quotes/2", bad, nil), bad)
		}
		require.Equal(t, http.StatusNotFound, c.json(http.MethodPatch, ch1+"/quotes/99", map[string]any{"narrator": true}, nil))

		// Attributing again keeps the speakers chosen by hand.
		require.Equal(t, http.StatusAccepted, c.json(http.MethodPost, ch1+"/attribution", map[string]any{"force": true}, nil))
		require.Eventually(t, func() bool {
			d = api.ChapterDetail{}
			require.Equal(t, http.StatusOK, c.json(http.MethodGet, ch1, nil, &d))
			return d.AttributionStatus == api.AttributionStatusDone
		}, 10*time.Second, 20*time.Millisecond)
		for _, para := range d.Paragraphs {
			for _, sg := range para.Segments {
				if sg.QuoteRef == 1 {
					assert.Nil(t, sg.SpeakerId)
				}
			}
		}
		var chapters api.ChapterPage
		require.Equal(t, http.StatusOK, c.json(http.MethodGet, "/api/books/"+id+"/chapters", nil, &chapters))
		assert.Equal(t, api.AttributionStatusDone, chapters.Items[1].AttributionStatus)
		assert.Equal(t, api.AttributionStatusNone, chapters.Items[2].AttributionStatus)
	})
}
