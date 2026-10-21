// Package attribution decides who voices each passage of a chapter and
// how.
//
// A chapter is attributed just in time, before it is rendered: its
// paragraphs are sent to the LLM, numbered, together with the characters
// that appear in the chapter and the last speakers of the previous one.
// The LLM finds every passage that is not plain narration itself (speech,
// thoughts, telepathy, off-voices, system messages, writing read out,
// song), however the book marks it, and names its speaker, emotion and a
// spoken-delivery direction; it also gives directions for narration that
// should be read differently. It points at passages by their first and
// last words only — the text itself is never rewritten: code finds the
// words in the paragraph and splits it there (no character is lost), and
// passages it cannot find stay narration. Speakers the model names but
// the bible lacks ("new:") become minor characters. Long chapters are
// sent in parts (preferably split at scene breaks, with a few paragraphs
// of context); each part is stored as soon as it is answered, so a
// stopped job resumes with the next part and never spends quota twice.
package attribution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/segment"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// JobType attributes one chapter; it runs on the LLM queue.
const (
	JobType = "attribute_chapter"
	Queue   = "llm"
)

// Defaults.
const (
	DefaultPartTokens = 12000
	// UnsureBelow is the confidence under which a speaker is flagged for
	// review.
	UnsureBelow = 0.6
	// contextParas precede a part (after the first) as context.
	contextParas = 3
	// maxCharacters bounds the characters listed per request.
	maxCharacters = 120
	// outputTokens: every passage costs about 60 tokens of answer.
	outputTokens  = 16384
	tokensPerWord = 1.35
	// maxDirection bounds a direction, in runes.
	maxDirection = 160
)

// Emotions the prompt offers.
var emotions = map[string]bool{
	"neutral": true, "happy": true, "sad": true, "angry": true, "afraid": true, "surprised": true,
	"whispering": true, "shouting": true, "sarcastic": true,
}

// Completer sends structured requests (llm.Pool).
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, out any) (*llm.Result, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Attributor runs speaker attributions.
type Attributor struct {
	Store *store.Store
	LLM   Completer
	Jobs  Enqueuer
	// PartTokens is the input size of one request.
	PartTokens int
	Log        *slog.Logger
}

var prompt = llm.MustLoadPrompt("attribute_chapter")

// PromptID is the prompt version attributions use.
func PromptID() string { return prompt.ID() }

func (a *Attributor) partTokens() int {
	if a.PartTokens > 0 {
		return a.PartTokens
	}
	return DefaultPartTokens
}

// Errors of Start.
var (
	ErrBusy     = errors.New("the speakers of this chapter are being attributed")
	ErrNotReady = fmt.Errorf("%w: the character analysis has not finished", store.ErrInvalid)
)

// Active reports whether a chapter's attribution is queued or running.
func Active(c *store.Chapter) bool {
	return c.AttributionStatus == store.AttributionQueued || c.AttributionStatus == store.AttributionRunning
}

// Needed reports whether a chapter must be attributed before it is
// rendered with character voices: the book has a finished analysis, the
// chapter has text, and it was not attributed (or failed) yet. Chapters
// without quotation marks are attributed too: the LLM finds speech and
// thoughts however they are set.
func Needed(b *store.Book, c *store.Chapter) bool {
	return b.AnalysisStatus == store.AnalysisDone && c.WordCount > 0 && !c.Excluded &&
		c.AttributionStatus != store.AttributionDone && c.AttributionStatus != store.AttributionFailed
}

// Start queues the attribution of a chapter. With force, the automatic
// speakers are discarded and the chapter is attributed again (speakers the
// user chose stay). priority orders the job on the LLM queue.
func (a *Attributor) Start(ctx context.Context, b *store.Book, c *store.Chapter, force bool, priority int) error {
	if b.AnalysisStatus != store.AnalysisDone {
		return ErrNotReady
	}
	pending, err := a.Store.PendingChapterJob(ctx, JobType, c.ID)
	if err != nil {
		return err
	}
	if pending != nil {
		if force {
			return ErrBusy
		}
		return nil
	}
	if force {
		if err := a.Store.ResetAttribution(ctx, c.ID); err != nil {
			return err
		}
	} else if c.AttributionStatus == store.AttributionDone {
		return nil
	}
	// From scratch after a reset; a failed or interrupted one resumes.
	fresh, err := a.Store.TransitionAttribution(ctx, c.ID, store.AttributionQueued,
		map[string]any{"attribution_error": "", "attribution_part": 0}, store.AttributionNone)
	if err != nil {
		return err
	}
	if !fresh {
		ok, err := a.Store.TransitionAttribution(ctx, c.ID, store.AttributionQueued,
			map[string]any{"attribution_error": ""},
			store.AttributionFailed, store.AttributionQueued, store.AttributionRunning)
		if err != nil || !ok {
			return err
		}
	}
	return a.Jobs.Enqueue(ctx, &store.Job{
		Type: JobType, BookID: &b.ID, ChapterID: &c.ID, Priority: priority, MaxAttempts: 5,
	})
}

type partDoneKey struct{}

// Handler is the job handler for JobType.
func (a *Attributor) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.ChapterID == nil {
				return jobs.Permanent(errors.New("attribution job without chapter"))
			}
			// Attempts count failures of the current part.
			ctx = context.WithValue(ctx, partDoneKey{}, func(ctx context.Context) {
				j.Attempts = 1
				if err := a.Store.ResetJobAttempts(ctx, j.ID); err != nil {
					a.Log.Warn("reset job attempts", "job", j.ID, "err", err)
				}
			})
			return a.Run(ctx, *j.ChapterID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.ChapterID == nil {
				return
			}
			// A newer start queued another job: that one decides.
			if p, perr := a.Store.PendingChapterJob(ctx, JobType, *j.ChapterID); perr == nil && p != nil && p.ID != j.ID {
				return
			}
			if _, uerr := a.Store.TransitionAttribution(ctx, *j.ChapterID, store.AttributionFailed,
				map[string]any{"attribution_error": store.CleanText(err.Error())},
				store.AttributionQueued, store.AttributionRunning); uerr != nil {
				a.Log.Error("mark attribution failed", "chapter", *j.ChapterID, "err", uerr)
			}
		},
	}
}

