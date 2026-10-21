# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this repo is

A self-hosted service that turns very large English EPUBs into multi-voice
audiobooks: full character analysis up front (LLM, free-tier OpenAI-compatible
providers), one voice per character (Kokoro TTS on CPU), chapter-by-chapter
just-in-time rendering, managed through a web UI.

The end product is a **self-contained Helm chart** (`charts/audiobook-generator`)
that ships everything it needs (app, Kokoro TTS, storage).

Read `docs/PLAN.md` first — it is the agreed design and milestone plan.

## Stack (fixed)

- Backend: **Go** (`cmd/`, `internal/`)
- Frontend: **React + TypeScript + Mantine** (`web/`), embedded into the Go binary
- Persistence: **GORM** with **SQLite** (default, pure-Go driver, no CGO) or
  **PostgreSQL**, chosen by configuration; migrations via gormigrate
- API contract: OpenAPI (`api/openapi.yaml`) is the single source of truth;
  Go server and TS client are generated from it
- Deployment: Helm chart in `charts/audiobook-generator`, image + chart on ghcr.io

## Commands

```sh
make fake-tts      # stand-in for Kokoro and an LLM on :8880 (no GPU/model/key needed)
make run           # backend on :8080, SQLite in ./tmp-data
make generate      # after editing api/openapi.yaml (Go server + TS client)
make test          # Go tests; set ABG_TEST_POSTGRES_DSN to also test PostgreSQL
make lint          # golangci-lint
make web           # frontend: lint, test, build into internal/webui/dist
make chart-test    # helm lint, helm unittest, kubeconform
```

## Working rules

- Code, docs and README in English.
- Work on feature branches and open PRs against `main`; releases via Git tags.
- Everything in the store layer must work on both SQLite and PostgreSQL: no
  raw SQL outside the small dialect interface, tests run against both.
- Never let the LLM rewrite book text: it only returns compact labels and
  points at text by copying a few of its words (anchors), which code finds
  in the text and checks; what cannot be found is dropped. The import
  segments deterministically; the speaker attribution then splits
  paragraphs where the LLM found speech, thoughts and other voices,
  losing no character (see `docs/PLAN.md` §3).
- Every long-running step is a persistent, resumable job. LLM calls go
  through `llm.Pool`; its quota errors defer jobs (`jobs.Deferrer`) instead
  of failing them.
- Prompts are embedded under `internal/llm/prompts/<name>/v<N>/`; never edit
  a version whose results may be stored — add a new version.
- The character analysis (`internal/analysis`) applies each LLM answer in
  the same transaction that marks its batch done; keep it that way so a
  stop at any point loses nothing and never spends quota twice.
- Pin all image and chart versions (Renovate bumps them).
- Generated files are committed: `internal/api/api.gen.go`,
  `web/src/api/schema.d.ts`. CI fails when they are stale.
- Schema changes are new gormigrate migrations in
  `internal/store/migrations.go`; never edit a released one.
