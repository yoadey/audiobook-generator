package epub

import (
	"regexp"
	"sort"
	"strconv"
)

// Outline is the book split into volumes and chapters. Chapter spans are
// half-open block index ranges [Start, End) over Book.Walk's blocks.
type Outline struct {
	Volumes  []Volume
	Chapters []Chapter
	// Blocks is the total number of blocks in the book.
	Blocks int
	// Source tells how the chapters were found: "toc", "headings" or "documents".
	Source string
}

// Volume groups chapters (a top-level TOC entry with children).
type Volume struct {
	Title string
}

// Chapter is one chapter span.
type Chapter struct {
	Title    string
	Volume   int // index into Outline.Volumes, -1 when none
	Start    int
	End      int
	Words    int
	Excluded bool // front/back matter that should not be narrated by default
}

// blockMeta is what Plan keeps per block: enough to place boundaries
// without holding the book's text in memory.
type blockMeta struct {
	kind  BlockKind
	level int
	words int
	text  string // headings only
}

// Plan scans the book once and splits it into chapters. observe, when not
// nil, sees the text of every block (e.g. to gather quote statistics).
func Plan(b *Book, observe func(Block)) (*Outline, error) {
	var (
		blocks    []blockMeta
		anchors   = map[string]int{}
		docStarts = map[string]int{}
	)
	err := b.Walk(Visitor{
		Block: func(blk Block) error {
			m := blockMeta{kind: blk.Kind, level: blk.Level, words: WordCount(blk.Text)}
			if blk.Kind == Heading {
				m.text = blk.Text
			}
			blocks = append(blocks, m)
			if observe != nil {
				observe(blk)
			}
			return nil
		},
		DocStart: func(doc string, next int) { docStarts[doc] = next },
		Anchor: func(doc, id string, next int) {
			if _, ok := anchors[doc+"#"+id]; !ok {
				anchors[doc+"#"+id] = next
			}
		},
	})
	if err != nil {
		return nil, err
	}

	p := planner{blocks: blocks}
	switch {
	case p.fromTOC(b.TOC, anchors, docStarts):
		p.out.Source = "toc"
	case p.fromHeadings():
		p.out.Source = "headings"
	default:
		p.fromDocuments(b.Spine, docStarts)
		p.out.Source = "documents"
	}
	p.finish()
	return &p.out, nil
}

type boundary struct {
	index  int
	title  string
	volume int
	// volumeStart marks the start of a volume: its own text (a title page,
	// an introduction) only becomes a chapter if it is long enough.
	volumeStart bool
}

type planner struct {
	blocks     []blockMeta
	boundaries []boundary
	out        Outline
}

// Volume intros shorter than this are title pages and get dropped.
const minVolumeIntroWords = 50

func (p *planner) fromTOC(toc []TOCEntry, anchors, docStarts map[string]int) bool {
	locate := func(e TOCEntry) (int, bool) {
		if e.Href == "" {
			return 0, false
		}
		if e.Fragment != "" {
			if i, ok := anchors[e.Href+"#"+e.Fragment]; ok {
				return i, true
			}
		}
		i, ok := docStarts[e.Href]
		return i, ok
	}
	// first returns the start of an entry or, for entries without a target,
	// of its first locatable descendant.
	var first func(TOCEntry) (int, bool)
	first = func(e TOCEntry) (int, bool) {
		if i, ok := locate(e); ok {
			return i, true
		}
		for _, c := range e.Children {
			if i, ok := first(c); ok {
				return i, true
			}
		}
		return 0, false
	}

	hasVolumes := false
	for _, e := range toc {
		if len(e.Children) > 0 {
			hasVolumes = true
			break
		}
	}

	var bs []boundary
	for _, e := range toc {
		if hasVolumes && len(e.Children) > 0 {
			vol := len(p.out.Volumes)
			p.out.Volumes = append(p.out.Volumes, Volume{Title: e.Title})
			if i, ok := first(e); ok {
				bs = append(bs, boundary{index: i, title: e.Title, volume: vol, volumeStart: true})
			}
			for _, c := range e.Children {
				if i, ok := first(c); ok {
					bs = append(bs, boundary{index: i, title: c.Title, volume: vol})
				}
			}
			continue
		}
		if i, ok := first(e); ok {
			bs = append(bs, boundary{index: i, title: e.Title, volume: -1})
		}
	}
	chapters := 0
	for _, b := range bs {
		if !b.volumeStart {
			chapters++
		}
	}
	if chapters < 2 {
		p.out.Volumes = nil
		return false
	}
	p.boundaries = bs
	return true
}

