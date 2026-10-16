# syntax=docker/dockerfile:1

# --- Frontend -----------------------------------------------------------------
FROM node:24.22.0-alpine3.24 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# Vite writes into ../internal/webui/dist, the Go embed directory.
RUN mkdir -p ../internal/webui/dist && npm run build

# --- Backend ------------------------------------------------------------------
FROM golang:1.27.2-trixie AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webui/dist/ internal/webui/dist/
ARG VERSION=dev
ARG COMMIT=unknown
RUN go build -trimpath \
      -ldflags "-s -w \
        -X github.com/yoadey/audiobook-generator/internal/buildinfo.Version=${VERSION} \
        -X github.com/yoadey/audiobook-generator/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/audiobook-generator ./cmd/audiobook-generator

# --- Runtime ------------------------------------------------------------------
# Debian slim rather than distroless: ffmpeg is needed for audio assembly.
FROM debian:trixie-20261005-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home-dir /data --shell /usr/sbin/nologin abg \
 && mkdir -p /data && chown abg /data
COPY --from=build /out/audiobook-generator /usr/local/bin/audiobook-generator
USER 10001
ENV ABG_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/audiobook-generator"]