// Run attributes the remaining parts of a chapter.
func (a *Attributor) Run(ctx context.Context, chapterID int64, progress func(float64)) error {
	ch, err := a.Store.GetChapter(ctx, chapterID)
	if err != nil {
		return jobs.Permanent(err)
	}
	b, err := a.Store.GetBook(ctx, ch.BookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	switch b.AnalysisStatus {
	case store.AnalysisDone:
	case store.AnalysisNone, store.AnalysisFailed:
		return jobs.Permanent(errors.New("the character analysis has not finished"))
	default:
		// Analysed again (or paused meanwhile): wait for it, the bible
		// changes under it.
		return jobs.Defer(time.Now().Add(analysisWait), "waiting for the character analysis")
	}
	if ch.Excluded {
		_, err := a.Store.TransitionAttribution(ctx, chapterID, store.AttributionNone, nil,
			store.AttributionQueued, store.AttributionRunning)
		return err // not narrated: nothing to attribute
	}
	if ok, err := a.Store.TransitionAttribution(ctx, chapterID, store.AttributionRunning, nil,
		store.AttributionQueued, store.AttributionRunning); err != nil || !ok {
		return err // reset or done meanwhile
	}
	paras, segs, err := a.Store.ChapterText(ctx, chapterID)
	if err != nil {
		return err
	}
	// Resume after the paragraphs already attributed.
	done := ch.AttributionPart
	var parts []part
	for _, p := range split(paras, a.partTokens()) {
		if p.to <= done {
			continue
		}
		p.from = max(p.from, done)
		parts = append(parts, p)
	}
	ctxData, err := a.loadContext(ctx, b, ch)
	if err != nil {
		return err
	}
	for i, p := range parts {
		progress(float64(i) / float64(len(parts)))
		res, err := a.attribute(ctx, ch, ctxData, paras, segs, p.from, p.to, p.from-min(p.from, contextParas))
		if err != nil {
			return err
		}
		as, err := a.store(ctx, ch, ctxData, res, done, p.to)
		if errors.Is(err, store.ErrStale) {
			a.Log.Info("attribution reset meanwhile, stopping", "chapter", chapterID)
			return nil
		}
		if err != nil {
			return err
		}
		done = p.to
		ctxData.remember(as)
		if f, ok := ctx.Value(partDoneKey{}).(func(context.Context)); ok {
			f(ctx)
		}
	}
	progress(1)
	_, err = a.Store.MarkAttributed(ctx, chapterID, prompt.ID())
	return err
}

// analysisWait is how long an attribution waits for an analysis that runs
// again.
const analysisWait = 10 * time.Minute

// store saves a part's answers in one transaction with the speakers it
// names for the first time ("new:"), only if the attribution was not reset
// meanwhile (store.ErrStale). It returns the speakers of the part's
// passages, in order.
func (a *Attributor) store(ctx context.Context, ch *store.Chapter, c *chapterContext, res []scripted,
	from, to int,
) ([]*int64, error) {
	var speakers []*int64
	err := a.Store.Tx(ctx, func(tx *store.Store) error {
		speakers = speakers[:0]
		ps := make([]store.ScriptParagraph, len(res))
		var created []store.Character
		for i, r := range res {
			ps[i] = r.ScriptParagraph
			ps[i].Segments = slices.Clone(r.Segments)
			for k := range ps[i].Segments {
				sg := &ps[i].Segments[k]
				if sg.Quote == 0 {
					continue
				}
				if nn, ok := r.newSpeakers[sg.Quote]; ok {
					id, nc, err := a.newSpeaker(ctx, tx, ch, c, nn.name, nn.gender)
					if err != nil {
						return err
					}
					sg.CharacterID = id
					if nc != nil {
						created = append(created, *nc)
					}
				}
				if k == 0 || ps[i].Segments[k-1].Quote != sg.Quote {
					speakers = append(speakers, sg.CharacterID)
				}
			}
		}
		if err := tx.ApplyScript(ctx, ch.ID, ps, from, to); err != nil {
			return err
		}
		for _, nc := range created {
			a.Log.Info("new character from attribution", "chapter", ch.ID, "name", nc.Name)
		}
		return nil
	})
	return speakers, err
}

// part is a range of paragraphs [from, to) sent in one request.
type part struct{ from, to int }

func paraTokens(p store.Paragraph) int { return int(float64(p.WordCount)*tokensPerWord) + 4 }

// split cuts the chapter into parts of about budget tokens that contain
// text, preferring scene breaks once a part is half full.
func split(paras []store.Paragraph, budget int) []part {
	var out []part
	start, tokens, hasText := 0, 0, false
	flush := func(end int) {
		if hasText {
			out = append(out, part{start, end})
		}
		start, tokens, hasText = end, 0, false
	}
	for i, p := range paras {
		t := paraTokens(p)
		if tokens > 0 && (tokens+t > budget || p.Kind == store.ParagraphSceneBreak && tokens > budget/2) {
			flush(i)
		}
		tokens += t
		hasText = hasText || p.Kind == store.ParagraphText && p.WordCount > 0
	}
	flush(len(paras))
	return out
}

// chapterContext is what every request of a chapter shares.
type chapterContext struct {
	chars    []store.Character
	aliases  map[int64][]store.CharacterAlias
	listed   map[int64]bool
	byNorm   map[string][]int64
	previous []store.Speaker
	names    map[int64]string
	pov      string
}

func (a *Attributor) loadContext(ctx context.Context, b *store.Book, ch *store.Chapter) (*chapterContext, error) {
	all, err := a.Store.BookCharacters(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	aliases, err := a.Store.BookAliases(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	appear, err := a.Store.ChapterCharacters(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	previous, err := a.Store.LastSpeakers(ctx, b.ID, ch.Idx, 5)
	if err != nil {
		return nil, err
	}
	c := &chapterContext{aliases: map[int64][]store.CharacterAlias{}, listed: map[int64]bool{},
		byNorm: map[string][]int64{}, previous: previous, names: map[int64]string{}}
	byID := map[int64]*store.Character{}
	for i := range all {
		byID[all[i].ID] = &all[i]
		c.names[all[i].ID] = all[i].Name
		c.addNorm(all[i].ID, all[i].Name)
	}
	for _, al := range aliases {
		c.aliases[al.CharacterID] = append(c.aliases[al.CharacterID], al)
		c.byNorm[al.Norm] = appendUnique(c.byNorm[al.Norm], al.CharacterID)
	}
	// The characters of the chapter, the point-of-view character and the
	// previous speakers (the scene may continue).
	add := func(id int64) {
		if ch := byID[id]; ch != nil && !c.listed[id] && len(c.chars) < maxCharacters {
			c.listed[id] = true
			c.chars = append(c.chars, *ch)
		}
	}
	if ch.POVCharacterID != nil {
		add(*ch.POVCharacterID)
		if pov := byID[*ch.POVCharacterID]; pov != nil {
			c.pov = fmt.Sprintf("%s (c%d)", pov.Name, pov.ID)
		}
	}
	for _, s := range previous {
		add(s.CharacterID)
	}
	for _, x := range appear {
		add(x.ID)
	}
	return c, nil
}

func (c *chapterContext) addNorm(id int64, name string) {
	if n := store.NormName(name); n != "" {
		c.byNorm[n] = appendUnique(c.byNorm[n], id)
	}
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

// remember makes the last speakers of a part the context of the next.
func (c *chapterContext) remember(speakers []*int64) {
	var prev []store.Speaker
	seen := map[int64]bool{}
	for i := len(speakers) - 1; i >= 0 && len(prev) < 5; i-- {
		if id := speakers[i]; id != nil && !seen[*id] {
			seen[*id] = true
			prev = append(prev, store.Speaker{CharacterID: *id, Name: c.names[*id]})
		}
	}
	if len(prev) > 0 {
		c.previous = prev
	}
}

// maxSpeechStyle bounds the speech style listed per character, in runes.
const maxSpeechStyle = 120

func (c *chapterContext) lines() []string {
	out := make([]string, 0, len(c.chars))
	for _, ch := range c.chars {
		var as []string
		for _, al := range c.aliases[ch.ID] {
			as = append(as, al.Alias)
		}
		out = append(out, fmt.Sprintf("c%d | %s | %s | %s | %s | %s", ch.ID, ch.Name, strings.Join(as, ", "),
			ch.Gender, ch.AgeGroup, oneLine(ch.SpeechStyle, maxSpeechStyle)))
	}
	return out
}

// oneLine collapses spaces and line breaks and cuts s to n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// emphasisOf finds the italic runs of a paragraph (in runes) from those
// of its segments, which hold its text in order.
func emphasisOf(p store.Paragraph, segs []store.Segment) []segment.Span {
	text := []rune(p.Text)
	var out []segment.Span
	at := 0
	for _, s := range segs {
		st := []rune(s.Text)
		i := indexRunes(text, st, at)
		if i < 0 {
			continue
		}
		for _, sp := range store.DecodeSpans(s.Emphasis, len(st)) {
			out = append(out, segment.Span{Start: i + sp[0], End: i + sp[1]})
		}
		at = i + len(st)
	}
	return out
}

func indexRunes(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if string(s[i:i+len(sub)]) == string(sub) {
			return i
		}
	}
	return -1
}

// italic sets a paragraph's italic runs between asterisks.
func italic(text string, emph []segment.Span) string {
	if len(emph) == 0 {
		return text
	}
	r := []rune(text)
	var b strings.Builder
	at := 0
	for _, e := range emph {
		if e.Start < at || e.End > len(r) {
			continue
		}
		b.WriteString(string(r[at:e.Start]))
		b.WriteString("*" + string(r[e.Start:e.End]) + "*")
		at = e.End
	}
	b.WriteString(string(r[at:]))
	return b.String()
}

// numbered renders paragraphs [from, to) numbered [pN] (N is the index
// plus one) with their italics; context paragraphs [ctxFrom, from) are
// rendered without numbers.
func numbered(paras []store.Paragraph, byPara map[int64][]store.Segment, ctxFrom, from, to int,
) (context, text string, count int) {
	var cb, tb strings.Builder
	for i := ctxFrom; i < to; i++ {
		p := paras[i]
		b := &cb
		if i >= from {
			b = &tb
		}
		switch p.Kind {
		case store.ParagraphSceneBreak:
			b.WriteString("* * *\n\n")
		case store.ParagraphHeading:
			b.WriteString("# " + p.Text + "\n\n")
		default:
			if i >= from {
				fmt.Fprintf(b, "[p%d] ", i+1)
				count++
			}
			b.WriteString(italic(p.Text, emphasisOf(p, byPara[p.ID])))
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(cb.String()), strings.TrimSpace(tb.String()), count
}

type voiceAnswer struct {
	P          int     `json:"p"`
	Start      string  `json:"start"`
	End        string  `json:"end"`
	Kind       string  `json:"kind"`
	Speaker    string  `json:"speaker"`
	Gender     string  `json:"gender"`
	Emotion    string  `json:"emotion"`
	Direction  string  `json:"direction"`
	Confidence float64 `json:"confidence"`
}

type answer struct {
	Voices    []voiceAnswer `json:"voices"`
	Narration []struct {
		P         int    `json:"p"`
		Direction string `json:"direction"`
	} `json:"narration"`
}

// attribute asks for the passages of paragraphs [from, to), splitting the
// range when the request or answer is too large; a passage a content
// filter still refuses on its own keeps its segments, read by the
// narrator.
func (a *Attributor) attribute(ctx context.Context, ch *store.Chapter, c *chapterContext,
	paras []store.Paragraph, segs []store.Segment, from, to, ctxFrom int,
) ([]scripted, error) {
	byPara := map[int64][]store.Segment{}
	for _, s := range segs {
		byPara[s.ParagraphID] = append(byPara[s.ParagraphID], s)
	}
	contextText, text, count := numbered(paras, byPara, ctxFrom, from, to)
	if count == 0 {
		return nil, nil
	}
	prev := make([]string, 0, len(c.previous))
	for _, s := range c.previous {
		prev = append(prev, fmt.Sprintf("%s (c%d)", s.Name, s.CharacterID))
	}
	req, err := prompt.Render(map[string]any{
		"Characters": c.lines(), "POV": c.pov, "Previous": strings.Join(prev, ", "), "Title": ch.Title,
		"Context": contextText, "Count": count, "Text": text,
	})
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	req.MaxTokens = outputTokens
	var ans answer
	_, err = a.LLM.CompleteJSON(ctx, req, &ans)
	var (
		tooLarge *llm.TooLargeError
		invalid  *llm.InvalidOutputError
		rejected *llm.RejectedError
	)
	blocked := (errors.As(err, &invalid) && invalid.Blocked) || (errors.As(err, &rejected) && rejected.Blocked())
	if blocked || errors.As(err, &tooLarge) || (errors.As(err, &invalid) && invalid.Truncated) ||
		(errors.As(err, &rejected) && rejected.TooLong()) {
		if to-from < 2 {
			a.Log.Warn("passage cannot be attributed, it stays with the narrator",
				"chapter", ch.ID, "paragraph", from, "err", err)
			return nil, nil
		}
		mid := from + (to-from)/2
		first, err := a.attribute(ctx, ch, c, paras, segs, from, mid, ctxFrom)
		if err != nil {
			return nil, err
		}
		second, err := a.attribute(ctx, ch, c, paras, segs, mid, to, mid-min(mid-from, contextParas))
		if err != nil {
			return nil, err
		}
		return append(first, second...), nil
	}
	if errors.As(err, &invalid) {
		a.Log.Warn("attribution answer unusable, the part stays with the narrator", "chapter", ch.ID, "err", err)
		return nil, nil
	}
	if errors.As(err, &rejected) {
		return nil, jobs.Permanent(err)
	}
	if err != nil {
		return nil, err
	}
	res, missed := apply(c, paras, byPara, from, to, &ans)
	if missed > 0 {
		a.Log.Info("passages not found in the text stay narration", "chapter", ch.ID, "count", missed)
	}
	return res, nil
}

// scripted is a paragraph split anew, possibly naming speakers the bible
// lacks (by the number of their passage).
type scripted struct {
	store.ScriptParagraph
	newSpeakers map[int]newName
}

type newName struct{ name, gender string }

// voicingOf normalizes a passage kind ("" when unknown).
func voicingOf(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case "off-voice", "offvoice", "voice_over", "voice-over", "voiceover", "phone", "radio", "recording":
		return store.VoicingRemote
	case "telepathic", "mental", "mind":
		return store.VoicingTelepathy
	case "message", "announcement", "notification":
		return store.VoicingSystem
	case "letter", "sign", "written", "text":
		return store.VoicingWriting
	case "singing", "sung", "chant":
		return store.VoicingSong
	}
	if slices.Contains(store.Voicings, k) {
		return k
	}
	return ""
}

// direction cleans a direction: one line, no quotation of its own, at
// most maxDirection runes.
func direction(s string) string {
	s = strings.Trim(oneLine(s, maxDirection), " \"'“”‘’.:;")
	if strings.EqualFold(s, "neutral") || strings.EqualFold(s, "none") {
		return ""
	}
	return s
}

// apply validates an answer and splits paragraphs [from, to) by the
// passages found in them (every text paragraph of the range is listed,
// one without passages as narration only). Passages whose words are not
// found, overlap another or name a paragraph outside the range are left
// out (missed counts them).
func apply(c *chapterContext, paras []store.Paragraph, byPara map[int64][]store.Segment, from, to int,
	ans *answer,
) (out []scripted, missed int) {
	voices := map[int][]voiceAnswer{}
	for _, v := range ans.Voices {
		i := v.P - 1
		if i < from || i >= to || paras[i].Kind != store.ParagraphText {
			missed++
			continue
		}
		voices[i] = append(voices[i], v)
	}
	directions := map[int]string{}
	for _, n := range ans.Narration {
		if i := n.P - 1; i >= from && i < to {
			directions[i] = direction(n.Direction)
		}
	}
	for i := from; i < to; i++ {
		p := paras[i]
		if p.Kind != store.ParagraphText || p.WordCount == 0 {
			continue
		}
		type found struct {
			span segment.Span
			v    voiceAnswer
		}
		var fs []found
		overlaps := func(sp segment.Span) bool {
			for _, f := range fs {
				if sp.Start < f.span.End && f.span.Start < sp.End {
					return true
				}
			}
			return false
		}
		cursor := 0
		for _, v := range voices[i] {
			sp, ok := segment.Locate(p.Text, cursor, v.Start, v.End)
			if !ok || overlaps(sp) {
				// Out of order: anywhere it does not overlap.
				sp, ok = segment.Locate(p.Text, 0, v.Start, v.End)
			}
			if !ok || overlaps(sp) {
				missed++
				continue
			}
			fs = append(fs, found{sp, v})
			cursor = max(cursor, sp.End)
		}
		slices.SortFunc(fs, func(a, b found) int { return a.span.Start - b.span.Start })
		spans := make([]segment.Voiced, len(fs))
		for k, f := range fs {
			kind := segment.Quote
			if vc := voicingOf(f.v.Kind); vc == store.VoicingThought || vc == store.VoicingTelepathy {
				kind = segment.Thought
			}
			spans[k] = segment.Voiced{Span: f.span, Kind: kind}
		}
		sp := scripted{ScriptParagraph: store.ScriptParagraph{ParagraphID: p.ID, Direction: directions[i]}}
		for _, part := range segment.Script(p.Text, emphasisOf(p, byPara[p.ID]), spans, 1) {
			var label store.ScriptSegment
			if part.Quote > 0 {
				v := fs[part.Quote-1].v
				var nn *newName
				label, nn = labelOf(c, v)
				if nn != nil {
					if sp.newSpeakers == nil {
						sp.newSpeakers = map[int]newName{}
					}
					sp.newSpeakers[part.Quote] = *nn
				}
			}
			for _, piece := range segment.ChunkPart(part, segment.MaxChunk) {
				sg := label
				sg.Kind, sg.Text, sg.Quote = store.SegmentNarration, piece.Text, piece.Quote
				switch part.Kind {
				case segment.Quote:
					sg.Kind = store.SegmentQuote
				case segment.Thought:
					sg.Kind = store.SegmentThought
				}
				spans := make([][2]int, len(piece.Emphasis))
				for k, e := range piece.Emphasis {
					spans[k] = [2]int{e.Start, e.End}
				}
				sg.Emphasis = store.EncodeSpans(spans)
				sp.Segments = append(sp.Segments, sg)
			}
		}
		out = append(out, sp)
	}
	return out, missed
}

// labelOf turns a passage of the answer into a segment's labels; a
// "new:" speaker the bible lacks is returned to be created.
func labelOf(c *chapterContext, v voiceAnswer) (store.ScriptSegment, *newName) {
	at := store.ScriptSegment{
		Voicing: voicingOf(v.Kind), Emotion: "neutral", Direction: direction(v.Direction),
		Confidence: clamp(v.Confidence),
	}
	if at.Voicing == "" {
		at.Voicing = store.VoicingSpeech
	}
	if e := strings.ToLower(strings.TrimSpace(v.Emotion)); emotions[e] {
		at.Emotion = e
	}
	var nn *newName
	speaker := strings.TrimSpace(v.Speaker)
	switch {
	case strings.EqualFold(speaker, "unknown") || speaker == "":
	case strings.HasPrefix(strings.ToLower(speaker), "new:"):
		name := strings.TrimSpace(speaker[4:])
		norm := store.NormName(name)
		switch ids := c.byNorm[norm]; {
		case norm == "" || utf8.RuneCountInString(name) > 120 || len(ids) > 1:
			// unusable or ambiguous: the narrator reads it
		case len(ids) == 1:
			id := ids[0]
			at.CharacterID = &id
		default:
			nn = &newName{name, v.Gender}
		}
	default:
		id, err := strconv.ParseInt(strings.TrimPrefix(speaker, "c"), 10, 64)
		if err == nil && c.listed[id] {
			at.CharacterID = &id
		} else {
			at.Confidence = 0 // an id that was not offered
		}
	}
	// No one to voice it: flagged for review, unless no one is expected
	// (a system message).
	if at.CharacterID == nil && nn == nil && at.Voicing != store.VoicingSystem {
		at.Confidence = min(at.Confidence, UnsureBelow/2)
	}
	return at, nn
}

func clamp(f float64) float64 {
	if math.IsNaN(f) {
		return 0
	}
	return min(max(f, 0), 1)
}

// newSpeaker adds a speaker the bible lacks as a minor character of this
// chapter (within tx); one already added by an earlier part is reused.
func (a *Attributor) newSpeaker(ctx context.Context, tx *store.Store, ch *store.Chapter, c *chapterContext,
	name, gender string,
) (*int64, *store.Character, error) {
	if ids := c.byNorm[store.NormName(name)]; len(ids) == 1 {
		id := ids[0]
		return &id, nil, nil
	}
	g := store.Gender(strings.ToLower(gender))
	if g != store.GenderFemale && g != store.GenderMale {
		g = store.GenderUnknown
	}
	nc := &store.Character{BookID: ch.BookID, Name: name, Gender: g, AgeGroup: store.AgeUnknown,
		Importance: 1, Speaks: true, Tier: store.TierMinor}
	if err := tx.CreateCharacter(ctx, nc); err != nil {
		return nil, nil, err
	}
	if err := tx.LinkCharacterChapters(ctx, ch.BookID, nc.ID, ch.ID); err != nil {
		return nil, nil, err
	}
	if err := tx.RefreshCharacterStats(ctx, nc.ID); err != nil {
		return nil, nil, err
	}
	c.listed[nc.ID] = true
	c.names[nc.ID] = name
	c.addNorm(nc.ID, name)
	return &nc.ID, nc, nil
}

// pollEvery is how often a render waiting for its attribution looks again.
const pollEvery = 30 * time.Second

// WaitError defers a render until its chapter is attributed.
func WaitError() error {
	return jobs.Defer(time.Now().Add(pollEvery), "waiting for the speaker attribution of this chapter")
}
