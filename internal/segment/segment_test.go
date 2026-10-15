package segment

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func q(n int, text string) Part { return Part{Kind: Quote, Quote: n, Text: text} }
func n(text string) Part        { return Part{Kind: Narration, Text: text} }

func TestCurlyDouble(t *testing.T) {
	s := New(CurlyDouble)
	assert.Equal(t, []Part{
		q(1, "“Hello,”"),
		n("she said, and then, after a pause:"),
		q(2, "“Don’t ‘quote’ me.”"),
	}, s.Paragraph("“Hello,” she said, and then, after a pause: “Don’t ‘quote’ me.”"))

	// Punctuation-only narration between quotes is dropped.
	assert.Equal(t, []Part{q(3, "“Yes!”"), q(4, "“No!”")}, s.Paragraph("“Yes!” — “No!”"))
	assert.Equal(t, []Part{n("Plain narration.")}, s.Paragraph("Plain narration."))
	assert.Equal(t, 4, s.Quotes())
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
