// Package casting gives a book's characters voices.
//
// Main characters (the most important speaking ones) get their own voice
// recipe: the LLM proposes a blend of Kokoro base voices for each, and code
// makes sure it fits — the character's gender, a clear distance from the
// narrator and from every other recipe — and otherwise picks the nearest
// blend that does. Minor characters get a pool voice by gender and age,
// picked by hash (PoolVoice). Recipes the user chose are never touched.
package casting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/llm"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/tts"
)

// JobType casts a book; it runs on the LLM queue.
const (
	JobType = "cast_book"
	Queue   = "llm"
)

// Priority puts a casting before the speaker attributions on the LLM queue
// (renders wait for it; it is a handful of requests).
const Priority = 300

// Casting rules.
const (
	DefaultMainCharacters = 50
	// MinDistance separates two characters' recipes, NarratorDistance a
	// character's recipe from the narrator (see Distance).
	MinDistance      = 0.3
	NarratorDistance = 0.6
	MinSpeed         = 0.85
	MaxSpeed         = 1.15
	// groupSize is the number of characters per LLM request.
	groupSize = 8
)

// Completer sends structured requests (llm.Pool).
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, out any) (*llm.Result, error)
}

// Enqueuer schedules jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, j *store.Job) error
}

// Caster runs castings.
type Caster struct {
	Store   *store.Store
	LLM     Completer
	Jobs    Enqueuer
	Catalog *Catalog
	// DefaultVoice is the narrator of books without their own.
	DefaultVoice string
	// MainCharacters is how many speaking characters get their own recipe.
	MainCharacters int
	Log            *slog.Logger
}

var castPrompt = llm.MustLoadPrompt("cast_voices")

// PromptID is the prompt version castings use.
func PromptID() string { return castPrompt.ID() }

func (c *Caster) mainCharacters() int {
	if c.MainCharacters > 0 {
		return c.MainCharacters
	}
	return DefaultMainCharacters
}

// Errors of Start.
var (
	ErrBusy     = errors.New("the voice casting of this book is in progress")
	ErrNotReady = fmt.Errorf("%w: the character analysis has not finished", store.ErrInvalid)
)

// Active reports whether a casting is queued or running.
func Active(b *store.Book) bool {
	return b.CastingStatus == store.CastingQueued || b.CastingStatus == store.CastingRunning
}

// Narrator returns a book's effective narrator recipe and speed.
func Narrator(b *store.Book, defaultVoice string) (string, float64) {
	voice, speed := b.NarratorVoice, b.NarratorSpeed
	if voice == "" {
		voice = defaultVoice
	}
	if speed <= 0 {
		speed = 1
	}
	return voice, speed
}

// Start queues the casting of a book: main characters without a voice get
// one. With restart, all voices and tiers the user did not choose are
// cast anew.
func (c *Caster) Start(ctx context.Context, b *store.Book, restart bool) error {
	if b.AnalysisStatus != store.AnalysisDone {
		return ErrNotReady
	}
	pending, err := c.Store.PendingBookJob(ctx, JobType, b.ID)
	if err != nil {
		return err
	}
	if pending != nil {
		// A restart must not reset voices under a casting at work; a job
		// that is only waiting (its work done, or not begun) is fine.
		if restart && Active(b) {
			return ErrBusy
		}
		if restart {
			if err := c.Store.ResetCasting(ctx, b.ID); err != nil {
				return err
			}
		}
		// A queued job picks up what is new. A running one sees the
		// "queued" status when it finishes and runs again, but it may be
		// finishing right now: queue another job too (it does nothing if
		// the first one already did the work).
		if err := c.Store.UpdateBook(ctx, b.ID, map[string]any{
			"CastingStatus": store.CastingQueued, "CastingError": "",
		}); err != nil || pending.Status != store.JobRunning {
			return err
		}
		return c.Jobs.Enqueue(ctx, &store.Job{Type: JobType, BookID: &b.ID, Priority: Priority, MaxAttempts: 5})
	}
	if restart {
		if err := c.Store.ResetCasting(ctx, b.ID); err != nil {
			return err
		}
	}
	if err := c.Store.UpdateBook(ctx, b.ID, map[string]any{
		"CastingStatus": store.CastingQueued, "CastingError": "",
	}); err != nil {
		return err
	}
	return c.Jobs.Enqueue(ctx, &store.Job{Type: JobType, BookID: &b.ID, Priority: Priority, MaxAttempts: 5})
}

