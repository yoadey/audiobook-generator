// Package epubtest builds small EPUB files for tests and fixtures.
package epubtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"strings"
)

// Doc is one XHTML content document in the spine.
type Doc struct {
	Name      string // e.g. "ch1.xhtml", stored below OEBPS/text/
	Body      string // inner XHTML of <body>
	NonLinear bool
}

// Entry is a table-of-contents entry; Href is relative to the text
// directory, e.g. "ch1.xhtml#part2".
type Entry struct {
	Title    string
	Href     string
	Children []Entry
}

// Options describe the EPUB to build.
type Options struct {
	Title, Author, Language string
	Docs                    []Doc
	TOC                     []Entry
	// Version 2 writes an NCX table of contents, 3 (default) a nav document.
	Version int
	// Cover, when set, is stored as a PNG cover image.
	Cover []byte
	// Description, when set, is the dc:description (HTML is kept as text,
	// escaped, the way publishers write it).
	Description string
}

// Build returns the EPUB file bytes.
func Build(o Options) ([]byte, error) {
	if o.Version == 0 {
		o.Version = 3
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string, store bool) error {
		method := zip.Deflate
		if store {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}

	if err := write("mimetype", "application/epub+zip", true); err != nil {
		return nil, err
	}
	if err := write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`, false); err != nil {
		return nil, err
	}

	var manifest, spine strings.Builder
	for i, d := range o.Docs {
		fmt.Fprintf(&manifest, `<item id="doc%d" href="text/%s" media-type="application/xhtml+xml"/>`+"\n", i, d.Name)
		linear := ""
		if d.NonLinear {
			linear = ` linear="no"`
		}
		fmt.Fprintf(&spine, `<itemref idref="doc%d"%s/>`+"\n", i, linear)
		content := `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>` + html.EscapeString(d.Name) + `</title><style>p{margin:0}</style></head>
<body>` + d.Body + `</body></html>`
		if err := write("OEBPS/text/"+d.Name, content, false); err != nil {
			return nil, err
		}
	}

	metaCover := ""
	if o.Cover != nil {
		props := ""
		if o.Version == 3 {
			props = ` properties="cover-image"`
		}
		fmt.Fprintf(&manifest, `<item id="cover-img" href="images/cover.png" media-type="image/png"%s/>`+"\n", props)
		metaCover = `<meta name="cover" content="cover-img"/>`
		if err := write("OEBPS/images/cover.png", string(o.Cover), true); err != nil {
			return nil, err
		}
	}

	spineAttr := ""
	if o.Version == 3 {
		manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
		if err := write("OEBPS/nav.xhtml", navDoc(o.TOC), false); err != nil {
			return nil, err
		}
	} else {
		manifest.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
		spineAttr = ` toc="ncx"`
		if err := write("OEBPS/toc.ncx", ncxDoc(o.TOC), false); err != nil {
			return nil, err
		}
	}

	opf := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="%d.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="id">urn:uuid:test</dc:identifier>
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
    <dc:language>%s</dc:language>
    %s%s
  </metadata>
  <manifest>
%s  </manifest>
  <spine%s>
%s  </spine>
</package>`, o.Version, html.EscapeString(o.Title), html.EscapeString(o.Author),
		html.EscapeString(o.Language), metaCover, description(o.Description), manifest.String(),
		spineAttr, spine.String())
	if err := write("OEBPS/content.opf", opf, false); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func description(d string) string {
	if d == "" {
		return ""
	}
	return "\n    <dc:description>" + html.EscapeString(d) + "</dc:description>"
}

func navDoc(toc []Entry) string {
	var sb strings.Builder
	var list func([]Entry)
	list = func(es []Entry) {
		sb.WriteString("<ol>")
		for _, e := range es {
			sb.WriteString("<li>")
			if e.Href != "" {
				fmt.Fprintf(&sb, `<a href="text/%s">%s</a>`, e.Href, html.EscapeString(e.Title))
			} else {
				fmt.Fprintf(&sb, `<span>%s</span>`, html.EscapeString(e.Title))
			}
			if len(e.Children) > 0 {
				list(e.Children)
			}
			sb.WriteString("</li>")
		}
		sb.WriteString("</ol>")
	}
	list(toc)
	return `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Contents</title></head>
<body><nav epub:type="toc" id="toc"><h1>Contents</h1>` + sb.String() + `</nav></body></html>`
}

func ncxDoc(toc []Entry) string {
	var sb strings.Builder
	n := 0
	var points func([]Entry)
	points = func(es []Entry) {
		for _, e := range es {
			n++
			fmt.Fprintf(&sb, `<navPoint id="p%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="text/%s"/>`,
				n, n, html.EscapeString(e.Title), e.Href)
			points(e.Children)
			sb.WriteString("</navPoint>")
		}
	}
	points(toc)
	return `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>` + sb.String() + `</navMap></ncx>`
}

// Paragraphs wraps each string in <p>…</p> (already escaped text expected).
func Paragraphs(ps ...string) string {
	var sb strings.Builder
	for _, p := range ps {
		sb.WriteString("<p>" + p + "</p>\n")
	}
	return sb.String()
}

// TinyPNG is a valid 1×1 PNG image.
var TinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89" +
	"\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")

// Sample is a small two-volume book exercising quotes across paragraphs,
// scene breaks, front matter and a cover. hack/testdata/sample.epub is this
// book (see TestSampleFixtureIsUpToDate).
func Sample() ([]byte, error) {
	p := Paragraphs
	return Build(Options{
		Title: "The Sample", Author: "A. Writer", Language: "en",
		Cover: TinyPNG,
		Description: "<p>Two mornings in a small town: <i>will it rain?</i></p>" +
			"<p>A sample book for the tests.</p>",
		Docs: []Doc{
			{Name: "copyright.xhtml", Body: p("Copyright 2026 A. Writer.")},
			{Name: "v1.xhtml", Body: `<h1 id="v1">Book One</h1>` +
				`<h2 id="c1">Chapter 1</h2>` +
				p("“Good morning,” said Anna. “Is it raining?”",
					"Tom looked outside. “It was raining earlier,",
					"“but it has stopped now.”") +
				`<p>* * *</p>` + p("Later that day, nothing happened.") +
				`<h2 id="c2">Chapter 2</h2>` +
				p("“Bye,” said Tom.")},
			{Name: "v2.xhtml", Body: `<h1 id="v2">Book Two</h1><h2 id="c3">Chapter 3</h2>` +
				p(strings.Repeat("A very long sentence keeps going on and on. ", 20))},
		},
		TOC: []Entry{
			{Title: "Copyright", Href: "copyright.xhtml"},
			{Title: "Book One", Href: "v1.xhtml#v1", Children: []Entry{
				{Title: "Chapter 1", Href: "v1.xhtml#c1"},
				{Title: "Chapter 2", Href: "v1.xhtml#c2"},
			}},
			{Title: "Book Two", Href: "v2.xhtml#v2", Children: []Entry{
				{Title: "Chapter 3", Href: "v2.xhtml#c3"},
			}},
		},
	})
}
