# audiobook-generator

Turns (very large) English EPUBs into multi-voice audiobooks: a full
character analysis up front, a distinct voice per character, and
chapter-by-chapter generation managed through a web UI.

Go backend, React frontend, shipped as a self-contained Helm chart that
brings the app, the [Kokoro](https://github.com/remsky/Kokoro-FastAPI) TTS
server and storage.

> **Status:** milestone M5 — a usable single-voice audiobook plus the
> character bible and a voice for every character. EPUBs are split into volumes, chapters and narration/quote segments
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
> quotes. Rendering with these voices follows in M6 — see
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
| `casting.mainCharacters` | `50` | speaking characters that get a voice recipe of their own; the rest speak with pool voices |
| `ingress.enabled` | `false` | expose the UI |
| `metrics.serviceMonitor.enabled` | `false` | Prometheus Operator scraping of `/metrics` |

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
                          (character bible), casting (voices), audio, store, api,
                          tts, webui
web/                      React + Mantine frontend (built into internal/webui/dist)
charts/audiobook-generator/  Helm chart
hack/                     CI helpers: kubeconform, kind e2e, fake TTS/LLM server
docs/PLAN.md              design and milestones
```

Configuration is environment-only (`ABG_*`, see
[`internal/config/config.go`](internal/config/config.go)).
