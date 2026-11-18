# ─────────────────────────────────────────────────────────────────────────────
# CONFIG
# ─────────────────────────────────────────────────────────────────────────────
-include .env

APP_NAME     ?= commercial-entitlements
APP_ENV      ?= dev
GO           ?= go
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

export GOPRIVATE  ?= github.com/BCBP-SOLUTIONS-FZC-LLC/*
export GONOSUMDB  ?= github.com/BCBP-SOLUTIONS-FZC-LLC/*

DOCKER_SOCKET := $(shell [ -S /Users/$(USER)/.docker/run/docker.sock ] && echo unix:///Users/$(USER)/.docker/run/docker.sock || echo unix:///var/run/docker.sock)
export DOCKER_HOST ?= $(DOCKER_SOCKET)
export APP_NAME APP_ENV BUILD_VERSION

.PHONY: help setup tidy fmt fmt-check vet lint arch-lint build run migrate test test-unit test-ci docker-up docker-down docker-build swag ci clean mod-verify

help:
	@echo "Available targets: setup tidy fmt fmt-check vet lint arch-lint build run migrate test test-unit test-ci docker-up docker-down docker-build swag ci clean"

setup:
	@test -f .env || cp .env-example .env
	@echo "Setup complete"

tidy:
	$(GO) mod tidy

fmt:
	@gofmt -l -w .

fmt-check:
	@unformatted=$$(gofmt -l . 2>/dev/null); \
	[ -n "$$unformatted" ] && { echo "FAIL: unformatted:"; echo "$$unformatted"; exit 1; }; \
	echo "gofmt: ok"

vet:
	$(GO) vet ./...

mod-verify:
	$(GO) mod verify

lint:
	$(GO) tool golangci-lint run ./...

arch-lint:
	bash .github/scripts/arch-lint.sh

build:
	@mkdir -p bin
	$(GO) build -ldflags "-X main.buildVersion=$(BUILD_VERSION)" -o bin/$(APP_NAME)           ./cmd/server
	$(GO) build -ldflags "-X main.buildVersion=$(BUILD_VERSION)" -o bin/reconciler            ./cmd/reconciler
	$(GO) build -o bin/migrate         ./cmd/migrate
	$(GO) build -o bin/worker          ./cmd/worker
	$(GO) build -o bin/expiry          ./cmd/expiry
	$(GO) build -o bin/catalog-refresh ./cmd/catalog-refresh
	@echo "Library compile check..."
	$(GO) build ./internal/...

run:
	@-lsof -ti :$${APP_PORT:-8080} | xargs kill -9 2>/dev/null; true
	bash -c 'set -a && source .env && set +a && BUILD_VERSION=$(BUILD_VERSION) $(GO) run ./cmd/server'

migrate:
	$(GO) run ./cmd/migrate

test-unit:
	$(GO) test ./internal/... -count=1 -timeout 60s -v

test:
	$(GO) test ./... -count=1 -timeout 120s

test-ci:
	$(GO) test ./internal/... ./cmd/... -race -count=1 -timeout 120s -coverprofile=coverage.out

docker-up:
	docker compose up -d postgres valkey temporal localstack

docker-down:
	docker compose down

docker-build:
	docker build -t $(APP_NAME):local .

swag:
	$(GO) tool swag init \
		-g swagger_info.go \
		-d cmd/server,internal/adapter/inbound/http \
		--output docs/swagger \
		--parseDependency \
		--parseInternal

ci: tidy fmt-check vet lint test-ci build

clean:
	rm -rf bin coverage.out
