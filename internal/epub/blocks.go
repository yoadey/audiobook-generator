package epub

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// BlockKind classifies a block of body text.
type BlockKind int

// Block kinds.
const (
	Text BlockKind = iota
	Heading
	SceneBreak
)

func (k BlockKind) String() string {
	switch k {
	case Heading:
		return "heading"
	case SceneBreak:
		return "scene_break"
	default:
		return "text"
	}
}

// Block is one paragraph-like unit of body text.
type Block struct {
	Index int // position in the whole book (spine order)
	Doc   string
	Kind  BlockKind
	Level int // heading level 1-6 for Heading blocks
	Text  string
}

// Visitor receives the body text of the book. Block is required; the other
// callbacks are optional.
type Visitor struct {
	Block func(Block) error
	// DocStart is called before the first block of each spine document;
	// next is the index the document's first block will get.
	DocStart func(doc string, next int)
	// Anchor is called for every element id; next is the index of the block
	// the anchor points at.
	Anchor func(doc, id string, next int)
}

// Walk streams the body text of all spine documents in reading order.
func (b *Book) Walk(v Visitor) error {
	next := 0
	for _, doc := range b.Spine {
		if v.DocStart != nil {
			v.DocStart(doc, next)
		}
		f, err := b.Open(doc)
		if err != nil {
			return fmt.Errorf("open %s: %w", doc, err)
		}
		n, err := walkDoc(f, doc, next, v)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", doc, err)
		}
		next = n
	}
	return nil
}

type frame struct {
	name string
	a    atom.Atom
	skip bool
}

var blockElements = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Li: true, atom.Ul: true, atom.Ol: true,
	atom.Blockquote: true, atom.Pre: true, atom.Dd: true, atom.Dt: true, atom.Dl: true,
	atom.Table: true, atom.Tr: true, atom.Td: true, atom.Th: true, atom.Caption: true,
	atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true,
	atom.Main: true, atom.Aside: true, atom.Figure: true, atom.Figcaption: true,
	atom.Address: true, atom.Center: true, atom.Hgroup: true, atom.Body: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
}

var headingLevel = map[atom.Atom]int{
	atom.H1: 1, atom.H2: 2, atom.H3: 3, atom.H4: 4, atom.H5: 5, atom.H6: 6,
}

// Elements whose text is never read aloud.
var skippedElements = map[atom.Atom]bool{
	atom.Head: true, atom.Script: true, atom.Style: true, atom.Title: true,
	atom.Svg: true, atom.Math: true, atom.Noscript: true, atom.Template: true,
}

// Semantic types of notes and page markers (epub:type / ARIA role).
var skippedTypes = []string{
	"footnote", "footnotes", "endnote", "endnotes", "rearnote", "rearnotes",
	"noteref", "pagebreak", "doc-footnote", "doc-endnote", "doc-endnotes",
	"doc-noteref", "doc-pagebreak",
}

// sceneBreakRe matches paragraphs that are only ornaments ("* * *", "#", "⁂").
var sceneBreakRe = regexp.MustCompile(`^[\s*#~•·⁂◊§_=+\-–—.]{1,30}$`)

func shouldSkip(a atom.Atom, attrs []html.Attribute) bool {
	if skippedElements[a] {
		return true
	}
	for _, at := range attrs {
		if at.Key == "epub:type" || (at.Namespace != "" && at.Key == "type") || at.Key == "role" {
			for _, t := range strings.Fields(at.Val) {
				if slices.Contains(skippedTypes, t) {
					return true
				}
			}
		}
		if at.Key == "hidden" || (at.Key == "style" && strings.Contains(strings.ReplaceAll(at.Val, " ", ""), "display:none")) {
			return true
		}
	}
	return false
}

// walkDoc tokenizes one XHTML document and returns the next block index.
func walkDoc(r io.Reader, doc string, next int, v Visitor) (int, error) {
	z := html.NewTokenizer(r)
	var (
		stack  []frame
		buf    strings.Builder
		inBody bool
	)
	skipping := func() bool {
		for _, f := range stack {
			if f.skip {
				return true
			}
		}
		return false
	}
	emit := func(kind BlockKind, level int, text string) error {
		err := v.Block(Block{Index: next, Doc: doc, Kind: kind, Level: level, Text: text})
		next++
		return err
	}
	flush := func() error {
		text := normalizeSpace(buf.String())
		buf.Reset()
		if text == "" {
			return nil
		}
		level := 0
		for i := len(stack) - 1; i >= 0; i-- {
			if l, ok := headingLevel[stack[i].a]; ok {
				level = l
				break
			}
		}
		switch {
		case level > 0:
			return emit(Heading, level, text)
		case sceneBreakRe.MatchString(text):
			return emit(SceneBreak, 0, text)
		default:
			return emit(Text, 0, text)
		}
	}
	anchor := func(attrs []html.Attribute, a atom.Atom) {
		if v.Anchor == nil {
			return
		}
		for _, at := range attrs {
			if at.Key == "id" || (a == atom.A && at.Key == "name") {
				v.Anchor(doc, at.Val, next)
			}
		}
	}

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return next, flush()
			}
			return next, z.Err()

		case html.TextToken:
			if inBody && !skipping() {
				buf.Write(z.Text())
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			a := atom.Lookup(name)
			var attrs []html.Attribute
			for hasAttr {
				var k, val []byte
				k, val, hasAttr = z.TagAttr()
				attrs = append(attrs, html.Attribute{Key: string(k), Val: string(val)})
			}
			if a == atom.Body {
				inBody = true
			}
			if inBody && !skipping() {
				switch {
				case a == atom.Br:
					buf.WriteByte(' ')
				case a == atom.Hr:
					if err := flush(); err != nil {
						return next, err
					}
					if err := emit(SceneBreak, 0, "* * *"); err != nil {
						return next, err
					}
				case blockElements[a]:
					if err := flush(); err != nil {
						return next, err
					}
				}
			}
			if inBody {
				anchor(attrs, a)
			}
			if tt == html.SelfClosingTagToken || isVoid(a) {
				continue
			}
			stack = append(stack, frame{name: string(name), a: a, skip: shouldSkip(a, attrs)})

		case html.EndTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			if inBody && blockElements[a] && !skipping() {
				if err := flush(); err != nil {
					return next, err
				}
			}
			// Pop up to and including the nearest matching element; tolerate
			// unbalanced markup.
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == string(name) {
					stack = stack[:i]
					break
				}
			}
			if a == atom.Body {
				inBody = false
			}
		}
	}
}

func isVoid(a atom.Atom) bool {
	switch a {
	case atom.Br, atom.Hr, atom.Img, atom.Meta, atom.Link, atom.Input, atom.Col,
		atom.Area, atom.Base, atom.Embed, atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return false
}

// normalizeSpace collapses whitespace and drops invisible characters.
func normalizeSpace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == '\u00ad' || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff':
			continue
		case unicode.IsSpace(r):
			space = true
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteRune(r)
	}
	return sb.String()
}

// WordCount counts whitespace-separated words containing a letter or digit.
func WordCount(s string) int {
	n := 0
	for _, w := range strings.Fields(s) {
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			n++
		}
	}
	return n
}