// Handler is the job handler for JobType.
func (c *Caster) Handler() jobs.Handler {
	return jobs.Handler{
		Queue: Queue,
		Run: func(ctx context.Context, j *store.Job, progress func(float64)) error {
			if j.BookID == nil {
				return jobs.Permanent(errors.New("casting job without book"))
			}
			// Attempts count failures of the current group, not of the run.
			ctx = context.WithValue(ctx, groupDoneKey{}, func(ctx context.Context) {
				j.Attempts = 1
				if err := c.Store.ResetJobAttempts(ctx, j.ID); err != nil {
					c.Log.Warn("reset job attempts", "job", j.ID, "err", err)
				}
			})
			return c.Run(ctx, *j.BookID, progress)
		},
		OnFail: func(ctx context.Context, j *store.Job, err error) {
			if j.BookID == nil {
				return
			}
			// A newer start queued another job: that one decides.
			if p, perr := c.Store.PendingBookJob(ctx, JobType, *j.BookID); perr == nil && p != nil && p.ID != j.ID {
				return
			}
			if _, uerr := c.Store.TransitionCasting(ctx, *j.BookID, store.CastingFailed,
				map[string]any{"casting_error": store.CleanText(err.Error())},
				store.CastingQueued, store.CastingRunning); uerr != nil {
				c.Log.Error("mark casting failed", "book", *j.BookID, "err", uerr)
			}
		},
	}
}

type groupDoneKey struct{}

// ranked returns the speaking characters whose tier the user did not
// choose, most important first.
func ranked(chars []store.Character) []*store.Character {
	var out []*store.Character
	for i := range chars {
		if chars[i].Speaks && !chars[i].TierManual {
			out = append(out, &chars[i])
		}
	}
	slices.SortStableFunc(out, byImportance)
	return out
}

func byImportance(a, b *store.Character) int {
	switch {
	case a.Speaks != b.Speaks:
		if a.Speaks {
			return -1
		}
		return 1
	case a.Importance != b.Importance:
		return b.Importance - a.Importance
	case a.ChapterCount != b.ChapterCount:
		return b.ChapterCount - a.ChapterCount
	}
	return int(a.ID - b.ID)
}

// taken is a recipe in use.
type taken struct {
	name   string
	recipe Recipe
	speed  float64
}

// Run casts the main characters of a book that have no voice yet.
func (c *Caster) Run(ctx context.Context, bookID int64, progress func(float64)) error {
	b, err := c.Store.GetBook(ctx, bookID)
	if err != nil {
		return jobs.Permanent(err)
	}
	if b.AnalysisStatus != store.AnalysisDone {
		return jobs.Permanent(errors.New("the character analysis has not finished"))
	}
	if ok, err := c.Store.TransitionCasting(ctx, bookID, store.CastingRunning, nil,
		store.CastingQueued, store.CastingRunning); err != nil || !ok {
		return err
	}
	for {
		// The characters speak with the narrator's engine (asked every
		// round: the book may have switched engines meanwhile).
		narratorVoice, _ := Narrator(b, c.DefaultVoice)
		voices, err := c.Catalog.VoicesFor(ctx, narratorVoice)
		if errors.Is(err, tts.ErrUnknownEngine) {
			return jobs.Permanent(fmt.Errorf("the narrator's engine is not configured: %w", err))
		}
		if err != nil {
			return err // the server is needed to know what it offers
		}
		castable := Castable(voices)
		if len(castable) == 0 {
			return jobs.Permanent(fmt.Errorf("the engine of the narrator (%s) offers no English voices of a known gender",
				tts.EngineOf(narratorVoice)))
		}
		if err := c.cast(ctx, b, castable, progress); err != nil {
			return err
		}
		if ok, err := c.Store.MarkCastingDone(ctx, bookID); err != nil || ok {
			return err
		}
		// Started again while running (a character was promoted): run
		// once more for what is new.
		if ok, err := c.Store.TransitionCasting(ctx, bookID, store.CastingRunning, nil,
			store.CastingQueued); err != nil || !ok {
			return err
		}
		if b, err = c.Store.GetBook(ctx, bookID); err != nil {
			return err
		}
	}
}

