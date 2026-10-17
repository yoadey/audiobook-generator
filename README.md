# audiobook-generator

Turns (very large) English EPUBs into multi-voice audiobooks: a full
character analysis up front, a distinct voice per character, and
chapter-by-chapter generation managed through a web UI.

Go backend, React frontend, shipped as a self-contained Helm chart that
brings the app, the [Kokoro](https://github.com/remsky/Kokoro-FastAPI) TTS
server and storage.

> **Status:** milestone M8 — a multi-voice audiobook in which every
> character speaks with their own voice, to listen to and read along,
> plus pronunciations, podcast feeds with transcripts, M4B downloads,
> sign-in, backups and export/import. EPUBs are split into volumes, chapters and narration/quote segments
> (a 3-million-word book imports in seconds) and can be reviewed in the UI;
> chapters are rendered with a narrator voice of your choice (Kokoro, voice
> mixes, speed, previews) and played in the built-in player, which resumes
> where you stopped and keeps the next chapters rendered ahead of you. Free
> LLM providers (any OpenAI-compatible endpoint) can be configured with
> their rate limits and daily quotas, tested and watched in the UI; they
> read the whole book once to build its character list (names, aliases,
> gender, age, role, speech style, chapters, point of view), which can be
> corrected, merged and split in the UI. The main characters are then cast
> with voices of their own (LLM-proposed Kokoro blends that code keeps
> distinct from the narrator and each other), everyone else with pool
> voices by gender and age; voices can be edited and previewed with real
> quotes. Before a chapter is rendered, the LLM attributes its quotations to
> their speakers (just in time, in parts for long chapters); the chapter
> editor shows who says what, flags unsure speakers and lets you correct
> them. A per-book pronunciation lexicon fixes how fantasy names are
> spoken (plain text or Kokoro phonemes, with previews); every book has a
> podcast RSS feed for any podcast app and an M4B per volume with chapter
> markers. Sign-in via OpenID Connect (e.g. Zitadel), Prometheus metrics,
> Litestream backups of SQLite to S3 and an export/import of a book with
> everything its analysis cost round it off. The player shows the text
> along with the voice (also as a transcript in podcast apps), and voice
> previews speak sample texts of your choice. See
> [`docs/PLAN.md`](docs/PLAN.md).

## Install

```sh
helm install audiobooks oci://ghcr.io/yoadey/charts/audiobook-generator \
  --namespace audiobooks --create-namespace
kubectl -n audiobooks port-forward svc/audiobooks-audiobook-generator 8080:80
```

Common options (all values: [`charts/audiobook-generator/values.yaml`](charts/audiobook-generator/values.yaml)):

| Value | Default | Purpose |
|---|---|---|
| `database.type` | `sqlite` | `sqlite` (file on the data volume) or `postgres` |
| `database.postgres.mode` | `external` | `external` (host/user/password or `existingSecret`) or `cnpg` (renders a CloudNativePG `Cluster`) |
| `persistence.size` | `50Gi` | EPUBs, rendered audio, SQLite file |
| `maxUploadMB` | `512` | largest accepted EPUB (raise your ingress body limit to match) |
| `tts.kokoro.enabled` | `true` | bundled Kokoro-FastAPI (CPU); set `false` + `tts.externalUrl` to use your own |
| `tts.defaultVoice` | `af_heart` | narrator of books without an own voice |
| `tts.concurrency` | `2` | parallel synthesis requests (~1–2 per Kokoro replica) |
| `llm.providers` | `[]` | OpenAI-compatible LLM providers with limits; keys from Secrets (see below). More can be added in the UI |
| `llm.analysisBatchTokens` | `30000` | input tokens per character analysis request (fewer, larger requests save daily request quota) |
| `llm.attributionTokens` | `12000` | input tokens per speaker attribution request (longer chapters are sent in parts) |
| `casting.mainCharacters` | `50` | speaking characters that get a voice recipe of their own; the rest speak with pool voices |
| `ingress.enabled` | `false` | expose the UI |
| `publicUrl` | `""` | external URL (`https://audiobooks.example.com`) for feed links and the sign-in redirect; empty: taken from each request |
| `auth.oidc.enabled` | `false` | require a sign-in via OpenID Connect (see below) |
| `feed.enabled` | `true` | podcast RSS feed per book; the chart generates the token key |
| `backup.litestream.enabled` | `false` | stream the SQLite database to S3 and restore it onto a fresh volume |
| `metrics.serviceMonitor.enabled` | `false` | Prometheus Operator scraping of `/metrics` |

### Sign-in

With `auth.oidc.enabled` (needs `publicUrl`), every page and API call needs a sign-in at your
OpenID Connect provider (authorization code flow with PKCE; listening
positions are kept per user). Register a web application with the
redirect URI `<publicUrl>/auth/callback` and the post-logout URI
`<publicUrl>/`, then:

```yaml
publicUrl: https://audiobooks.example.com
auth:
  oidc:
    enabled: true
    issuer: https://id.example.com
    clientId: "123456789@audiobooks"
    clientSecret: { existingSecret: audiobooks-oidc, key: client-secret }  # omit for a public client
```

Who may sign in is decided by the provider (e.g. Zitadel's "check
authorization on authentication" with a project role). Health probes,
`/metrics` and the podcast feeds stay reachable without a session; feed
URLs carry a token instead, so treat them like passwords.

### Reading along, podcast feeds, downloads, backup

- **Reading along**: the listen page is a reader with a small player bar
  at the bottom. It marks the sentence and the word being spoken (word
  times come from Kokoro-FastAPI; other TTS servers give sentences only)
  and follows the voice
  (scroll away and *Back to the voice* returns). A click on a passage
  listens from there; scrolling while paused moves the listening position
  to what you read (also in chapters without audio yet), so reading and
  listening continue at the same place. Text size and serif/sans are under
  *Aa*, chapters in the list beside it; space plays and pauses, the arrow
  keys skip.
- **Quotation marks and thoughts**: speech in double or single marks
  (curly or straight), guillemets, „low-high“ marks or dash dialogue is
  recognized per book. Italics are kept: runs of a few words in italics
  (and, in books whose speech uses double marks, runs in single ones) are
  thoughts, attributed like quotations and spoken with the thinker's voice.
  Thought detection applies to books imported from now on (import a book
  again to get it).
- **Spoken text**: footnote markers, brackets of game texts ([Skill]) and
  decoration are not read; shouted capitals and Roman chapter numbers are
  read as words.
- **Expression**: the emotion of a quote (from the speaker attribution or
  set by hand) changes its pace and loudness; pauses follow the text:
  short between lines of a dialogue, longer after speech that trails off,
  almost none after speech that is cut off, and a pause at an ellipsis
  within a sentence.
- **Chapter preparation** (optional, per book under *Narrator*): before a
  chapter is rendered, the LLM marks the mood of the narration, dramatic
  pauses and notation the voice would misread ("Lv. 5" → "level five"),
  and suggests pronunciations of invented names, which you accept or
  dismiss under *Pronunciation*. It only changes how the text is spoken,
  never its words: spoken forms are accepted only for notation (numbers,
  symbols, abbreviations) and must keep every other word. The chapter page
  shows what was found and can prepare it again.
- **Podcast**: the book page shows a feed URL (button *Podcast*) that any
  podcast app can subscribe to; rendered chapters are its episodes, each
  with a WebVTT transcript (`podcast:transcript`) that apps such as Apple
  Podcasts, Pocket Casts or AntennaPod show along the audio. Chapters
  rendered before reading along existed get estimated timings until they
  are rendered again.
- **Sample texts**: voice previews say a text from *Settings*, a book's own
  (narrator settings) or, for a character, quotes from the book or a text
  of its own (voice editor).
- **Downloads**: one M4B with chapter markers and cover per volume (card
  *Downloads*); missing chapters are rendered first.
- **Backup**: with SQLite, `backup.litestream` (S3 endpoint, bucket and a
  Secret with `access-key-id`/`secret-access-key`) keeps the database in
  S3 (Kubernetes 1.29+). Audio is not backed up: after a restore onto a
  new volume the app notices the missing files and renders them again.
- **Export/import**: *Export* on the book page downloads a zip with the
  book, its characters, voices, speakers, chapter edits and pronunciations
  (no audio); *Import export* in the library restores it as a new book,
  also on another instance or database.

### LLM providers

The character analysis and speaker attribution (from M6 on) use free tiers
of OpenAI-compatible LLM APIs. A 5-million-word book needs roughly 200
analysis requests (about 7M input tokens), i.e. a day or two of free quota. Providers are tried in order; when one hits its
rate limit or daily quota the next takes over, and when all are exhausted
the work waits until a quota resets. Configure them in the UI
(**Settings**) or in the values:

```yaml
llm:
  providers:
    - name: gemini
      baseUrl: https://generativelanguage.googleapis.com/v1beta/openai/
      model: gemini-2.5-flash
      apiKeySecret: { name: audiobook-llm-keys, key: gemini }
      limits: { rpm: 10, rpd: 250 }      # those of your plan
      quotaTimezone: America/Los_Angeles  # Gemini resets at midnight Pacific
    - name: groq
      baseUrl: https://api.groq.com/openai/v1/
      model: llama-3.3-70b-versatile
      apiKeySecret: { name: audiobook-llm-keys, key: groq }
      structuredOutput: json_object       # if the model rejects JSON schemas
```

Use **Test** on the settings page to check key, model and structured
output. Book text is sent to these providers; free tiers often allow them
to train on it.

## Development

Requirements: Go (version from `go.mod`), Node 24, ffmpeg, Python 3 (fake TTS), Helm 3 with the
[helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin,
[kubeconform](https://github.com/yannh/kubeconform), golangci-lint.

```sh
make fake-tts             # stand-in for Kokoro (quiet tones) and an LLM on :8880
make run                  # backend on :8080 with SQLite in ./tmp-data
cd web && npm ci && npm run dev   # frontend on :5173, proxies /api to :8080

make generate             # after editing api/openapi.yaml
make test                 # Go tests; also PostgreSQL with:
                          #   ABG_TEST_POSTGRES_DSN="host=localhost user=postgres dbname=postgres sslmode=disable"
make lint web chart-test  # everything CI checks
make image                # container image
```

Layout:

```
api/openapi.yaml          API contract (source of truth)
cmd/audiobook-generator/  main
internal/                 Go packages: epub (parser), segment (quotes, chunking),
                          importer, jobs (persistent queue), render (TTS + ffmpeg,
                          lookahead), llm (providers, limits, prompts), analysis
                          (character bible), casting (voices), attribution (speakers),
                          lexicon (pronunciations), feed (podcast RSS), assemble (M4B),
                          auth (OIDC), transfer (export/import), metrics,
                          audio, store, api, tts, webui
web/                      React + Mantine frontend (built into internal/webui/dist)
charts/audiobook-generator/  Helm chart
hack/                     CI helpers: kubeconform, kind e2e, fake TTS/LLM server
docs/PLAN.md              design and milestones
```

Configuration is environment-only (`ABG_*`, see
[`internal/config/config.go`](internal/config/config.go)).
