package epub

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// xmlText is an element whose content is text, escaped HTML or raw XHTML
// (all three occur in dc:description).
type xmlText struct {
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

var (
	cdata     = regexp.MustCompile(`<!\[CDATA\[[\s\S]*?\]\]>`)
	tag       = regexp.MustCompile(`</?[A-Za-z][^<>]*>`)
	blankLine = regexp.MustCompile(`\n\s*\n`)
)

// source is the element's content as text or HTML: the raw XHTML when it
// has child elements (which the text alone would lose), else its text
// (escaped HTML and CDATA come out decoded).
func (t xmlText) source() string {
	if tag.MatchString(cdata.ReplaceAllString(t.Inner, "")) {
		return t.Inner
	}
	return t.Text
}

// plainText turns a description, which publishers often give as (escaped)
// HTML, into plain text: one paragraph per block or line break, separated
// by blank lines, with the markup and extra whitespace gone. Text without
// markup keeps its own paragraphs (blank lines).
func plainText(s string) string {
	if !tag.MatchString(s) {
		var paras []string
		for _, p := range blankLine.Split(s, -1) {
			if p = normalizeSpace(p); p != "" {
				paras = append(paras, p)
			}
		}
		return strings.Join(paras, "\n\n")
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{
		Type: html.ElementNode, Data: "body", DataAtom: atom.Body,
	})
	if err != nil {
		return "" // markup no parser takes (absurdly deep): none rather than tags
	}
	var paras []string
	var cur strings.Builder
	end := func() {
		if t := normalizeSpace(cur.String()); t != "" {
			paras = append(paras, t)
		}
		cur.Reset()
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			cur.WriteString(n.Data)
		case n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style):
			return
		case n.Type == html.ElementNode && (n.DataAtom == atom.Br || n.DataAtom == atom.Hr):
			end()
		case n.Type == html.ElementNode && (n.DataAtom == atom.Td || n.DataAtom == atom.Th):
			cur.WriteByte(' ')
		}
		block := n.Type == html.ElementNode && isBlock(n.DataAtom)
		if block {
			end()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			end()
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	end()
	return strings.Join(paras, "\n\n")
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Li, atom.Ul, atom.Ol, atom.Dl, atom.Dt, atom.Dd, atom.Blockquote,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Section, atom.Article,
		atom.Header, atom.Footer, atom.Aside, atom.Figure, atom.Figcaption, atom.Table, atom.Tr,
		atom.Pre, atom.Address:
		return true
	}
	return false
}