// cast gives the main characters without a voice one.
func (c *Caster) cast(ctx context.Context, b *store.Book, castable []BaseVoice, progress func(float64)) error {
	bookID := b.ID
	narratorVoice, narratorSpeed := Narrator(b, c.DefaultVoice)
	narrator, err := ParseRecipe(narratorVoice)
	if err != nil {
		// A narrator saved before recipes were checked this strictly.
		if narrator, err = ParseRecipe(c.DefaultVoice); err != nil {
			return jobs.Permanent(fmt.Errorf("narrator voice %q: %w", narratorVoice, err))
		}
	}

	chars, err := c.Store.BookCharacters(ctx, bookID)
	if err != nil {
		return err
	}
	top := ranked(chars)
	top = top[:min(len(top), c.mainCharacters())]
	ids := make([]int64, len(top))
	for i, ch := range top {
		ids[i] = ch.ID
	}
	if err := c.Store.PromoteCharacters(ctx, ids...); err != nil {
		return err
	}
	// Voices cast from another engine (the book switched, or a casting
	// was running when it did) are cast again.
	engine := tts.EngineOf(narratorVoice)
	var stale []int64
	for _, ch := range chars {
		if ch.VoiceRecipe != "" && !ch.VoiceManual && tts.EngineOf(ch.VoiceRecipe) != engine {
			stale = append(stale, ch.ID)
		}
	}
	if err := c.Store.ClearCastVoices(ctx, stale...); err != nil {
		return err
	}
	if chars, err = c.Store.BookCharacters(ctx, bookID); err != nil {
		return err
	}
	// The most important characters choose first.
	var todo []*store.Character
	var used []taken
	for i := range chars {
		ch := &chars[i]
		if ch.Tier == store.TierMain && ch.VoiceRecipe == "" && !ch.VoiceManual {
			todo = append(todo, ch)
		}
		if ch.VoiceRecipe != "" {
			if r, err := ParseRecipe(ch.VoiceRecipe); err == nil {
				used = append(used, taken{ch.Name, r, ch.VoiceSpeed})
			}
		}
	}

	slices.SortStableFunc(todo, byImportance)
	for start := 0; start < len(todo); start += groupSize {
		progress(float64(start) / float64(len(todo)))
		group := todo[start:min(start+groupSize, len(todo))]
		proposals, err := c.propose(ctx, group, castable, narrator, narratorSpeed, used)
		if err != nil {
			return err
		}
		for _, ch := range group {
			r, speed, note := choose(castable, ch, proposals[ch.ID], narrator, narratorSpeed, used)
			if r == nil {
				continue
			}
			ok, err := c.Store.AssignVoice(ctx, ch.ID, ch.Gender, r.String(), speed, note)
			if err != nil {
				return err
			}
			if ok {
				used = append(used, taken{ch.Name, r, speed})
			}
		}
		if done, ok := ctx.Value(groupDoneKey{}).(func(context.Context)); ok {
			done(ctx)
		}
	}
	progress(1)
	c.Log.Info("casting done", "book", bookID, "cast", len(todo))
	return nil
}

// proposal is the LLM's suggestion for one character.
type proposal struct {
	Ref   string `json:"ref"`
	Parts []struct {
		Voice  string  `json:"voice"`
		Weight float64 `json:"weight"`
	} `json:"parts"`
	Speed  float64 `json:"speed"`
	Reason string  `json:"reason"`
}

