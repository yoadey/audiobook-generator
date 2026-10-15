#!/usr/bin/env bash
# End-to-end smoke test on a kind cluster: installs the chart with the image
# built from this checkout (ghcr.io/yoadey/audiobook-generator:e2e, already
# loaded into kind) and checks the API and UI.
#
# Kokoro is replaced by an unreachable external URL (its image is several
# GB): the app must still become ready and report the TTS server as down.
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
  --set tts.kokoro.enabled=false --set tts.externalUrl=http://tts.invalid:8880
  --set persistence.size=1Gi
)
if [[ "$db" == postgres ]]; then
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
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
grep -q '"reachable":false' <<<"$info"
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
curl -fsS "localhost:18080/api/books/$id/chapters" | grep -q '"title":"Chapter 1"'
echo "e2e ($db) passed"
