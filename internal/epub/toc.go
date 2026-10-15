package epub

import (
	"path"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// readNav parses the EPUB 3 navigation document's table of contents.
func (b *Book) readNav(href string) ([]TOCEntry, error) {
	f, err := b.Open(href)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	doc, err := html.Parse(f)
	if err != nil {
		return nil, err
	}

	var navs []*html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Nav {
			navs = append(navs, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if len(navs) == 0 {
		return nil, nil
	}
	nav := navs[0]
	for _, n := range navs {
		if slices.Contains(strings.Fields(attr(n, "epub:type")), "toc") || attr(n, "role") == "doc-toc" {
			nav = n
			break
		}
	}
	ol := firstChild(nav, atom.Ol)
	if ol == nil {
		return nil, nil
	}
	return navList(ol, path.Dir(href)), nil
}

func navList(ol *html.Node, dir string) []TOCEntry {
	var out []TOCEntry
	for li := ol.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.DataAtom != atom.Li {
			continue
		}
		var e TOCEntry
		if a := firstChild(li, atom.A); a != nil {
			e.Title = normalizeSpace(textContent(a))
			e.Href, e.Fragment = splitHref(dir, attr(a, "href"))
		} else if span := firstChild(li, atom.Span); span != nil {
			e.Title = normalizeSpace(textContent(span))
		}
		if sub := firstChild(li, atom.Ol); sub != nil {
			e.Children = navList(sub, dir)
		}
		if e.Title != "" || len(e.Children) > 0 {
			out = append(out, e)
		}
	}
	return out
}

// readNCX parses an EPUB 2 NCX table of contents.
func (b *Book) readNCX(href string) ([]TOCEntry, error) {
	type navPoint struct {
		Label   string `xml:"navLabel>text"`
		Content struct {
			Src string `xml:"src,attr"`
		} `xml:"content"`
		Children []navPoint `xml:"navPoint"`
	}
	var ncx struct {
		Points []navPoint `xml:"navMap>navPoint"`
	}
	if err := b.readXML(href, &ncx); err != nil {
		return nil, err
	}
	dir := path.Dir(href)
	var conv func([]navPoint) []TOCEntry
	conv = func(ps []navPoint) []TOCEntry {
		out := make([]TOCEntry, 0, len(ps))
		for _, p := range ps {
			e := TOCEntry{Title: normalizeSpace(p.Label), Children: conv(p.Children)}
			e.Href, e.Fragment = splitHref(dir, p.Content.Src)
			out = append(out, e)
		}
		return out
	}
	return conv(ncx.Points), nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key || (a.Namespace != "" && a.Namespace+":"+a.Key == key) {
			return a.Val
		}
	}
	return ""
}

// firstChild returns the first descendant element with the given atom,
// searching depth-first but not into nested lists.
func firstChild(n *html.Node, a atom.Atom) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.DataAtom == a {
			return c
		}
		if c.DataAtom == atom.Ol || c.DataAtom == atom.Li {
			continue
		}
		if found := firstChild(c, a); found != nil {
			return found
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
