package segment

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func q(n int, text string) Part  { return Part{Kind: Quote, Quote: n, Text: text} }
func n(text string) Part         { return Part{Kind: Narration, Text: text} }
func th(n int, text string) Part { return Part{Kind: Thought, Quote: n, Text: text} }

func TestCurlyDouble(t *testing.T) {
	s := New(CurlyDouble)
	assert.Equal(t, []Part{
		q(1, "“Hello,”"),
		n("she said, and then, after a pause:"),
		q(2, "“Don’t ‘quote’ me.”"),
	}, s.Paragraph("“Hello,” she said, and then, after a pause: “Don’t ‘quote’ me.”"))

	// Punctuation-only narration between quotes stays with the quote
	// before it: no character of the text is lost.
	assert.Equal(t, []Part{q(3, "“Yes!” —"), q(4, "“No!”")}, s.Paragraph("“Yes!” — “No!”"))
	assert.Equal(t, []Part{q(5, "— “Well”")}, s.Paragraph("— “Well”"))
	assert.Equal(t, []Part{n("Plain narration.")}, s.Paragraph("Plain narration."))
	assert.Equal(t, 5, s.Quotes())
}

func TestMultiParagraphQuote(t *testing.T) {
	s := New(CurlyDouble)
	assert.Equal(t, []Part{n("He began:"), q(1, "“It was a long story,")},
		s.Paragraph("He began: “It was a long story,"))
	// Continues with an opening mark: same quotation.
	assert.Equal(t, []Part{q(1, "“and it went on.”"), n("He stopped.")},
		s.Paragraph("“and it went on.” He stopped."))

	// An unterminated quote not followed by an opening mark ends at the
	// paragraph boundary.
	assert.Equal(t, []Part{q(2, "“Broken")}, s.Paragraph("“Broken"))
	assert.Equal(t, []Part{n("Narration again.")}, s.Paragraph("Narration again."))
}

func TestStraightDouble(t *testing.T) {
	s := New(StraightDouble)
	assert.Equal(t, []Part{q(1, `"Run,"`), n("he said."), q(2, `"Now."`)},
		s.Paragraph(`"Run," he said. "Now."`))
}

func TestCurlySingle(t *testing.T) {
	s := New(CurlySingle)
	assert.Equal(t, []Part{
		q(1, "‘I don’t know,’"),
		n("said Harry. ‘Tis late, they’d say ‘em all."),
	}, s.Paragraph("‘I don’t know,’ said Harry. ‘Tis late, they’d say ‘em all."))
}

func TestStyleCounter(t *testing.T) {
	var c StyleCounter
	assert.Equal(t, CurlyDouble, c.Style())
	c.Add("‘Hello,’ she said. ‘Goodbye.’ ‘em")
	c.Add("“One” quote")
	assert.Equal(t, CurlySingle, c.Style())

	var d StyleCounter
	d.Add(`"A" and "B" and it's 5" tall`)
	assert.Equal(t, StraightDouble, d.Style())
	assert.Equal(t, StraightDouble, ParseStyle(StraightDouble.String()))
}

func TestSentences(t *testing.T) {
	assert.Equal(t, []string{
		"Mr. Darcy arrived at 5 p.m. on Tuesday.",
		"“Is it true?” she asked.",
		"J. R. R. Tolkien wrote it…",
		"Truly!",
		"End",
	}, Sentences("Mr. Darcy arrived at 5 p.m. on Tuesday. “Is it true?” she asked. J. R. R. Tolkien wrote it… Truly! End"))
	assert.Equal(t, []string{"no capital. after"}, Sentences("no capital. after"))
}

func TestChunk(t *testing.T) {
	assert.Nil(t, Chunk("   ", 10))
	assert.Equal(t, []string{"Short."}, Chunk("Short.", 400))

	text := strings.Repeat("This is a sentence. ", 30)
	chunks := Chunk(text, 100)
	for _, c := range chunks {
		assert.LessOrEqual(t, len([]rune(c)), 100)
		assert.True(t, strings.HasSuffix(c, "sentence."), c)
	}
	assert.Equal(t, strings.TrimSpace(text), strings.Join(chunks, " "))

	long := strings.Repeat("word ", 50) + "and then, " + strings.Repeat("more ", 30)
	for _, c := range Chunk(long, 60) {
		assert.LessOrEqual(t, len([]rune(c)), 60)
	}
}

