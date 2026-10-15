#!/usr/bin/env bash
# Renders the chart in every database/TTS mode and validates the manifests
# (CRDs such as the CNPG Cluster and ServiceMonitor via the datree catalog).
set -euo pipefail

chart="$(dirname "$0")/../charts/audiobook-generator"
kube_version="${KUBE_VERSION:-1.34.0}"

modes=(
  ""
  "--set database.type=postgres --set database.postgres.host=db --set database.postgres.password=x"
  "--set database.type=postgres --set database.postgres.mode=cnpg"
  "--set tts.kokoro.enabled=false --set tts.externalUrl=http://tts:8880 --set persistence.enabled=false"
  "--set ingress.enabled=true --set metrics.serviceMonitor.enabled=true --set networkPolicy.enabled=true"
  "--set llm.providers[0].name=g --set llm.providers[0].baseUrl=https://llm --set llm.providers[0].model=m --set llm.providers[0].apiKeySecret.name=s --set llm.providers[0].apiKeySecret.key=k"
)

for mode in "${modes[@]}"; do
  echo "--- helm template ${mode:-(defaults)}"
  # shellcheck disable=SC2086 # word splitting of $mode is intended
  helm template abg "$chart" $mode |
    kubeconform -strict -summary -kubernetes-version "$kube_version" \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
done
