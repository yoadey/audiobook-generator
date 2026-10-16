# Developer entry points. CI runs the same commands (.github/workflows/ci.yaml).

CHART := charts/audiobook-generator
IMAGE ?= ghcr.io/yoadey/audiobook-generator:dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: all generate build run fake-tts test lint web web-test chart-test image clean

all: generate lint test web chart-test

## generate: regenerate the Go server and the TypeScript client from api/openapi.yaml
generate:
	go generate ./...
	cd web && npm run gen:api

## build: build the binary (embeds whatever is in internal/webui/dist)
build:
	go build -trimpath -ldflags "-X github.com/yoadey/audiobook-generator/internal/buildinfo.Version=$(VERSION) -X github.com/yoadey/audiobook-generator/internal/buildinfo.Commit=$(COMMIT)" -o bin/audiobook-generator ./cmd/audiobook-generator

## run: run the backend locally with SQLite in ./tmp-data (frontend: `cd web && npm run dev`)
run:
	ABG_DATA_DIR=$(CURDIR)/tmp-data ABG_LOG_FORMAT=text go run ./cmd/audiobook-generator

## fake-tts: a stand-in for Kokoro on :8880 (quiet tones) for local runs
fake-tts:
	python3 hack/fake-tts/server.py

## test: Go tests (PostgreSQL too when ABG_TEST_POSTGRES_DSN is set)
test:
	go test -race ./...

## lint: golangci-lint
lint:
	golangci-lint run ./...

## web: install, check and build the frontend into internal/webui/dist
web:
	cd web && npm ci && npm run lint && npm test && npm run build

## chart-test: lint, unit-test and schema-validate the Helm chart
chart-test:
	helm lint --strict $(CHART)
	helm unittest $(CHART)
	hack/kubeconform.sh

## image: build the container image
image:
	docker build -t $(IMAGE) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

clean:
	rm -rf bin tmp-data
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
