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

## Working rules

- Code, docs and README in English.
- Work on feature branches and open PRs against `main`; releases via Git tags.
- Everything in the store layer must work on both SQLite and PostgreSQL: no
  raw SQL outside the small dialect interface, tests run against both.
- Never let the LLM rewrite book text: segmentation is deterministic code, the
  LLM only returns compact labels (see `docs/PLAN.md` §3).
- Every long-running step is a persistent, resumable job.
- Pin all image and chart versions.
