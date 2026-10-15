# audiobook-generator

Turns (very large) English EPUBs into multi-voice audiobooks: a full
character analysis up front, a distinct voice per character, and
chapter-by-chapter generation managed through a web UI.

Go backend, React frontend, shipped as a self-contained Helm chart that
brings the app, the [Kokoro](https://github.com/remsky/Kokoro-FastAPI) TTS
server and storage.

> **Status:** milestone M0 (skeleton). The app starts, migrates its database
> (SQLite or PostgreSQL), reports its status and serves an empty library.
> EPUB import, character analysis and rendering follow in M1–M7 — see
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
| `tts.kokoro.enabled` | `true` | bundled Kokoro-FastAPI (CPU); set `false` + `tts.externalUrl` to use your own |
| `ingress.enabled` | `false` | expose the UI |
| `metrics.serviceMonitor.enabled` | `false` | Prometheus Operator scraping of `/metrics` |

## Development

Requirements: Go (version from `go.mod`), Node 24, Helm 3 with the
[helm-unittest](https://github.com/helm-unittest/helm-unittest) plugin,
[kubeconform](https://github.com/yannh/kubeconform), golangci-lint.

```sh
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
internal/                 Go packages (store, api, tts, webui, …)
web/                      React + Mantine frontend (built into internal/webui/dist)
charts/audiobook-generator/  Helm chart
hack/                     CI helper scripts (kubeconform, kind e2e)
docs/PLAN.md              design and milestones
```

Configuration is environment-only (`ABG_*`, see
[`internal/config/config.go`](internal/config/config.go)).
