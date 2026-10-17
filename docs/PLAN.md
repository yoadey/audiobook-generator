# Project plan: audiobook-generator

Last updated: 2026-10-16 · Status: **M0–M7 implemented** (feature-complete v1.0: multi-voice audiobook, lexicon, podcast RSS, M4B, OIDC, metrics, Litestream, export/import); from M8: reading along (also as podcast transcripts) and custom preview texts

Goal: a self-hosted service that turns very large English EPUBs into
multi-voice audiobooks. All characters of the book are analysed up front,
every (important) character gets its own voice, and a web UI lets you browse
and adjust chapters, characters and voices. Audio is generated chapter by
chapter. Everything ships as **one Helm chart that brings everything it
needs**.

- Backend: **Go**
- Frontend: **React** (TypeScript) with **Mantine**
- Persistence: **GORM**, with **SQLite** (default) or **PostgreSQL**
- Delivery: **Helm chart** + container image on ghcr.io

---

## 1. Constraints

| Topic | Requirement / assumption |
|---|---|
| Books | English, DRM-free, very large: the reference EPUB is 12 MB without images ≈ **4–6 M words**, probably > 1000 chapters, hundreds to thousands of characters |
| Audio length | at ~155 words/min ≈ **450–650 h of audio** per book |
| Hardware | Kubernetes (Talos) on Proxmox, **no GPU** (the UHD 770 iGPU stays unused for now), nodes ~16 GB RAM |
| LLM | Cloud is fine but must be **free**: free tiers of OpenAI-compatible providers (Gemini Flash first), optional local model as fallback |
| TTS | **Kokoro-82M** via Kokoro-FastAPI (CPU image, OpenAI-compatible, voice mixing) |
| Usage | One household, LAN only, a handful of users, not multi-tenant |

Principles derived from these:

