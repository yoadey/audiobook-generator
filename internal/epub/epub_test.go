package epub_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/epub"
	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
)

var p = epubtest.Paragraphs

func open(t *testing.T, o epubtest.Options) *epub.Book {
	t.Helper()
	data, err := epubtest.Build(o)
	require.NoError(t, err)
	b, err := epub.Open(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	return b
}

func blocks(t *testing.T, b *epub.Book) []epub.Block {
	t.Helper()
	var out []epub.Block
	require.NoError(t, b.Walk(epub.Visitor{Block: func(blk epub.Block) error {
		out = append(out, blk)
		return nil
	}}))
	return out
}

func titles(o *epub.Outline) []string {
	var out []string
	for _, c := range o.Chapters {
		out = append(out, c.Title)
	}
	return out
}

func TestMetadataAndCover(t *testing.T) {
	b := open(t, epubtest.Options{
		Title: "Pride & Prejudice", Author: "Jane Austen", Language: "en",
		Docs:  []epubtest.Doc{{Name: "a.xhtml", Body: p("Hi.")}},
		TOC:   []epubtest.Entry{{Title: "One", Href: "a.xhtml"}},
		Cover: []byte("\x89PNG fake"),
	})
	assert.Equal(t, "Pride & Prejudice", b.Title)
	assert.Equal(t, "Jane Austen", b.Author)
	assert.Equal(t, "en", b.Language)
	assert.Equal(t, "OEBPS/images/cover.png", b.CoverHref)
	assert.Equal(t, "image/png", b.CoverMediaType)
	assert.Equal(t, []string{"OEBPS/text/a.xhtml"}, b.Spine)

	f, err := b.Open(b.CoverHref)
	require.NoError(t, err)
	data, _ := io.ReadAll(f)
	assert.Equal(t, "\x89PNG fake", string(data))
}

func TestDescription(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"A plain blurb.", "A plain blurb."},
		{"<p>One <i>two</i>\n three.</p><p>Four &amp; five.</p>", "One two three.\n\nFour & five."},
		{"Line one<br/>line two<script>x()</script>", "Line one\n\nline two"},
		{"<div><p>Nested</p> tail</div>", "Nested\n\ntail"},
	} {
		b := open(t, epubtest.Options{
			Title: "T", Language: "en", Description: tc.in,
			Docs: []epubtest.Doc{{Name: "a.xhtml", Body: p("Hi.")}},
		})
		assert.Equal(t, tc.want, b.Description, tc.in)
	}
}

func TestNotAnEPUB(t *testing.T) {
	_, err := epub.Open(strings.NewReader("hello"), 5)
	assert.ErrorIs(t, err, epub.ErrNotEPUB)
}

func TestBlocks(t *testing.T) {
	b := open(t, epubtest.Options{
		Docs: []epubtest.Doc{{Name: "a.xhtml", Body: `
<h1 class="chapter">Chapter&#160;One</h1>
<p>It is a truth   universally <i>acknowledged</i>,
that a single man<a epub:type="noteref" href="#n1">1</a> must be in want of a wife.</p>
<div>Loose div text<br/>with a line break.</div>
<p>* * *</p>
<hr/>
<div><p>Nested paragraph.</p></div>
<aside epub:type="footnote" id="n1"><p>A footnote nobody wants read aloud.</p></aside>
<p>Soft&#173;hyphen and zero&#8203;width.</p>
<script>var x = "nope";</script>
<p hidden="">Hidden.</p>`}},
	})
	got := blocks(t, b)
	type simple struct {
		Kind epub.BlockKind
		Text string
	}
	var s []simple
	for _, blk := range got {
		s = append(s, simple{blk.Kind, blk.Text})
	}
	assert.Equal(t, []simple{
		{epub.Heading, "Chapter One"},
		{epub.Text, "It is a truth universally acknowledged, that a single man must be in want of a wife."},
		{epub.Text, "Loose div text with a line break."},
		{epub.SceneBreak, "* * *"},
		{epub.SceneBreak, "* * *"},
		{epub.Text, "Nested paragraph."},
		{epub.Text, "Softhyphen and zerowidth."},
	}, s)
	assert.Equal(t, 1, got[0].Level)
	for i, blk := range got {
		assert.Equal(t, i, blk.Index)
	}
}

func TestNonLinearDocsAreSkipped(t *testing.T) {
	b := open(t, epubtest.Options{Docs: []epubtest.Doc{
		{Name: "a.xhtml", Body: p("Main text.")},
		{Name: "notes.xhtml", Body: p("Endnotes."), NonLinear: true},
	}})
	got := blocks(t, b)
	require.Len(t, got, 1)
	assert.Equal(t, "Main text.", got[0].Text)
}