// propose asks the LLM for recipes. A request no provider accepts, or
// whose answer is unusable, yields no proposals: code then casts alone.
func (c *Caster) propose(ctx context.Context, group []*store.Character, voices []BaseVoice,
	narrator Recipe, narratorSpeed float64, used []taken,
) (map[int64]*proposal, error) {
	var voiceLines, takenLines, charLines []string
	for _, v := range voices {
		voiceLines = append(voiceLines, fmt.Sprintf("%s | %s | %s | %s | %s", v.ID, v.Gender, v.Accent,
			cmpOr(v.Grade, "ungraded"), cmpOr(v.Traits, "-")))
	}
	for _, t := range used {
		takenLines = append(takenLines, fmt.Sprintf("%s: %s, speed %.2f", t.name, t.recipe, t.speed))
	}
	for _, ch := range group {
		line := []string{"c" + strconv.FormatInt(ch.ID, 10), ch.Name, string(ch.Gender) + ", " + string(ch.AgeGroup),
			"importance " + strconv.Itoa(ch.Importance)}
		for _, s := range []string{ch.Role, ch.Description, ch.SpeechStyle} {
			if s != "" {
				line = append(line, s)
			}
		}
		charLines = append(charLines, strings.Join(line, " | "))
	}
	req, err := castPrompt.Render(map[string]any{
		"Voices": voiceLines, "Narrator": fmt.Sprintf("%s, speed %.2f", narrator, narratorSpeed),
		"Taken": takenLines, "Characters": charLines,
	})
	if err != nil {
		return nil, jobs.Permanent(err)
	}
	req.MaxTokens = 4096
	var out struct {
		Voices []proposal `json:"voices"`
	}
	_, err = c.LLM.CompleteJSON(ctx, req, &out)
	var (
		rejected *llm.RejectedError
		tooLarge *llm.TooLargeError
		invalid  *llm.InvalidOutputError
	)
	if errors.As(err, &rejected) || errors.As(err, &tooLarge) || errors.As(err, &invalid) {
		c.Log.Warn("voice casting request failed, casting without proposals", "err", err)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	inGroup := map[int64]bool{}
	for _, ch := range group {
		inGroup[ch.ID] = true
	}
	res := map[int64]*proposal{}
	for i := range out.Voices {
		p := &out.Voices[i]
		id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(p.Ref), "c"), 10, 64)
		if err == nil && inGroup[id] {
			res[id] = p
		}
	}
	return res, nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// choose returns the recipe for a character: the proposal if it fits the
