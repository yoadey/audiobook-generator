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
  # An LLM provider from the values, served by the fake (its key from a Secret).
  --set 'llm.providers[0].name=fake'
  --set 'llm.providers[0].baseUrl=http://fake-tts:8880/v1/'
  --set 'llm.providers[0].model=fake-small'
  --set 'llm.providers[0].apiKeySecret.name=llm-keys'
  --set 'llm.providers[0].apiKeySecret.key=fake'
  --set 'llm.providers[0].limits.rpd=100'
)
kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" create configmap fake-tts --from-file="$root/hack/fake-tts/server.py" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" create secret generic llm-keys --from-literal=fake=e2e-llm-key \
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
books="$(curl -fsS localhost:18080/api/books)"
grep -q '"total":0' <<<"$books"
page="$(curl -fsS localhost:18080/library)"
grep -q '<div id="root">' <<<"$page"

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
# Reading along: the render recorded when each segment is spoken.
timings="$(curl -fsS "localhost:18080/api/chapters/$chapter/timings")"
grep -q '"exact":true' <<<"$timings"
grep -q '"segmentId"' <<<"$timings"

# The configured LLM provider answers the structured-output test, with the
# key from the Secret (the fake rejects anything else).
providers="$(curl -fsS localhost:18080/api/llm/providers)"
echo "$providers"
grep -q '"source":"config"' <<<"$providers"
provider="$(python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])' <<<"$providers")"
result="$(curl -fsS -X POST "localhost:18080/api/llm/providers/$provider/test")"
echo "$result"
grep -q '"ok":true' <<<"$result"
providers="$(curl -fsS localhost:18080/api/llm/providers)"
grep -q '"requests":1' <<<"$providers"

# The character analysis finds the speakers of the sample book.
curl -fsS -X POST "localhost:18080/api/books/$id/analysis" >/dev/null
for _ in $(seq 1 60); do
  analysis="$(curl -fsS "localhost:18080/api/books/$id/analysis")"
  grep -qE '"status":"(done|failed)"' <<<"$analysis" && break
  sleep 1
done
echo "$analysis"
grep -q '"status":"done"' <<<"$analysis"
characters="$(curl -fsS "localhost:18080/api/books/$id/characters")"
echo "$characters"
grep -q '"name":"Anna"' <<<"$characters"
grep -q '"name":"Tom"' <<<"$characters"

# The finished analysis casts voices for the speaking characters.
for _ in $(seq 1 60); do
  casting="$(curl -fsS "localhost:18080/api/books/$id/casting")"
  grep -qE '"status":"(done|failed)"' <<<"$casting" && break
  sleep 1
done
echo "$casting"
grep -q '"status":"done"' <<<"$casting"
grep -q '"voicedCount":2' <<<"$casting"
mains="$(curl -fsS "localhost:18080/api/books/$id/characters?tier=main")"
echo "$mains"
grep -q '"voiceSource":"own"' <<<"$mains"

# Rendering a chapter now attributes its speakers first.
chapter2="$(curl -fsS "localhost:18080/api/books/$id/chapters" |
  python3 -c 'import sys, json; print([c["id"] for c in json.load(sys.stdin)["items"] if c["title"] == "Chapter 2"][0])')"
curl -fsS -X POST "localhost:18080/api/chapters/$chapter2/render?force=true" >/dev/null
for _ in $(seq 1 90); do
  detail="$(curl -fsS "localhost:18080/api/chapters/$chapter2")"
  grep -q '"audioStatus":"ready"' <<<"$detail" && grep -q '"attributionStatus":"done"' <<<"$detail" &&
    grep -q '"audioRerendering":false' <<<"$detail" && ! grep -q '"audioStale":true' <<<"$detail" && break
  sleep 1
done
echo "$detail" | head -c 2000; echo
grep -q '"attributionStatus":"done"' <<<"$detail"
grep -q '"speakerId"' <<<"$detail"
grep -q '"audioStatus":"ready"' <<<"$detail"
grep -q '"audioRerendering":false' <<<"$detail"
if grep -q '"audioError"' <<<"$detail"; then exit 1; fi

# Podcast feed: works without a session and lists the rendered chapters.
feed="$(curl -fsS "localhost:18080/api/books/$id" |
  python3 -c 'import sys, json; print(json.load(sys.stdin)["feedPath"])')"
rss="$(curl -fsS "localhost:18080$feed")"
grep -q '<rss' <<<"$rss"
grep -q '<enclosure' <<<"$rss"
# Each episode links its transcript (WebVTT).
transcript="$(python3 -c 'import re,sys; print(re.search(r"<podcast:transcript url=\"([^\"]+)\"", sys.stdin.read()).group(1))' <<<"$rss")"
vtt="$(curl -fsS "localhost:18080/${transcript#*://*/}")"
head -c 300 <<<"$vtt"; echo
grep -q '^WEBVTT' <<<"$vtt"
grep -q ' --> ' <<<"$vtt"

# The sample text of voice previews.
curl -fsS -X PATCH -H 'Content-Type: application/json' -d '{"previewText":"Call me Ishmael."}' \
  localhost:18080/api/settings | grep -q '"previewText":"Call me Ishmael."'

# M4B of the first volume (its missing chapters are rendered first).
volume="$(curl -fsS "localhost:18080/api/books/$id/downloads" |
  python3 -c 'import sys, json; print(json.load(sys.stdin)[0]["volumeId"])')"
curl -fsS -X POST "localhost:18080/api/books/$id/downloads/$volume" >/dev/null
for _ in $(seq 1 240); do
  downloads="$(curl -fsS "localhost:18080/api/books/$id/downloads")"
  grep -q '"status":"ready"' <<<"$downloads" && break
  grep -q '"status":"failed"' <<<"$downloads" && break
  sleep 1
done
echo "$downloads"
grep -q '"status":"ready"' <<<"$downloads"
code="$(curl -sS -o /tmp/volume.m4b -w '%{http_code}' "localhost:18080/api/books/$id/downloads/$volume/file")"
test "$code" = 200
test -s /tmp/volume.m4b

# Export and import as a new book.
curl -fsS -o /tmp/book.abg.zip "localhost:18080/api/books/$id/export"
restored="$(curl -fsS -F "file=@/tmp/book.abg.zip" localhost:18080/api/import |
  python3 -c 'import sys, json; print(json.load(sys.stdin)["id"])')"
for _ in $(seq 1 60); do
  book="$(curl -fsS "localhost:18080/api/books/$restored")"
  grep -q '"status":"importing"' <<<"$book" || break
  sleep 1
done
echo "$book"
grep -q '"status":"imported"' <<<"$book"
mains="$(curl -fsS "localhost:18080/api/books/$restored/characters?tier=main")"
grep -q '"voiceSource":"own"' <<<"$mains"

echo "e2e ($db) passed"