func TestOutlineFromNavWithVolumesAndFragments(t *testing.T) {
	// Two volumes in one file each, chapters addressed by fragment, plus a
	// title page and a copyright page.
	b := open(t, epubtest.Options{
		Docs: []epubtest.Doc{
			{Name: "front.xhtml", Body: p("Copyright 2026. All rights reserved.")},
			{Name: "v1.xhtml", Body: `<h1 id="v1">Volume One</h1>` +
				`<h2 id="c1">Chapter 1</h2>` + p("Alpha one two three.", "Alpha four.") +
				`<h2 id="c2">Chapter 2</h2>` + p("Beta one two.")},
			{Name: "v2.xhtml", Body: `<h1>Volume Two</h1>` + p(strings.Repeat("Introduction words here. ", 20)) +
				`<h2 id="c3">Chapter 3</h2>` + p("Gamma.")},
		},
		TOC: []epubtest.Entry{
			{Title: "Copyright", Href: "front.xhtml"},
			{Title: "Volume One", Href: "v1.xhtml#v1", Children: []epubtest.Entry{
				{Title: "Chapter 1", Href: "v1.xhtml#c1"},
				{Title: "Chapter 2", Href: "v1.xhtml#c2"},
			}},
			{Title: "Volume Two", Href: "v2.xhtml", Children: []epubtest.Entry{
				{Title: "Chapter 3", Href: "v2.xhtml#c3"},
			}},
		},
	})
	o, err := epub.Plan(b, nil)
	require.NoError(t, err)
	assert.Equal(t, "toc", o.Source)
	assert.Equal(t, []epub.Volume{{Title: "Volume One"}, {Title: "Volume Two"}}, o.Volumes)
	// "Volume One" has no text of its own besides its heading (dropped as a
	// title page); "Volume Two" has a long introduction, kept as a chapter.
	assert.Equal(t, []string{"Copyright", "Chapter 1", "Chapter 2", "Volume Two", "Chapter 3"}, titles(o))

	c := o.Chapters
	assert.True(t, c[0].Excluded, "copyright page is excluded")
	assert.Equal(t, -1, c[0].Volume)
	assert.Equal(t, 0, c[1].Volume)
	assert.Equal(t, 1, c[3].Volume)
	assert.False(t, c[1].Excluded)
	// Chapter 1 starts at its heading and ends where Chapter 2's heading is.
	assert.Equal(t, 8, c[1].Words) // "Chapter 1" + "Alpha one two three." + "Alpha four."
	assert.Equal(t, c[1].End, c[2].Start)
	assert.Equal(t, o.Blocks, c[4].End)
}

func TestOutlineFromNCX(t *testing.T) {
	b := open(t, epubtest.Options{
		Version: 2,
		Docs: []epubtest.Doc{
			{Name: "c1.xhtml", Body: p("One.")},
			{Name: "c2.xhtml", Body: p("Two.")},
		},
		TOC: []epubtest.Entry{
			{Title: "First", Href: "c1.xhtml"},
			{Title: "Second", Href: "c2.xhtml"},
		},
	})
	o, err := epub.Plan(b, nil)
	require.NoError(t, err)
	assert.Equal(t, "toc", o.Source)
	assert.Equal(t, []string{"First", "Second"}, titles(o))
}

func TestOutlineFallsBackToHeadings(t *testing.T) {
	b := open(t, epubtest.Options{
		Docs: []epubtest.Doc{{Name: "all.xhtml", Body: `<h1>The Book</h1>` + p("Foreword text.") +
			`<h2>Prologue</h2>` + p("Once.") + `<h2>Chapter 1</h2>` + p("Then.") + `<h2>Chapter 2</h2>` + p("Later.")}},
		TOC: []epubtest.Entry{{Title: "Start", Href: "all.xhtml"}},
	})
	var observed int
	o, err := epub.Plan(b, func(epub.Block) { observed++ })
	require.NoError(t, err)
	assert.Equal(t, "headings", o.Source)
	assert.Equal(t, []string{"Front matter", "Prologue", "Chapter 1", "Chapter 2"}, titles(o))
	assert.True(t, o.Chapters[0].Excluded)
	assert.Equal(t, o.Blocks, observed)
}

func TestOutlineFallsBackToDocuments(t *testing.T) {
	b := open(t, epubtest.Options{Docs: []epubtest.Doc{
		{Name: "a.xhtml", Body: p("First file.")},
		{Name: "b.xhtml", Body: `<h3>Named</h3>` + p("Second file.")},
	}})
	o, err := epub.Plan(b, nil)
	require.NoError(t, err)
	assert.Equal(t, "documents", o.Source)
	assert.Equal(t, []string{"Part 1", "Named"}, titles(o))
}

func TestIsMatter(t *testing.T) {
	for _, s := range []string{"Copyright", "Table of Contents", "About the Author", "Also by Jane Doe", "ACKNOWLEDGMENTS"} {
		assert.True(t, epub.IsMatter(s), s)
	}
	for _, s := range []string{"Chapter 1", "The Cover Story", "Prologue", "Epilogue"} {
		assert.False(t, epub.IsMatter(s), s)
	}
}

func TestEmphasis(t *testing.T) {
	b := open(t, epubtest.Options{
		Docs: []epubtest.Doc{{Name: "a.xhtml", Body: `
<p>She stopped. <em> This is wrong, </em>she thought. It was <i>very</i> late.</p>
<p><span style="font-style: italic">All of it.</span></p>
<p class="x">Nested <em>outer <i>inner</i> rest</em> end.</p>
<div><i><p>Across</p><p>blocks.</p></i></div>
<h2><em>Heading</em></h2>`}},
	})
	got := blocks(t, b)
	sub := func(blk epub.Block) []string {
		r := []rune(blk.Text)
		var out []string
		for _, s := range blk.Emphasis {
			out = append(out, string(r[s.Start:s.End]))
		}
		return out
	}
	require.Len(t, got, 6)
	assert.Equal(t, "She stopped. This is wrong, she thought. It was very late.", got[0].Text)
	assert.Equal(t, []string{"This is wrong,", "very"}, sub(got[0]))
	assert.Equal(t, []string{"All of it."}, sub(got[1]))
	assert.Equal(t, "Nested outer inner rest end.", got[2].Text)
	assert.Equal(t, []string{"outer inner rest"}, sub(got[2]))
	assert.Equal(t, []string{"Across"}, sub(got[3]))
	assert.Equal(t, []string{"blocks."}, sub(got[4]))
	assert.Equal(t, "Heading", got[5].Text)
	assert.Empty(t, got[5].Emphasis)
}
