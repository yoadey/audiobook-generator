// Package epub reads EPUB 2 and 3 files: metadata, cover, table of contents
// and the body text of the spine as a flat stream of blocks.
//
// It is deliberately small and streaming: each content document is
// tokenized on its own, so a single huge XHTML file does not need a DOM.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

// Book is an opened EPUB.
type Book struct {
	Title    string
	Author   string
	Language string
	// Description is the publisher's blurb as plain text: paragraphs
	// separated by blank lines ("" when none).
	Description string

	// CoverHref is the zip path of the cover image ("" when none).
	CoverHref      string
	CoverMediaType string

	// Spine lists the linear content documents (zip paths) in reading order.
	Spine []string
	// TOC is the table of contents (nav document or NCX), hrefs resolved to
	// zip paths.
	TOC []TOCEntry

	zip *zip.Reader
}

// TOCEntry is one table-of-contents entry.
type TOCEntry struct {
	Title    string
	Href     string // zip path of the target document
	Fragment string // anchor within the document, may be empty
	Children []TOCEntry
}

// ErrNotEPUB is returned for files that are not a readable EPUB.
var ErrNotEPUB = errors.New("not an EPUB file")

// maxXMLSize bounds container, OPF, NCX and nav documents.
const maxXMLSize = 32 << 20

// Open parses the package metadata. Content documents are read lazily.
func Open(r io.ReaderAt, size int64) (*Book, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotEPUB, err)
	}
	b := &Book{zip: zr}

	opfPath, err := b.rootfile()
	if err != nil {
		return nil, err
	}
	if err := b.readPackage(opfPath); err != nil {
		return nil, err
	}
	if len(b.Spine) == 0 {
		return nil, fmt.Errorf("%w: empty spine", ErrNotEPUB)
	}
	return b, nil
}

// Open returns a reader for a file inside the EPUB.
func (b *Book) Open(name string) (io.ReadCloser, error) {
	f, err := b.zip.Open(name)
	if err != nil {
		// Some EPUBs percent-encode hrefs or differ in case; try a lookup.
		for _, zf := range b.zip.File {
			if strings.EqualFold(zf.Name, name) {
				return zf.Open()
			}
		}
		return nil, err
	}
	return f, nil
}

func (b *Book) readXML(name string, v any) error {
	f, err := b.Open(name)
	if err != nil {
		return fmt.Errorf("%w: missing %s", ErrNotEPUB, name)
	}
	defer func() { _ = f.Close() }()
	dec := xml.NewDecoder(io.LimitReader(f, maxXMLSize))
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: parse %s: %w", ErrNotEPUB, name, err)
	}
	return nil
}

func (b *Book) rootfile() (string, error) {
	var c struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := b.readXML("META-INF/container.xml", &c); err != nil {
		return "", err
	}
	for _, rf := range c.Rootfiles {
		if rf.MediaType == "" || rf.MediaType == "application/oebps-package+xml" {
			return rf.FullPath, nil
		}
	}
	return "", fmt.Errorf("%w: no OPF rootfile", ErrNotEPUB)
}

type opfPackage struct {
	Metadata struct {
		Titles       []string `xml:"title"`
		Creators     []string `xml:"creator"`
		Languages    []string `xml:"language"`
		Descriptions []string `xml:"description"`
		Metas        []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		ItemRefs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func (b *Book) readPackage(opfPath string) error {
	var p opfPackage
	if err := b.readXML(opfPath, &p); err != nil {
		return err
	}
	base := path.Dir(opfPath)

	b.Title = firstNonEmpty(p.Metadata.Titles)
	b.Author = strings.Join(trimAll(p.Metadata.Creators), ", ")
	b.Language = firstNonEmpty(p.Metadata.Languages)
	b.Description = plainText(firstNonEmpty(p.Metadata.Descriptions))

	type item struct{ href, mediaType, props string }
	items := make(map[string]item, len(p.Manifest))
	var navHref string
	for _, it := range p.Manifest {
		href := resolve(base, it.Href)
		items[it.ID] = item{href, it.MediaType, it.Properties}
		props := strings.Fields(it.Properties)
		if slices.Contains(props, "nav") {
			navHref = href
		}
		if slices.Contains(props, "cover-image") {
			b.CoverHref, b.CoverMediaType = href, it.MediaType
		}
	}
	if b.CoverHref == "" {
		// EPUB 2: <meta name="cover" content="manifest-id"/>.
		for _, m := range p.Metadata.Metas {
			if m.Name == "cover" {
				if it, ok := items[m.Content]; ok && strings.HasPrefix(it.mediaType, "image/") {
					b.CoverHref, b.CoverMediaType = it.href, it.mediaType
				}
			}
		}
	}

	for _, ref := range p.Spine.ItemRefs {
		it, ok := items[ref.IDRef]
		if !ok || ref.Linear == "no" {
			continue
		}
		if strings.Contains(it.mediaType, "html") || it.mediaType == "" {
			b.Spine = append(b.Spine, it.href)
		}
	}

	switch {
	case navHref != "":
		toc, err := b.readNav(navHref)
		if err == nil && len(toc) > 0 {
			b.TOC = toc
			return nil
		}
		fallthrough
	default:
		if it, ok := items[p.Spine.Toc]; ok {
			if toc, err := b.readNCX(it.href); err == nil {
				b.TOC = toc
			}
		}
	}
	return nil
}

// resolve joins an href (relative to dir, possibly percent-encoded, possibly
// with a fragment) into a clean zip path without fragment.
func resolve(dir, href string) string {
	href, _, _ = strings.Cut(href, "#")
	href = unescape(href)
	if dir == "." {
		return path.Clean(href)
	}
	return path.Clean(path.Join(dir, href))
}

func splitHref(dir, href string) (string, string) {
	file, frag, _ := strings.Cut(href, "#")
	if file == "" {
		return "", unescape(frag)
	}
	return resolve(dir, file), unescape(frag)
}

func unescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			sb.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func trimAll(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
