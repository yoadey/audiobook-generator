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
- Frontend: **React + TypeScript** (`web/`), embedded into the Go binary
- API contract: OpenAPI (`api/openapi.yaml`) is the single source of truth;
  Go server and TS client are generated from it
- Deployment: Helm chart in `charts/audiobook-generator`, image + chart on ghcr.io

## Working rules

- Work on feature branches and open PRs against `main`.
- Never let the LLM rewrite book text: segmentation is deterministic code, the
  LLM only returns compact labels (see `docs/PLAN.md` §3).
- Every long-running step is a persistent, resumable job.
- Pin all image and chart versions.