// fromHeadings splits at the most frequent heading level that occurs at
// least twice (the book title is usually a single h1, chapters many h2).
func (p *planner) fromHeadings() bool {
	counts := map[int]int{}
	for _, b := range p.blocks {
		if b.kind == Heading {
			counts[b.level]++
		}
	}
	best, bestCount := 0, 1
	for level := 1; level <= 6; level++ {
		if counts[level] > bestCount {
			best, bestCount = level, counts[level]
		}
	}
	if best == 0 {
		return false
	}
	for i, b := range p.blocks {
		if b.kind == Heading && b.level == best {
			p.boundaries = append(p.boundaries, boundary{index: i, title: b.text, volume: -1})
		}
	}
	return true
}

func (p *planner) fromDocuments(spine []string, docStarts map[string]int) {
	n := 0
	for k, doc := range spine {
		i, ok := docStarts[doc]
		if !ok {
			continue
		}
		end := len(p.blocks)
		if k+1 < len(spine) {
			if e, ok := docStarts[spine[k+1]]; ok {
				end = e
			}
		}
		n++
		title := "Part " + strconv.Itoa(n)
		// Use a heading near the document's start as title when it has one.
		for j := i; j < end && j < i+5; j++ {
			if p.blocks[j].kind == Heading {
				title = p.blocks[j].text
				break
			}
		}
		p.boundaries = append(p.boundaries, boundary{index: i, title: title, volume: -1})
	}
}

// finish turns boundaries into chapter spans.
func (p *planner) finish() {
	total := len(p.blocks)
	p.out.Blocks = total

	bs := p.boundaries
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].index < bs[j].index })
	// Several boundaries at the same block: a volume start that coincides
	// with its first chapter is redundant; otherwise keep the first.
	dedup := bs[:0]
	for _, b := range bs {
		if n := len(dedup); n > 0 && dedup[n-1].index == b.index {
			if dedup[n-1].volumeStart && !b.volumeStart {
				dedup[n-1] = b
			}
			continue
		}
		dedup = append(dedup, b)
	}
	bs = dedup

	words := func(start, end int) int {
		n := 0
		for _, b := range p.blocks[start:end] {
			n += b.words
		}
		return n
	}

	if len(bs) > 0 && bs[0].index > 0 {
		if w := words(0, bs[0].index); w > 0 {
			p.out.Chapters = append(p.out.Chapters, Chapter{
				Title: "Front matter", Volume: -1, Start: 0, End: bs[0].index, Words: w, Excluded: true,
			})
		}
	}
	for i, b := range bs {
		end := total
		if i+1 < len(bs) {
			end = bs[i+1].index
		}
		if b.index >= end || b.index >= total {
			continue
		}
		w := words(b.index, end)
		if w == 0 || (b.volumeStart && w < minVolumeIntroWords) {
			continue
		}
		title := b.title
		if title == "" {
			title = "Chapter " + strconv.Itoa(len(p.out.Chapters)+1)
		}
		p.out.Chapters = append(p.out.Chapters, Chapter{
			Title:    title,
			Volume:   b.volume,
			Start:    b.index,
			End:      end,
			Words:    w,
			Excluded: IsMatter(title),
		})
	}
}

var matterRe = regexp.MustCompile(`(?i)^\s*(cover|title( page)?|half[- ]title|copyright( page)?|` +
	`(table of )?contents|dedication|epigraph|acknowledge?ments?|about the authors?|` +
	`about the publisher|also by\b.*|other books by\b.*|praise for\b.*|newsletter|` +
	`sign up\b.*|index|colophon|imprint|frontispiece)\s*$`)

// IsMatter reports whether a chapter title looks like front or back matter
// that should not be narrated by default.
func IsMatter(title string) bool {
	return matterRe.MatchString(title)
}
