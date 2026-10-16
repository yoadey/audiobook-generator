{{/* Chart name. */}}
{{- define "abg.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fully qualified app name. */}}
{{- define "abg.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "abg.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Labels that are not part of any selector. */}}
{{- define "abg.metaLabels" -}}
helm.sh/chart: {{ include "abg.chart" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Common labels of every object. */}}
{{- define "abg.labels" -}}
{{ include "abg.metaLabels" . }}
app.kubernetes.io/name: {{ include "abg.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Selector labels of the app. */}}
{{- define "abg.selectorLabels" -}}
app.kubernetes.io/name: {{ include "abg.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: app
{{- end }}

{{/* Selector labels of Kokoro. */}}
{{- define "abg.kokoro.selectorLabels" -}}
app.kubernetes.io/name: {{ include "abg.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: kokoro
{{- end }}

{{- define "abg.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "abg.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "abg.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "abg.kokoro.fullname" -}}
{{- printf "%s-kokoro" (include "abg.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "abg.cnpg.clusterName" -}}
{{- printf "%s-db" (include "abg.fullname" .) | trunc 50 | trimSuffix "-" }}
{{- end }}

{{- define "abg.secretName" -}}
{{- include "abg.fullname" . }}
{{- end }}

{{/* TTS base URL. */}}
{{- define "abg.ttsUrl" -}}
{{- if .Values.tts.kokoro.enabled }}
{{- printf "http://%s:8880" (include "abg.kokoro.fullname" .) }}
{{- else }}
{{- .Values.tts.externalUrl }}
{{- end }}
{{- end }}

{{/* True when the chart renders its own Secret for the Postgres password. */}}
{{- define "abg.ownsPostgresSecret" -}}
{{- $pg := .Values.database.postgres -}}
{{- if and (eq .Values.database.type "postgres") (eq $pg.mode "external") (not $pg.existingSecret) $pg.password -}}
true
{{- end -}}
{{- end }}

{{/* Fails rendering on inconsistent values. */}}
{{- define "abg.validate" -}}
{{- $db := .Values.database -}}
{{- if not (has $db.type (list "sqlite" "postgres")) }}
{{- fail (printf "database.type must be sqlite or postgres, got %q" $db.type) }}
{{- end }}
{{- if eq $db.type "postgres" }}
{{- if not (has $db.postgres.mode (list "external" "cnpg")) }}
{{- fail (printf "database.postgres.mode must be external or cnpg, got %q" $db.postgres.mode) }}
{{- end }}
{{- if and (eq $db.postgres.mode "external") (not $db.postgres.host) }}
{{- fail "database.postgres.host is required when database.postgres.mode=external" }}
{{- end }}
{{- end }}
{{- $names := dict }}
{{- range $i, $p := .Values.llm.providers }}
{{- if hasKey $names $p.name }}
{{- fail (printf "llm.providers: duplicate name %q" $p.name) }}
{{- end }}
{{- $_ := set $names $p.name true }}
{{- end }}
{{- if and (not .Values.tts.kokoro.enabled) (not .Values.tts.externalUrl) }}
{{- fail "tts.externalUrl is required when tts.kokoro.enabled=false" }}
{{- end }}
{{- end }}

{{/* Database environment variables of the app container. */}}
{{- define "abg.databaseEnv" -}}
- name: ABG_DB_TYPE
  value: {{ .Values.database.type | quote }}
{{- if eq .Values.database.type "postgres" }}
{{- $pg := .Values.database.postgres }}
{{- if eq $pg.mode "cnpg" }}
- name: ABG_DB_POSTGRES_HOST
  value: {{ printf "%s-rw" (include "abg.cnpg.clusterName" .) | quote }}
- name: ABG_DB_POSTGRES_PORT
  value: "5432"
- name: ABG_DB_POSTGRES_DATABASE
  value: {{ $pg.database | quote }}
- name: ABG_DB_POSTGRES_USER
  value: {{ $pg.user | quote }}
- name: ABG_DB_POSTGRES_SSLMODE
  value: "require"
- name: ABG_DB_POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      # Created by the CNPG operator for the bootstrap owner.
      name: {{ printf "%s-app" (include "abg.cnpg.clusterName" .) }}
      key: password
{{- else }}
- name: ABG_DB_POSTGRES_HOST
  value: {{ $pg.host | quote }}
- name: ABG_DB_POSTGRES_PORT
  value: {{ $pg.port | quote }}
- name: ABG_DB_POSTGRES_DATABASE
  value: {{ $pg.database | quote }}
- name: ABG_DB_POSTGRES_USER
  value: {{ $pg.user | quote }}
- name: ABG_DB_POSTGRES_SSLMODE
  value: {{ $pg.sslMode | quote }}
{{- if $pg.existingSecret }}
- name: ABG_DB_POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ $pg.existingSecret }}
      key: {{ $pg.existingSecretPasswordKey }}
{{- else if $pg.password }}
- name: ABG_DB_POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "abg.secretName" . }}
      key: postgres-password
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/* LLM environment variables: the provider list as JSON (keys replaced by
the names of variables that read them from Secrets). */}}
{{- define "abg.llmEnv" -}}
- name: ABG_LLM_MAX_WAIT
  value: {{ .Values.llm.maxWait | quote }}
- name: ABG_LLM_ANALYSIS_BATCH_TOKENS
  value: {{ .Values.llm.analysisBatchTokens | int | quote }}
- name: ABG_LLM_ATTRIBUTION_TOKENS
  value: {{ .Values.llm.attributionTokens | int | quote }}
- name: ABG_CASTING_MAIN_CHARACTERS
  value: {{ .Values.casting.mainCharacters | int | quote }}
{{- with .Values.llm.providers }}
{{- $providers := list }}
{{- range $i, $p := . }}
{{- $q := omit $p "apiKeySecret" }}
{{- if $p.apiKeySecret }}
{{- $_ := set $q "apiKeyEnv" (printf "ABG_LLM_PROVIDER_%d_API_KEY" $i) }}
{{- end }}
{{- $providers = append $providers $q }}
{{- end }}
- name: ABG_LLM_PROVIDERS
  value: {{ toJson $providers | quote }}
{{- range $i, $p := . }}
{{- with $p.apiKeySecret }}
- name: {{ printf "ABG_LLM_PROVIDER_%d_API_KEY" $i }}
  valueFrom:
    secretKeyRef:
      name: {{ .name }}
      key: {{ .key }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