// rules, else the fitting blend closest to it (without a proposal, the
// best-graded fitting one). Only when nothing fits, the blend farthest
// from all others.
func choose(voices []BaseVoice, ch *store.Character, p *proposal, narrator Recipe, narratorSpeed float64,
	used []taken,
) (Recipe, float64, string) {
	var fit []BaseVoice
	for _, v := range voices {
		if genderFits(v, ch.Gender) {
			fit = append(fit, v)
		}
	}
	if len(fit) == 0 {
		return nil, 0, ""
	}
	grades := map[string]float64{}
	for _, v := range fit {
		grades[v.ID] = gradeScore(v.Grade)
	}
	speed := AgeSpeed(ch.AgeGroup)
	var want Recipe
	note := ""
	if p != nil {
		total := 0.0
		for _, part := range p.Parts {
			if _, ok := grades[part.Voice]; ok && part.Weight > 0 && !math.IsInf(part.Weight, 0) &&
				len(want) < 3 && want.weight(part.Voice) == 0 {
				want = append(want, Part{Voice: part.Voice, Weight: part.Weight})
				total += part.Weight
			}
		}
		for i := range want {
			want[i].Weight /= total
		}
		if want = want.normalized(); len(want) == 0 {
			want = nil
		}
		if p.Speed > 0 {
			speed = p.Speed
		}
		note = strings.TrimSpace(p.Reason)
		if utf8.RuneCountInString(note) > 200 {
			note = string([]rune(note)[:200])
		}
	}
	speed = math.Round(min(max(speed, MinSpeed), MaxSpeed)*100) / 100
	mix := mixable(fit)
	if !mix {
		// Voices of other engines stand alone and keep their pace: only a
		// voice of its own tells a character apart.
		speed = 1
		if len(want) > 1 {
			want = Recipe{{Voice: want[0].Voice, Weight: 1}}
		}
	}

	// gap is the smallest distance to the narrator and the other voices,
	// relative to the required one (>= 1 fits).
	gap := func(r Recipe) float64 {
		g := Distance(r, speed, narrator, narratorSpeed) / NarratorDistance
		for _, t := range used {
			g = min(g, Distance(r, speed, t.recipe, t.speed)/MinDistance)
		}
		return g
	}
	if want != nil && gap(want) >= 1 {
		return want, speed, note
	}
	grade := func(r Recipe) float64 {
		s := 0.0
		for _, part := range r {
			s += part.Weight * grades[part.Voice]
		}
		return s
	}
	// uses counts how often a voice is spoken already (nothing distinct
	// left: the least used one; the narrator's last).
	uses := func(r Recipe) int {
		n := 0
		if r.String() == narrator.String() {
			n = len(used) + 1
		}
		for _, t := range used {
			if t.recipe.String() == r.String() {
				n++
			}
		}
		return n
	}
	var best Recipe
	bestScore := math.Inf(-1)
	var fallback Recipe
	fallbackGap := math.Inf(-1)
	for _, r := range candidates(fit, mix) {
		g := gap(r)
		if g >= 1 {
			score := grade(r)
			if want != nil {
				score = -Distance(r, speed, want, speed)*10 + grade(r)/10
			}
			if score > bestScore {
				best, bestScore = r, score
			}
		} else if best == nil && (g > fallbackGap || g == fallbackGap &&
			(uses(r) < uses(fallback) || uses(r) == uses(fallback) && grade(r) > grade(fallback))) {
			fallback, fallbackGap = r, g
		}
	}
	adjusted := "chosen by rule"
	if want != nil {
		adjusted = "adjusted to keep voices distinct"
	}
	if best == nil {
		best = fallback
		adjusted = "no clearly distinct voice left"
	}
	if note != "" {
		note += " (" + adjusted + ")"
	} else {
		note = adjusted
	}
	return best, speed, note
}

// candidates lists the blends code chooses from: every base voice alone,
// and (mix: Kokoro's voices) every pair evenly and 70/30.
func candidates(vs []BaseVoice, mix bool) []Recipe {
	var out []Recipe
	for _, v := range vs {
		out = append(out, Recipe{{Voice: v.ID, Weight: 1}})
	}
	if !mix {
		return out
	}
	for i, a := range vs {
		for j, b := range vs {
			if i == j {
				continue
			}
			if i < j {
				out = append(out, Recipe{{Voice: a.ID, Weight: 0.5}, {Voice: b.ID, Weight: 0.5}})
			}
			out = append(out, Recipe{{Voice: a.ID, Weight: 0.7}, {Voice: b.ID, Weight: 0.3}})
		}
	}
	return out
}

// Reconcile starts the casting of books whose cast voices belong to
// another engine than their narrator's (the deployment changed its
// engines or default voice); it returns how many it started.
func (c *Caster) Reconcile(ctx context.Context) (int, error) {
	started := 0
	for offset := 0; ; offset += 100 {
		books, _, err := c.Store.ListBooks(ctx, offset, 100)
		if err != nil {
			return started, err
		}
		for i := range books {
			b := &books[i]
			if b.AnalysisStatus != store.AnalysisDone || Active(b) {
				continue
			}
			voice, _ := Narrator(b, c.DefaultVoice)
			chars, err := c.Store.BookCharacters(ctx, b.ID)
			if err != nil {
				return started, err
			}
			if !slices.ContainsFunc(chars, func(ch store.Character) bool {
				return ch.VoiceRecipe != "" && !ch.VoiceManual && tts.EngineOf(ch.VoiceRecipe) != tts.EngineOf(voice)
			}) {
				continue
			}
			// Only when the narrator's engine can be cast from (not one
			// this deployment lacks): that casting would fail every start.
			if vs, err := c.Catalog.VoicesFor(ctx, voice); err != nil || len(Castable(vs)) == 0 {
				continue
			}
			if err := c.Start(ctx, b, false); err != nil {
				return started, err
			}
			started++
		}
		if len(books) < 100 {
			return started, nil
		}
	}
}
