# audiobook-generator Helm chart

Self-contained deployment of
[audiobook-generator](https://github.com/yoadey/audiobook-generator): the app
(API, job runner, web UI), the Kokoro-FastAPI TTS server and storage.

```sh
helm install audiobooks oci://ghcr.io/yoadey/charts/audiobook-generator
```

## What gets deployed

| Resource | When |
|---|---|
| Deployment + Service `app` (1 replica, `Recreate`) | always |
| PVC `<release>-data` (kept on uninstall) | `persistence.enabled` and no `existingClaim` |
| Deployment + Service `kokoro` | `tts.kokoro.enabled` (default) |
| CloudNativePG `Cluster` | `database.type=postgres`, `database.postgres.mode=cnpg` |
| Secret with the Postgres password | external Postgres with `password` and no `existingSecret` |
| Ingress / ServiceMonitor / NetworkPolicy | when enabled |

## Database modes

```yaml
# SQLite (default): file on the data volume, nothing else needed.
database:
  type: sqlite
---
# Existing PostgreSQL server.
database:
  type: postgres
  postgres:
    mode: external
    host: postgres.db.svc
    database: audiobook
    user: audiobook
    existingSecret: audiobook-db   # key: password
---
# PostgreSQL via the CloudNativePG operator (must be installed).
database:
  type: postgres
  postgres:
    mode: cnpg
    cnpg:
      instances: 1
      storage:
        size: 5Gi
```

## LLM providers

```yaml
llm:
  providers:
    - name: gemini
      baseUrl: https://generativelanguage.googleapis.com/v1beta/openai/
      model: gemini-2.5-flash
      apiKeySecret: { name: audiobook-llm-keys, key: gemini }   # existing Secret
      limits: { rpm: 10, tpm: 250000, rpd: 250 }
      quotaTimezone: America/Los_Angeles
```

Providers from the values are shown read-only in the UI (Settings), where
more can be added. Each `apiKeySecret` becomes an environment variable of
the app; the provider list itself is passed as JSON (`ABG_LLM_PROVIDERS`).

All values are documented in [`values.yaml`](values.yaml) and validated by
[`values.schema.json`](values.schema.json).
