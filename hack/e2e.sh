#!/usr/bin/env bash
# End-to-end smoke test on a kind cluster: installs the chart with the image
# built from this checkout (ghcr.io/yoadey/audiobook-generator:e2e, already
# loaded into kind) and checks the API and UI.
#
# Kokoro is replaced by hack/fake-tts (its image is several GB), which is
# enough to render real audio files through ffmpeg in the app image.
#
# Usage: hack/e2e.sh [sqlite|postgres]
set -euo pipefail

db="${1:-sqlite}"
ns=abg-e2e
release=abg
root="$(dirname "$0")/.."

diagnostics() {
  echo "::group::diagnostics"
  kubectl -n "$ns" get all,pvc || true
  kubectl -n "$ns" describe pods || true
  kubectl -n "$ns" logs -l app.kubernetes.io/component=app --tail=200 || true
  echo "::endgroup::"
}
trap diagnostics ERR

args=(
  --namespace "$ns" --create-namespace --wait --timeout 5m
  --set image.tag=e2e --set image.pullPolicy=Never
  --set tts.kokoro.enabled=false --set tts.externalUrl=http://fake-tts:8880
  --set persistence.size=1Gi
)
kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" create configmap fake-tts --from-file="$root/hack/fake-tts/server.py" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" apply -f "$root/hack/e2e-fake-tts.yaml"
kubectl -n "$ns" rollout status deploy/fake-tts --timeout 3m
if [[ "$db" == postgres ]]; then
  kubectl -n "$ns" apply -f "$root/hack/e2e-postgres.yaml"
  kubectl -n "$ns" rollout status deploy/postgres --timeout 3m
  args+=(
    --set database.type=postgres
    --set database.postgres.host=postgres
    --set database.postgres.password=e2e
    --set database.postgres.sslMode=disable
  )
fi

helm upgrade --install "$release" "$root/charts/audiobook-generator" "${args[@]}"
helm test "$release" --namespace "$ns" --logs

kubectl -n "$ns" port-forward "svc/$release-audiobook-generator" 18080:80 >/dev/null 2>&1 &
pf=$!
trap 'kill $pf' EXIT
for _ in $(seq 1 30); do
  curl -fsS localhost:18080/healthz >/dev/null 2>&1 && break
  sleep 1
done

info="$(curl -fsS localhost:18080/api/system)"
echo "$info"
grep -q "\"dialect\":\"$db\"" <<<"$info"
grep -q '"reachable":true' <<<"$info"
curl -fsS localhost:18080/api/books | grep -q '"total":0'
curl -fsS localhost:18080/library | grep -q '<div id="root">'

# Upload the sample EPUB and wait for its import.
book="$(curl -fsS -F "file=@$root/hack/testdata/sample.epub" localhost:18080/api/books)"
echo "$book"
id="$(sed -E 's/.*"id":([0-9]+).*/\1/' <<<"$book")"
for _ in $(seq 1 60); do
  book="$(curl -fsS "localhost:18080/api/books/$id")"
  grep -q '"status":"importing"' <<<"$book" || break
  sleep 1
done
echo "$book"
grep -q '"status":"imported"' <<<"$book"
grep -q '"chapterCount":4' <<<"$book"
chapters="$(curl -fsS "localhost:18080/api/books/$id/chapters")"
grep -q '"title":"Chapter 1"' <<<"$chapters"

# Start "listening" at Chapter 1: the lookahead renders it (and more).
chapter="$(python3 -c 'import json,sys; print(next(c["id"] for c in json.load(sys.stdin)["items"] if c["title"] == "Chapter 1"))' <<<"$chapters")"
curl -fsS -X PUT -H 'Content-Type: application/json' \
  -d "{\"chapterId\":$chapter,\"positionMs\":0}" "localhost:18080/api/books/$id/progress"
for _ in $(seq 1 90); do
  detail="$(curl -fsS "localhost:18080/api/chapters/$chapter")"
  grep -qE '"audioStatus":"(ready|failed)"' <<<"$detail" && break
  sleep 1
done
grep -q '"audioStatus":"ready"' <<<"$detail" || { echo "$detail" | head -c 600; exit 1; }
code="$(curl -sS -o /tmp/chapter.m4a -w '%{http_code}' -r 0-1023 "localhost:18080/api/chapters/$chapter/audio")"
[[ "$code" == 206 ]]
[[ "$(wc -c </tmp/chapter.m4a)" -eq 1024 ]]
echo "e2e ($db) passed"
