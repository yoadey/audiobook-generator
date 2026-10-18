package epub

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// plainText turns a description, which publishers often give as (escaped)
// HTML, into plain text: one paragraph per block or line break, separated
// by blank lines, with the markup and extra whitespace gone.
func plainText(s string) string {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{
		Type: html.ElementNode, Data: "body", DataAtom: atom.Body,
	})
	if err != nil {
		return normalizeSpace(s)
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
		case n.Type == html.ElementNode && n.DataAtom == atom.Br:
			end()
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
	case atom.P, atom.Div, atom.Li, atom.Ul, atom.Ol, atom.Blockquote, atom.H1, atom.H2,
		atom.H3, atom.H4, atom.H5, atom.H6, atom.Section, atom.Article, atom.Table, atom.Tr:
		return true
	}
	return false
}