func TestMoreStyles(t *testing.T) {
	s := New(StraightSingle)
	assert.Equal(t, []Part{q(1, "'I don't know,'"), n("said Ann. 'Tis late, they'd say 'em all.")},
		s.Paragraph("'I don't know,' said Ann. 'Tis late, they'd say 'em all."))

	s = New(Guillemets)
	assert.Equal(t, []Part{q(1, "«Come,»"), n("she said.")}, s.Paragraph("«Come,» she said."))
	s = New(GuillemetsReversed)
	assert.Equal(t, []Part{q(1, "»Come,«"), n("she said.")}, s.Paragraph("»Come,« she said."))
	s = New(LowHigh)
	assert.Equal(t, []Part{q(1, "„Komm“"), n(", sagte sie.")}, s.Paragraph("„Komm“, sagte sie."))

	s = New(Dash)
	assert.Equal(t, []Part{q(1, "—Hola"), n("—dijo él—."), q(2, "¿Cómo estás?")},
		s.Paragraph("—Hola —dijo él—. ¿Cómo estás?"))
	assert.Equal(t, []Part{n("It was well–known in 1990–2000.")}, s.Paragraph("It was well–known in 1990–2000."))
}

func TestThoughts(t *testing.T) {
	text := "She stopped. This is wrong, she thought. It was very late."
	span := func(sub string) Span {
		at := strings.Index(text, sub)
		if at < 0 {
			t.Fatalf("%q not in the text", sub)
		}
		i := len([]rune(text[:at]))
		return Span{i, i + len([]rune(sub))}
	}
	s := New(CurlyDouble)
	got := s.Paragraph(text, span("This is wrong,"), span("very"))
	assert.Equal(t, []Part{
		n("She stopped."), th(1, "This is wrong,"),
		{Kind: Narration, Text: "she thought. It was very late.", Emphasis: []Span{{20, 24}}},
	}, []Part{got[0], {Kind: got[1].Kind, Quote: got[1].Quote, Text: got[1].Text}, got[2]})
	assert.Equal(t, []Span{{0, 14}}, got[1].Emphasis)

	// Single marks in a book with double ones: a thought (or quoted
	// phrase); a single word or an elision is not.
	s = New(CurlyDouble)
	assert.Equal(t, []Part{
		q(1, "“Fine,”"), n("he said."), th(2, "‘I should go,’"), n("he thought. The ‘plan’ failed, ‘twas sad."),
	}, s.Paragraph("“Fine,” he said. ‘I should go,’ he thought. The ‘plan’ failed, ‘twas sad."))

	// Two stressed words that end a sentence are a thought; two that do
	// not, and italics inside a quotation, are not.
	assert.True(t, isThought([]rune("Not again!")))
	assert.False(t, isThought([]rune("very much")))
	s = New(CurlyDouble)
	text = "“Never ever again,” she said."
	got = s.Paragraph(text, Span{1, 17})
	assert.Equal(t, Quote, got[0].Kind)
	assert.Equal(t, []Span{{1, 17}}, got[0].Emphasis)
}

func TestMoreStyleDetection(t *testing.T) {
	for want, texts := range map[Style][]string{
		StraightSingle:     {"'Run,' he said. 'Now.'", "It's 'em."},
		Guillemets:         {"«Come,» she said. «Now.»"},
		GuillemetsReversed: {"»Come,« she said. »Now.«"},
		LowHigh:            {"„Komm“, sagte sie. „Jetzt.“"},
		Dash:               {"—Hola —dijo.", "—Sí.", "—No.", "Narration."},
	} {
		var c StyleCounter
		for _, x := range texts {
			c.Add(x)
		}
		assert.Equal(t, want, c.Style(), want.String())
		assert.Equal(t, want, ParseStyle(want.String()))
	}
}
