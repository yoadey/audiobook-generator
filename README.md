# audiobook-generator

Turns (very large) English EPUBs into multi-voice audiobooks: a full
character analysis up front, a distinct voice per character, and
chapter-by-chapter generation managed through a web UI.

Go backend, React frontend, shipped as a self-contained Helm chart that
brings the app, the [Kokoro](https://github.com/remsky/Kokoro-FastAPI) TTS
server and storage.

> **Status:** milestone M2 — a usable single-voice audiobook. EPUBs are
> split into volumes, chapters and narration/quote segments (a
> 3-million-word book imports in seconds) and can be reviewed in the UI;
> chapters are rendered with a narrator voice of your choice (Kokoro, voice
> mixes, speed, previews) and played in the built-in player, which resumes
> where you stopped and keeps the next chapters rendered ahead of you.
> Character analysis and one voice per character follow in M3–M6 — see
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
| `ingress.enabled` | `false` | expose the UI |
| `metrics.serviceMonitor.enabled` | `false` | Prometheus Operator scraping of `/metrics` |

## Development

Requirements: Go (version from `go.mod`), Node 24, ffmpeg, Python 3 (fake TTS), Helm 3 with the
[helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin,
[kubeconform](https://github.com/yannh/kubeconform), golangci-lint.

```sh
make fake-tts             # stand-in for Kokoro on :8880 (quiet tones)
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
                          lookahead), audio, store, api, tts, webui
web/                      React + Mantine frontend (built into internal/webui/dist)
charts/audiobook-generator/  Helm chart
hack/                     CI helpers: kubeconform, kind e2e, fake TTS server
docs/PLAN.md              design and milestones
```

Configuration is environment-only (`ABG_*`, see
[`internal/config/config.go`](internal/config/config.go)).