1. **Just in time, not everything up front.** Character analysis runs once
   over the whole book; speaker attribution and rendering happen per chapter
   shortly before they are needed ("always keep the next N chapters after the
   listening position ready"). Rendering (~3–5× real time) is faster than
   listening.
2. **The LLM never rewrites book text.** Text is segmented deterministically
   in code; the LLM only returns compact labels (`q17 → char_42`). Saves
   free-tier quota and stops the model from altering the text.
3. **Everything is resumable.** Every step is a persisted job; restarts,
   429 rate limits and daily quotas only pause work, they never lose it.
4. **Voices are deterministic.** A character voice is a "recipe" (a weighted
   mix of Kokoro base voices + speed). Same recipe ⇒ same voice in chapter 3
   and chapter 1300.
5. **Self-contained.** One `helm install` is enough: app, TTS server,
   storage. External dependencies (PostgreSQL, OIDC, S3 backup, external TTS)
   are optional.

---

## 2. Architecture

```
                       ┌──────────────────── Helm release ─────────────────────┐
 Browser (React SPA) ──┼─▶ Ingress ─▶ app (Go, 1 pod)                          │
 Podcast app (RSS) ────┤               ├─ HTTP API (OpenAPI) + SPA (embed.FS)  │
                       │               ├─ Job runner                           │
                       │               │    ├─ LLM worker (rate-limited) ──────┼──▶ Gemini / Groq / OpenRouter / local
                       │               │    ├─ TTS workers (N parallel) ──┐    │      (OpenAI-compatible)
                       │               │    └─ Audio assembler (ffmpeg)   │    │
                       │               ├─ GORM ─┬─ SQLite (WAL, on PVC)   │    │
                       │               │        └─ or PostgreSQL ─────────┼────┼──▶ external / CNPG cluster
                       │               └─ /data (PVC): EPUBs, audio       │    │
                       │                    └─ optional Litestream ───────┼────┼──▶ S3 backup (SQLite only)
                       │                                                  ▼    │
                       │              kokoro (Kokoro-FastAPI CPU, 1..n pods)   │
                       └───────────────────────────────────────────────────────┘
```

### 2.1 Components

| Component | Responsibility |
|---|---|
| **app** (Go binary) | API, serves the SPA, job runner, EPUB import, segmentation, LLM orchestration, audio assembly, player backend, RSS feed |
| **kokoro** | Kokoro-FastAPI (CPU), cluster-internal only; scales via replicas |
| **Database** (GORM) | Books, chapters, segments, characters, voices, jobs, quotas, listening progress |
| **/data** on a PVC | Original EPUBs, rendered chapter audio, temporary render files (and the SQLite file in SQLite mode) |
| **ffmpeg** (in the app image) | Concatenation, loudness normalisation, AAC/M4B with chapter markers |

### 2.2 Decision: GORM with SQLite or PostgreSQL

Both databases are supported through **GORM**; the dialect is chosen by
configuration.

| | SQLite (default) | PostgreSQL |
|---|---|---|
| Driver | `github.com/ncruces/go-sqlite3/gormlite` (SQLite compiled to Go via wasm2go, pure Go, **no CGO**, actively maintained; `glebarez/sqlite` has had no release since 2024) | `gorm.io/driver/postgres` (pgx) |
| Where | file on the data PVC, WAL mode | external server or a CNPG `Cluster` rendered by the chart |
| Backup | optional **Litestream** sidecar → S3 (e.g. Garage) | the database's own backup (e.g. CNPG barman) |
| Use case | self-contained, zero dependencies | clusters that already run Postgres operators and backups |

Rules that keep both dialects working:

- **Migrations** with **gormigrate** (`github.com/go-gormigrate/gormigrate/v2`):
  versioned, ordered steps written against the GORM migrator API, so they
  run on both dialects. No bare `AutoMigrate` at startup in production.
- **No dialect-specific SQL** in the store layer. Where it is unavoidable
  (e.g. job claiming), it lives behind a small interface with one
  implementation per dialect and is covered by tests on both.
- **No JSON columns for things we query.** Aliases, chapter appearances etc.
  are proper tables; only opaque payloads (job parameters, raw LLM output)
  are stored as JSON text.
- **Job claiming**: a single `UPDATE … SET status='running' … WHERE id =
  (SELECT … LIMIT 1) RETURNING …` works on SQLite (≥ 3.35) and PostgreSQL;
  PostgreSQL additionally uses `FOR UPDATE SKIP LOCKED`.
- **Single replica in both modes**: the audio PVC is `ReadWriteOnce`, and
  the job runner lives in the same process. Strategy `Recreate`.
- Also a **JSON export/import** of the analysis results (characters, voices,
  attributions), because they cost days of LLM quota, independent of which
  database is used.

Volume: ~1 M segments per book, ~30 MB of text — trivial for either.

### 2.3 Decision: built-in player, Audiobookshelf later

The service ships its **own web player** (per-user progress, chapter
navigation, playback speed, lock-screen controls through the Media Session
API) and a **podcast RSS feed** per book/volume so any podcast app on a
phone works. The built-in player also provides the listening position that
drives just-in-time rendering.

An **Audiobookshelf export** (upload through its API or a shared volume) is
an optional later feature (M8), not a requirement.

---

## 3. Processing pipeline

```
 EPUB ─▶ 1 Import ─▶ 2 Segmentation ─▶ 3 Character analysis ─▶ 4 Merge ─▶ 5 Casting
         (once)       (once, code)       (once, LLM,             (code +    (code +
                                          ~150–250 requests)      LLM)       LLM)
                                                                     │
          ┌──────────────────────── per chapter, just in time ───────┘
          ▼
   6 Speaker attribution (LLM) ─▶ 7 Rendering (Kokoro) ─▶ 8 Assembly (ffmpeg) ─▶ player / RSS / download
```

### 3.1 Import (EPUB → volumes/chapters/paragraphs)

- Parse the EPUB ourselves: `archive/zip` + `encoding/xml` (OPF, NCX) +
  `golang.org/x/net/html` (XHTML, nav). No heavy dependency.
- Structure from the table of contents (nav.xhtml or NCX) **including
  fragment anchors** — large web serials often put many chapters into one
  XHTML file. Fallbacks: headings (`h1`–`h3`), then spine documents.
- Hierarchy **volume → chapter**, derived from TOC nesting.
- Normalisation: typography, strip footnotes/endnotes, scene breaks (`***`,
  `<hr>`) become pause markers, italics are kept as markup (later feature:
  inner thoughts).
- Streaming-capable: a single 30 MB XHTML file must not blow up memory.
- Reviewable in the UI: merge/split/rename chapters, exclude front/back
  matter.

### 3.2 Segmentation (deterministic, in code)

- Split paragraphs into **narration** and **quoted speech** with a state
  machine over quote characters; the quote style is detected per book from
  statistics (`“…”`, `"…"`, British `‘…’`, `'…'`, guillemets `«…»`/`»…«`,
  `„…“`, or dash dialogue). Multi-paragraph quotes (a paragraph without a
  closing mark) continue correctly.
- Italics from the EPUB are kept per segment. Italic runs of a few words,
  and runs in single marks in books whose speech uses double ones, are
  **thoughts**: segments of their own with a quote number, attributed by
  the LLM to the thinking character (marked `*…*` in the prompt).
- Every quote gets a stable ID (`q<n>` per chapter).
- Long narration is split at sentence boundaries into TTS-sized pieces
  (Kokoro works best with ≤ ~400 characters per call).
- Dialogue tags ("…, she said.") stay narration.

### 3.3 Character analysis (once, whole book)

- Chapters are bundled into batches of ~30–50k tokens
  (≈ 150–250 requests for a 5 M-word book).
- Batches run **sequentially**; every request includes the compact index of
  already known characters (canonical name + aliases only) so the model
  reuses existing names instead of producing duplicates.
- Response as a JSON schema (structured output); per character: canonical
  name, aliases/titles, gender, age group, role, short description, speech
  style, estimated importance, chapters it appears in.
- Per chapter additionally: **point of view** (first-person narrator? who?)
  — matters for web serials with changing POV chapters.
- Result: the character bible + an index "character → chapters" (+ mention
  counts).
- Duration on a free tier: roughly **1–3 days** including automatic pauses.
  The UI shows progress and the day's quota usage.

### 3.4 Merge (entity resolution)

1. Deterministic: normalisation (case, titles such as *Mr./Lady/Captain*),
   alias overlap ⇒ union-find clusters.
2. Only ambiguous candidates ("Erin" vs. "Erin Solstice" vs. "the
   [Innkeeper]") go to an LLM merge request as compact profiles.
3. Manual correction in the UI: merge, split, move an alias.

As implemented in M4 (`internal/analysis`):

- One `analyze_book` job per book on the `llm` queue (one at a time, the
  quotas are shared). Batches of about `llm.analysisBatchTokens` (30k)
  tokens of whole chapters; oversized chapters are split at paragraphs.
  The plan and every validated answer are stored (`analysis_batches`, with
  prompt version, provider, model and tokens); each answer is merged into
  the bible in the same transaction that completes its batch, so pausing,
  quota deferrals and restarts lose nothing.
- Per batch, only the known characters the batch text mentions (all words
  of a name or alias occur in it) are sent, with ids (`c12`), at most 250.
  A reported character is matched by `ref`, else by its full name, else by
  aliases that all point to one character; otherwise it is new.
  Profiles are filled in, never overwritten (and not at all once edited by
  hand); importance and "speaks" keep the strongest signal.
- An answer cut off at the output limit, or a batch no provider accepts
  (tokens per minute), is split in two and retried.
- Duplicate check after the last batch: single-word proper names that are
  the first or last word of another's full name, and names equal apart
  from titles, go to the `merge_characters` prompt in groups of 20; "same"
  merges (the character with more chapters survives), "different" is
  stored (`character_distincts`) and never asked again. Pairs with
  different known genders are distinct without asking ("Mr." vs. "Mrs.
  Bennet"). Generic words ("Mom", "the guard") never match.
- Per chapter: narration (first/third person, mixed) and point-of-view
  character.
- While an analysis is queued or running (including a paused one whose
  job still finishes its current request), characters cannot be edited,
  merged, split or deleted and the book cannot be deleted (409). Chapters
  cannot be merged or split while planned batches are pending (they refer
  to the chapters); before the start and once done, they can. An analysis
  can be paused, resumed or restarted (restart discards all results).
- Batches that every provider rejects as too long (400/413 with a context
  hint) are split like cut-off answers.

### 3.5 Voice casting

- **Voice catalogue**: Kokoro voices with metadata (gender, US/UK accent,
  quality grade, timbre) as an embedded catalogue file, reconciled against
  the TTS server's `/v1/audio/voices`.
- **Narrator**: one fixed voice per book (selectable).
- **Main characters** (top ~40–60 by importance/lines): the LLM gets the
  character profile + catalogue and proposes a recipe
  (e.g. `bm_george(0.7)+am_michael(0.3)`, speed 0.95). Code enforces:
  gender matches, recipes differ enough (minimum distance in weight space),
  no collision with the narrator.
- **Minor characters**: pool voices by (gender, age group), assigned
  deterministically by hash — the same minor character always sounds the
  same.
- Every character can be promoted/demoted in the UI and its recipe edited
  freely; previews use a **real quote of that character**.
- Unknown speakers ("a guard") → generic pool voice by gender.

As implemented in M5 (`internal/casting`):

- The catalogue (`internal/casting/voices.json`: the 28 English Kokoro
  voices with gender, accent, model-card grade and a timbre hint) is merged
  with the server's `/v1/audio/voices` (cached 5 min): catalogue voices the
  server lacks are unavailable, server-only voices are described from their
  IDs. Casting only uses available English voices.
- One `cast_book` job per book on the `llm` queue, started automatically
  when the analysis finishes (and by hand). The `casting.mainCharacters`
  (50) most important speaking characters become main characters unless
  their tier was chosen by hand. Main characters without a voice are sent
  to the `cast_voices` prompt in groups of 8, most important first, with
  the catalogue, the narrator and the voices already taken.
- Code checks every proposal: unknown or wrong-gender base voices are
  dropped, weights normalized, speed clamped to 0.85–1.15. The distance
  between two voices is the share of blend weight they do not have in
  common plus their speed difference; a recipe must be ≥ 0.6 from the
  narrator and ≥ 0.3 from every other recipe. A proposal that fails is
  replaced by the fitting blend closest to it (singles, 50/50 and 70/30
  pairs of fitting voices); without a usable answer (refused, invalid) the
  best-graded fitting blend is taken. The reason is stored with the voice.
- Minor characters have no stored voice: a pool voice (a single base voice
  of their gender, suited to their age where the catalogue says so, not a
  main part of the narrator's voice, at an age-dependent speed) is picked
  by hashing the character ID, so it stays the same.
- Tiers and voices chosen in the UI are flags (`TierManual`,
  `VoiceManual`) the casting never overrides; "Recast all" resets only
  automatic ones. Promoting a character (or resetting its voice) queues a
  casting for it; a gender change drops an automatic recipe. The casting
  view warns about main characters that sound like the narrator (after a
  narrator change) or like each other.
- Previews use a real quote: until speaker attribution exists, a quote in
  a paragraph whose narration names the character next to a speech verb
  ("said Erin"). Previews are cached in memory (LRU, 32 MB).

### 3.6 Speaker attribution (per chapter, just in time)

- Input: chapter text with quote markers (`[q12]“…”`), the characters
  appearing in the chapter (from the index) + the speakers of the last quotes
  of the previous chapter as context.
- Output: `{ "q12": { "speaker": "char_42" | "unknown" | "new:Name",
  "emotion": "angry", "confidence": 0.8 } }`.
- Validation: every quote ID covered, only known IDs; otherwise a repair
  request. `new:` characters are added to the bible as minor characters.
- Overlong chapters are split at scene breaks, with overlap.
- Low confidence is flagged in the UI.
- Optional later: narrate first-person chapters with the POV character's
  voice.

As implemented in M6 (`internal/attribution`):

- One `attribute_chapter` job per chapter on the `llm` queue. A render of
  a chapter that needs speakers (finished analysis, quotes, not yet
  attributed) queues it and waits (the render job is deferred and looks
  again every 30 s); a failed attribution leaves the quotes with the
  narrator instead of blocking the render.
- The request (`attribute_chapter@v1`) lists the chapter's characters
  (appearances, point-of-view character, the last speakers of the previous
  chapter, at most 120) and the text with every quotation marked once, at
  the paragraph it starts in. The answer per quotation: speaker (a listed
  id, `new:Name` or `unknown`), emotion and confidence.
- Validation in code: quotations not answered, or answered with an id that
  was not listed, stay with the narrator at confidence 0; `new:` names are
  matched against names and aliases or added as minor characters.
- Chapters longer than `llm.attributionTokens` (12k) go in parts, cut at
  scene breaks where possible, later parts with three paragraphs of
  context. Each part is stored when answered, together with the speakers it
  names for the first time and the number of paragraphs done, only while
  the attribution still runs from that point (a reset, chapter merge or
  split drops a late answer); a resumed job continues after those
  paragraphs.
- Waiting: a render waits for the attribution it needs and, if the chapter
  has speakers, for a pending casting (queued above attributions) — but
  not for jobs deferred more than two minutes (LLM quota, no provider):
  then it renders with the voices there are and is rendered again when
  they change. A finished analysis marks the audio of chapters with
  quotations outdated (it was narrated only). Outdated audio stays playable
  while it is re-rendered, and a failed re-render keeps it (the lookahead
  then backs off for 30 minutes). Every render is a new file; the one it
  replaces is kept until the next render, and the player asks for the
  version it started with (`?v=`), so a seek after a re-render still
  reads the same file. Cut-off answers, requests too large for every provider and
  content-filter refusals split the part; a single paragraph still refused
  stays with the narrator.
- Speakers chosen in the chapter editor are flags (`SpeakerManual`) an
  attribution never overrides. Confidence below 0.6 is flagged for review.
- Rendering speaks quotes with the speaker's own recipe or pool voice,
  narration and unknown speakers with the narrator. A per-chapter voice
  revision is bumped by attribution, speaker edits, character voice,
  tier, gender or age changes, merges and the casting; audio rendered at an
  older revision is stale and re-rendered by the lookahead. Kokoro has no
  emotion control, so a quote's emotion is spoken through pace and
  loudness (subtle: e.g. sad 0.92× speed, softer; shouting louder but never
  clipping; whispering much softer); thoughts are a little softer than
  speech. Changing an emotion by hand bumps the voice revision.

### 3.6a Chapter preparation (per chapter, just in time, optional)

A book can ask for its chapters to be prepared for reading aloud
(`prepareChapters`, off by default because it spends LLM quota). Like the
attribution, a render waits for it (queued if missing) unless the job
cannot run soon; it needs no character analysis. The LLM gets the chapter
with numbered paragraphs (`[pN]`, long chapters in parts, each stored with
its answer so a stopped job resumes) and returns labels only
(`prepare_chapter` prompt):

- **moods** of narration per paragraph (calm, tense, action, somber, warm,
  light, mysterious) → the narrator's pace and loudness in that paragraph
  (subtle, e.g. tense 1.04×, somber 0.94× and softer);
- **dramatic pauses**, anchored by the 3–8 words before them, which must be
  found in the paragraph and end a sentence or clause (short 0.5 s, long
  1 s; at most one per paragraph and a handful per chapter); a pause
  inside a segment splits it into phrases;
- **spoken forms** for notation a TTS misreads ("Lv. 5" → "level five",
  status screens, units, symbols): the spot must occur verbatim within one
  segment, be short and hold notation (digits, symbols, known
  abbreviations, acronyms said by their initials, Roman numerals); the
  spoken form is plain words (Kokoro syntax is removed), keeps every
  ordinary word of the spot in order and may add only a few words per
  piece of notation. The LLM thus never changes, adds or drops a word of
  the story, it only says how notation is read;
- **pronunciations** of invented names as respellings, stored in the book's
  lexicon as *suggestions* (applied only once accepted; dismissed ones are
  not suggested again); the terms must occur in the text.

All of it applies to the TTS input only: the book text, the reader and
the transcripts stay as written, and word timings skip the replaced spots.
Applying a part bumps the voice revision; turning the option on or off
outdates the book's audio, and the render ignores preparations while it
is off. "Prepare again" discards a chapter's moods, pauses and spoken
forms (the lexicon keeps its entries).

### 3.7 Rendering and assembly

- Every segment → `POST /dev/captioned_speech` (recipe, speed, PCM, word
  timestamps; Kokoro-FastAPI ≥ v0.9), falling back to `/v1/audio/speech`
  for servers without it. A segment with an ellipsis inside ("I just… I
  don't know") is split there into phrases with a pause between them.
  Concurrency is configurable (≈ Kokoro replicas × 1–2).
- A per-book pronunciation lexicon (fantasy names) is applied before the TTS
  call (text replacement or Kokoro phoneme syntax).
- Assembly with ffmpeg: pauses by context (narration↔dialogue ~150 ms,
  around thoughts 250 ms, paragraph ~450 ms but 300 ms between lines of an
  exchange of speech, after trailing speech "…" 450 ms, almost none after
  cut-off speech "—" so the next speaker cuts in, sad lines a little more,
  scene break ~1.2 s, chapter title + 1 s), `loudnorm`,
  **AAC 64 kbit/s mono** per chapter (`.m4a`) with metadata; optionally one
  M4B per volume with chapter markers.
- Temporary files exist only during the job. In v1 a correction re-renders
  the whole chapter (a few CPU minutes); a per-segment cache (Opus) is a
  later optimisation.
- Storage for finished audio: ~22–29 MB/h ⇒ **~10–19 GB** per huge book.
- While joining the pieces the render records when each segment is spoken
  (exact, from the PCM lengths and the known pauses) in a small JSON file
  next to the audio version, with the start of every word as reported by
  Kokoro (matched to the segment's text; words read differently, such as
  numbers, are skipped). The reader follows the voice with it, marking the
  sentence and word being spoken, and the podcast feed serves it as a
  WebVTT transcript per episode (`<podcast:transcript>`), its lines
  starting with their first word. Renders from before get an estimate from
  the text length; a re-render makes them exact.

### 3.8 Scheduler and jobs

- Job table in the database; types: `import`, `analyze_batch`, `merge`,
  `cast`, `attribute_chapter`, `render_chapter`, `assemble_volume`.
- Separate worker pools: **LLM** (concurrency 1, rate limiter) and **TTS**
  (concurrency N), so one resource never blocks the other.
- **Lookahead policy**: keeps the next *N* chapters after the listening
  position ready (attributed + rendered). Manual control too: enqueue single
  chapters or ranges, prioritise, pause.
- Dependencies: `render_chapter` needs `attribute_chapter`, which needs a
  finished analysis + casting.
- Live progress to the UI via **Server-Sent Events**.

### 3.9 LLM layer

- A small OpenAI-compatible client (chat completions + JSON schema),
  multiple **providers** configurable with base URL, model, API key and
  limits (RPM, TPM, RPD).
- Token bucket per provider, persistent daily counters, `429`/`Retry-After`
  honoured, then fallback to the next provider in the list.
- Quota display in the UI ("today 312/1000 requests on Gemini").
- Prompts are versioned, embedded templates; the prompt version is stored
  with every result (traceability, targeted recomputation).
- Documented caveat: free tiers often may use inputs for training; limits
  change frequently ⇒ everything is configuration, nothing hard-coded.

As implemented in M3 (`internal/llm`):

- Providers live in the database. Those from the Helm values
  (`llm.providers`, passed as `ABG_LLM_PROVIDERS` JSON, keys from Secrets
  via `apiKeyEnv`) are synced on start-up and read-only in the UI; more can
  be added in the UI (key stored in the database, never returned by the
  API). Order: `position`, then creation.
- Limits: RPM/TPM as a sliding 60 s window in memory, RPD/TPD as persistent
  daily counters (`llm_usages`) that reset at midnight in the provider's
  `quotaTimezone` (Gemini: Pacific time). A provider whose minute window
  frees up within `llm.maxWait` (70 s) is waited for, otherwise the next
  one is used.
- Errors: `429` → cooldown from `Retry-After`/Google's `retryDelay`, else
  exponential (15 s … 15 min); a 429 naming a per-day quota → cooldown
  until the reset; `401/403/404` → 5 min cooldown (misconfiguration);
  `400/413/422` → only this request skips the provider; network/`5xx` →
  30 s cooldown.
- When no provider can take a request, jobs are **deferred** (back to the
  queue until the earliest provider is available, without using up an
  attempt); likewise while no provider is configured.
- Structured output per provider: `json_schema` (response_format),
  `json_object` or `prompt` (schema in the system prompt). Answers are
  always validated against the schema (santhosh-tekuri/jsonschema), with
  one repair request quoting the validation errors; Markdown fences and
  `<think>` blocks are stripped.
- The provider test sends a small prompt (add two random numbers, echo a
  word) and checks connectivity, key, structured output and content.

---

## 4. Data model (sketch, GORM models)

```
Book             ID, Title, Author, Language, CoverPath, EpubPath, QuoteStyle,
                 NarratorVoiceID, Status, CreatedAt, UpdatedAt
Volume           ID, BookID, Idx, Title
Chapter          ID, BookID, VolumeID, Idx, Title, WordCount, POVCharacterID,
                 Excluded, Status (imported|attributed|rendered|failed),
                 AudioPath, AudioDurationMs, PromptVersion
Paragraph        ID, ChapterID, Idx, Kind (text|scene_break|heading), Text
Segment          ID, ChapterID, ParagraphID, Idx, Kind (narration|quote), QuoteRef,
                 Text, SpeakerCharacterID, Emotion, Confidence, ManualOverride
Character        ID, BookID, Name, Gender, AgeGroup, Role, Description, SpeechStyle,
                 Importance, MentionCount, LineCount, Tier (main|minor|pool),
                 MergedIntoID, VoiceID
CharacterAlias   ID, CharacterID, Alias                      (unique per book)
CharacterChapter CharacterID, ChapterID, Mentions
Voice            ID, BookID, Recipe ("bm_george(0.7)+am_michael(0.3)"), Speed, Label
LexiconEntry     ID, BookID, Term, Replacement, IsPhonemes
Job              ID, Type, BookID, ChapterID, Status, Priority, Attempts,
                 NotBefore, Error, Payload (JSON text), CreatedAt, UpdatedAt
LLMUsage         Provider, Day, Requests, InputTokens, OutputTokens
ListenProgress   UserID, BookID, ChapterID, PositionMs, UpdatedAt
User             ID, OIDCSubject, Name                   (only with OIDC enabled)
```

Large tables (`Segment`, `Paragraph`) are written in batches
(`CreateInBatches`) and read paginated per chapter; indexes on every foreign
key plus `(chapter_id, idx)`.

---

## 5. Backend (Go)

| Area | Choice |
|---|---|
| Language | Go (current stable, ≥ 1.25) |
| HTTP | `net/http` (ServeMux patterns) + **oapi-codegen** (strict server) |
| API contract | **OpenAPI 3.0.3** in `api/openapi.yaml` as the single source of truth (3.0 because oapi-codegen's 3.1 support is incomplete) |
| Persistence | **GORM**; `github.com/ncruces/go-sqlite3/gormlite` + `gorm.io/driver/postgres`; migrations with **gormigrate** |
| OIDC | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`, session cookie; can be disabled |
| Logging / metrics | `log/slog` (JSON), Prometheus `/metrics` (jobs, quotas, render time, real-time factor) |
| Configuration | environment variables (`ABG_*`); the provider list as JSON in `ABG_LLM_PROVIDERS` |
| Audio | `ffmpeg`/`ffprobe` as subprocesses (shipped in the image) |
| Frontend delivery | `embed.FS`, SPA fallback to `index.html` |
| Tests | `testing` + testify, golden files, fake LLM and fake TTS servers (httptest); store tests against **both** SQLite and PostgreSQL (testcontainers / CI service container) |
| Lint | golangci-lint |

### Package layout

```
cmd/audiobook-generator/     main: config, wiring, server + runner
internal/config/
internal/store/              GORM models, repositories, gormigrate migrations,
                             dialect-specific bits (job claiming) behind interfaces
internal/epub/               OPF/NCX/nav parser, XHTML → paragraphs, chapter split
internal/segment/            quote state machine, sentence chunking
internal/llm/                OpenAI-compatible client, providers, rate limiter, usage,
                             embedded versioned prompts (internal/llm/prompts)
internal/analysis/           character analysis batches, merge, POV
internal/casting/            voice catalogue, recipe assignment, pools
internal/attribution/        per-chapter speaker attribution + validation
internal/tts/                Kokoro client, pronunciation lexicon
internal/audio/              ffmpeg pipeline, pauses, loudnorm, M4A/M4B
internal/jobs/               queue, worker pools, lookahead policy, SSE events
internal/api/                oapi-codegen handlers
internal/auth/               OIDC, sessions
internal/feed/               podcast RSS
web/                         React app (own package.json)
api/openapi.yaml
charts/audiobook-generator/
```

### API (excerpt)

```
POST   /api/books                         upload EPUB (multipart, configurable limit)
GET    /api/books | /api/books/{id}       list / details incl. pipeline status
POST   /api/books/{id}/analysis           start/resume character analysis
GET    /api/books/{id}/chapters           paginated, filters (status, volume, character)
PATCH  /api/chapters/{id}                 title, exclude
POST   /api/chapters/{id}/merge|split
GET    /api/chapters/{id}/segments
PATCH  /api/segments/{id}                 fix speaker/emotion/text
POST   /api/chapters/render               enqueue a range/selection
GET    /api/books/{id}/characters         paginated, sortable, search
PATCH  /api/characters/{id}               profile, tier, voice
POST   /api/characters/merge | /{id}/split
GET    /api/voices/catalog                Kokoro base voices
POST   /api/voices/preview                recipe + text → audio (cached)
GET    /api/books/{id}/lexicon  (+ CRUD)
GET    /api/jobs  · POST /api/jobs/{id}/retry|cancel · POST /api/queue/pause
GET    /api/llm/providers (+ CRUD, /{id}/test) · POST /api/llm/models
GET    /api/books/{id}/analysis · POST …/analysis (start/resume/restart) · POST …/analysis/pause
POST   /api/books/{id}/characters/merge · GET|PATCH|DELETE /api/characters/{id} · POST …/split
GET    /api/events                        SSE: job and progress events
GET    /api/chapters/{id}/audio           range requests for the player
PUT    /api/books/{id}/progress           listening position
GET    /feed/{token}/{bookId}.xml         podcast RSS (token instead of session)
GET    /api/books/{id}/downloads · POST …/downloads/{volumeId} · GET …/downloads/{volumeId}/file   M4B
GET    /api/books/{id}/export · POST /api/import
GET    /auth/login|callback|logout · GET /api/me
GET    /healthz · /readyz · /metrics
```

---

## 6. Frontend (React)

| Area | Choice |
|---|---|
| Build | **Vite** + React + TypeScript 5 (strict; openapi-typescript does not support TS 6+ yet) |
| Routing / data | **TanStack Router** + **TanStack Query** |
| API client | **openapi-typescript** + **openapi-fetch** (types from `api/openapi.yaml`) |
| Tables | Mantine tables + **TanStack Virtual** (1000+ chapters, 1000+ characters) |
| UI library | **Mantine** (sliders, tables, notifications, modals; light/dark) |
| Tests | Vitest + Testing Library; Playwright for an E2E smoke test |
| Lint/format | ESLint + Prettier |

### Pages

```
┌ Library ──────────────────────────────────────────────────────────────────┐
│ [Upload EPUB]                                                             │
│ ▣ The Wandering Inn   Import ✔  Analysis 63 % (121/190)  Casting –        │
│                       LLM today: 312/1000 req.           ▶ continue        │
└───────────────────────────────────────────────────────────────────────────┘
┌ Book ▸ Chapters ──────────────────────────────────────────────────────────┐
│ Filter: [Status ▾] [Volume ▾] [Character: Erin ▾]   Lookahead: [10]       │
│ ▾ Volume 1                                                                │
│   1.00  Chapter 1.00          6,120 w  Script ✔  Audio ✔ 41:12  ▶         │
│   1.01  Chapter 1.01          5,870 w  Script ✔  Audio ⏳ 63 %   ✕         │
│   1.02  Interlude             7,010 w  Script –  Audio –        [▸ render] │
│ ☐ Selection: 20 chapters → [enqueue] [exclude]                            │
└───────────────────────────────────────────────────────────────────────────┘
┌ Book ▸ Characters ────────────────────────────────────────────────────────┐
│ Search [      ]  Tier [all ▾]                                             │
│ Name     Aliases               Lines   Ch.   Voice                   Test │
│ Erin     the Innkeeper          8,204   812  bf_emma(.8)+af_bella(.2)   ▶ │
│ Ryoka    the Runner             3,112   402  af_nicole · 1.05×          ▶ │
│ Guard #  —                         41    12  Pool (m, adult)            ▶ │
│ [merge] [split] [promote to main]                                         │
└───────────────────────────────────────────────────────────────────────────┘
┌ Voice editor (Erin) ──────────────────────────────────────────────────────┐
│ bf_emma     ███████████░░░  0.8      Speed  ◀━━━━●━━━▶ 1.00              │
│ af_bella    ███░░░░░░░░░░░  0.2      [+ base voice]                      │
│ Sample: (random real quote by Erin)  [↻]   ▶ preview                     │
└───────────────────────────────────────────────────────────────────────────┘
┌ Chapter editor (1.01) ────────────────────────────────────────────────────┐
│ ▶ chapter  [re-render]                                                    │
│ 🟦 Narrator  The door creaked open …                                      │
│ 🟥 Erin      “Welcome to the inn!”        [Erin ▾] [neutral ▾] ⚠ 0.55      │
│ 🟩 Pisces    “Hardly a welcome.”          [Pisces ▾] [dry ▾]               │
└───────────────────────────────────────────────────────────────────────────┘
```

Further pages: **Player** (chapter list, progress, speed, sleep timer,
Media Session), **Pronunciation** (lexicon with preview), **Voice catalogue**
(listen to all Kokoro voices), **Jobs** (queue, errors, retry),
**Settings** (providers, limits, quotas, narrator voice, lookahead).

---

## 7. Helm chart

`charts/audiobook-generator/` — the chart brings everything; only what you
want to replace externally can be switched off.

| Resource | Condition |
|---|---|
| Deployment `app` (1 replica, `Recreate`) | always |
| Service, ServiceAccount | always |
| PVC `data` (audio, EPUBs, SQLite file; default 50Gi) | `persistence.enabled` (otherwise `existingClaim`) |
| LLM providers (`llm.providers`, keys via `apiKeySecret` from existing Secrets) | as environment of the app |
| Secret (OIDC client secret, session key, feed token secret, DB password) | unless `existingSecret` is set |
| Deployment + Service `kokoro` | `tts.kokoro.enabled` (default `true`), otherwise `tts.externalUrl` |
| CNPG `Cluster` | `database.type=postgres` + `database.postgres.mode=cnpg` (needs the CNPG operator) |
| — (DSN/host from values or `existingSecret`) | `database.type=postgres` + `database.postgres.mode=external` |
| Litestream sidecar + config | `database.type=sqlite` + `backup.litestream.enabled` (S3 endpoint, bucket, `existingSecret`) |
| Ingress | `ingress.enabled` (className, hosts, tls, free annotations) |
| ServiceMonitor | `metrics.serviceMonitor.enabled` |
| NetworkPolicy | `networkPolicy.enabled` (kokoro reachable only from app) |

```yaml
# values.yaml excerpt
database:
  type: sqlite            # sqlite | postgres
  postgres:
    mode: external        # external | cnpg
    host: ""
    port: 5432
    database: audiobook
    user: audiobook
    existingSecret: ""    # key: password
    cnpg:
      instances: 1
      storage: { size: 5Gi, storageClass: "" }
```

Further values: `resources` separately for app and kokoro,
`priorityClassName`, `nodeSelector`/`tolerations`/`affinity`,
`podSecurityContext` (non-root, `readOnlyRootFilesystem`, `seccompProfile:
RuntimeDefault`, `/tmp` as an emptyDir with `sizeLimit`), pinned image tags
(explicit Kokoro-FastAPI version), `values.schema.json` for validation.

Probes: generous `startupProbe` (migrations), `readinessProbe` on `/readyz`
(database reachable only — the UI must stay usable while Kokoro restarts or
loads its model; Kokoro's state is shown in the UI instead), `livenessProbe` on `/healthz`
(independent of running jobs, so long renders never trigger a restart).
Kokoro: generous startup probe (model load).

Delivery: chart as an **OCI artifact** at
`oci://ghcr.io/yoadey/charts/audiobook-generator`, image at
`ghcr.io/yoadey/audiobook-generator`, both versioned by Git tag. The cluster
repo (`fluxcd-talos-cluster`) can then consume it through a `HelmRelease`
with an OCI source; Renovate handles updates.

---

## 8. Container image

- Multi-stage build: Node (Vite build) → Go (build with embedded
  `web/dist`, `CGO_ENABLED=0` — possible because the SQLite driver is pure
  Go) → runtime image with **ffmpeg** (`debian:stable-slim` + ffmpeg or a
  slim static ffmpeg), non-root user.
- Target platform `linux/amd64` (homelab); `arm64` optional.
- The Kokoro image is **not** built here; the official Kokoro-FastAPI CPU
  image is used, pinned.

---

## 9. CI/CD (GitHub Actions)

| Workflow | Contents |
|---|---|
| `ci.yaml` (PR + main) | `go vet`, golangci-lint, `go test -race` with a **matrix over SQLite and PostgreSQL** (Postgres as a service container); oapi-codegen drift check; frontend lint, typecheck, Vitest, build; `helm lint`, **helm-unittest**, `kubeconform` on rendered manifests (both database modes); Docker build (no push) |
| `e2e.yaml` (PR, optionally nightly) | kind cluster, install the chart (with a fake LLM), upload a mini EPUB, render one chapter, check the audio file |
| `release.yaml` (tag `v*`) | push image + chart to ghcr.io, GitHub release |
| Renovate | Go modules, npm, Actions, the Kokoro image tag in the chart |

---

## 10. Testing and quality measurement

- **Fixtures**: public-domain EPUBs (Project Gutenberg, e.g. *Pride and
  Prejudice* — dialogue-heavy) + synthetic edge cases (everything in one
  XHTML file, nested TOC, British quotes, multi-paragraph quotes).
- **Golden tests** for chapter splitting and segmentation.
- **Store tests** run against SQLite and PostgreSQL.
- **LLM layer** tested with recorded responses (fake provider); rate limiter
  and fallback with simulated 429s.
- **Attribution quality**: an evaluation script against an annotated corpus
  (e.g. the *Project Dialogism Novel Corpus*, public-domain novels with
  annotated speakers) — to compare prompts and providers objectively before
  spending quota on the real book.
- **Benchmark** Kokoro on the cluster nodes (real-time factor) as a
  Prometheus metric.

---

## 11. Milestones

Every milestone ends with an installable chart.

| # | Contents | Outcome |
|---|---|---|
| **M0** | Repo skeleton: Go module, GORM store with both dialects + gormigrate, OpenAPI + codegen, React/Mantine skeleton, embed, Dockerfile, chart skeleton (app + kokoro + PVC + database modes), CI | `helm install` starts app + Kokoro, empty UI, works with SQLite and Postgres |
| **M1** | EPUB import, chapter split, segmentation, library + chapter list + text view | The real 12 MB EPUB correctly split into volumes/chapters |
| **M2** | Kokoro integration, jobs/queue, rendering with **one narrator voice**, built-in player, lookahead | **A usable (single-voice) audiobook**; real-time factor measured |
| **M3** | LLM layer: providers, rate limiter, quotas, structured output, settings page | provider test button, quota display |
| **M4** | Character analysis + merge + character UI (list, details, merge/split) | complete character bible of the book |
| **M5** | Casting + voice catalogue + voice editor + previews | every main character has a recipe |
| **M6** | Speaker attribution + multi-voice rendering + chapter editor | **A multi-voice audiobook** |
| **M7** | Pronunciation lexicon, podcast RSS, OIDC, metrics/ServiceMonitor, Litestream, export/import, M4B per volume | feature-complete v1.0 |
| **M8** | Reading along: segment timings recorded while rendering, a reader (page scrolling, sticky player bar, sentence marking) that follows the voice and shares its position with the player, WebVTT transcripts per sentence in the podcast feed; more quotation styles (single, guillemets, low-high, dash dialogue), italics kept and italic/single-quoted thoughts attributed and voiced; spoken-text cleanup; custom sample texts for voice previews (instance, book, character); expression: emotions as pace and loudness, pauses by context, word timings from Kokoro (word marking in the reader, WebVTT lines by word), optional LLM chapter preparation (moods, dramatic pauses, spoken forms, pronunciation suggestions) | read and listen at once |
| **M9** (optional) | segment cache, POV narrator voice, Audiobookshelf export, local LLM fallback | refinements |

Integration into the cluster repo (`HelmRelease`, Zitadel client via
Terraform, secrets via SOPS, Garage bucket for Litestream or a CNPG
database) happens separately there once M2 is done.

---

## 12. Risks

| Risk | Mitigation |
|---|---|
| Free-tier limits change / models disappear | provider list + limits are configuration only, fallback chain, persistent jobs |
| Free tiers use inputs for training | conscious decision, stated in docs/UI; local fallback possible |
| Attribution quality on web-serial prose | measured against an annotated corpus, confidence flags, a fast manual editor |
| Kokoro on CPU slower than expected | M2 measures early; Kokoro replicas scale; lookahead buffers |
| Only ~25 English base voices | weighted mixing + speed; pool for minor characters; minimum distance enforced in code |
| Pathological EPUBs (one giant file, broken TOC) | streaming parser, several fallbacks, manual chapter correction |
| Two database dialects drift apart | no raw SQL outside a tiny dialect interface, CI matrix over both |
| Data loss | PVC + Litestream (SQLite) or the database's own backup (Postgres) + JSON export of the analysis |
| Copyright | private use of your own DRM-free books only, LAN only, no public hosting |

---

## 13. Decisions

| # | Decision |
|---|---|
| 1 | Persistence via **GORM**, supporting **SQLite** (default, self-contained) and **PostgreSQL** (external or CNPG) |
| 2 | **Built-in player + podcast RSS**; Audiobookshelf only as an optional later export |
| 3 | UI library: **Mantine** |
| 4 | Code, docs and README in **English** |
| 5 | Feature branches + PRs against `main`; releases via Git tags |
