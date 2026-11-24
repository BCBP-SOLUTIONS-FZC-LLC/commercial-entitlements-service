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

# Test package paths — tests live alongside source (*_test.go in each package).
TEST_UNIT_PKGS     := ./internal/core/...
TEST_POSTGRES_PKGS := ./internal/adapter/outbound/postgres/...
TEST_INT_PKGS      := ./internal/adapter/inbound/... ./internal/adapter/outbound/valkey/... ./internal/adapter/temporal/...
TEST_CMD_PKGS      := ./cmd/...

TEST_POSTGRES_PARALLEL ?= 4

# Full package list under coverage measurement — computed once at parse time.
COVER_PKG_LIST := $(shell $(GO) list ./internal/... 2>/dev/null | tr '\n' ',' | sed 's/,$$//')

DOCKER_SOCKET := $(shell [ -S /Users/$(USER)/.docker/run/docker.sock ] && echo unix:///Users/$(USER)/.docker/run/docker.sock || echo unix:///var/run/docker.sock)
export DOCKER_HOST ?= $(DOCKER_SOCKET)
export APP_NAME APP_ENV BUILD_VERSION

.PHONY: help setup tidy fmt fmt-check vet lint arch-lint build run migrate \
        test test-unit test-postgres test-integration test-ci \
        _test-unit _test-postgres _test-integration _merge-coverage \
        cover cover-func \
        docker-up docker-down docker-build swag swag-check trivy vuln-check check-metrics ci clean mod-verify

help:
	@echo "Available targets: setup tidy fmt fmt-check vet lint arch-lint build run migrate"
	@echo "                   test test-unit test-postgres test-integration test-ci"
	@echo "                   cover cover-func"
	@echo "                   docker-up docker-down docker-build swag swag-check trivy vuln-check ci clean"
	@echo "  make test-unit       - unit tests (no docker needed)"
	@echo "  make test-postgres   - postgres integration tests (docker up first)"
	@echo "  make test-ci         - all suites with race + coverage, merge to coverage.out"
	@echo "  make cover           - open HTML coverage report (runs test-ci)"
	@echo "  make cover-func      - function-level coverage summary (runs test-ci)"
	@echo "  make swag-check      - fail if Swagger regeneration would change docs/swagger/ (CI drift gate)"
	@echo "  make trivy           - build the image and Trivy-scan it like CI (CRITICAL/HIGH/UNKNOWN, fixed only); run by .githooks/pre-push"
	@echo "  make vuln-check      - run govulncheck on internal and cmd packages"
	@echo "  make check-metrics   - validate metric names, labels, suffixes, and tier compliance"

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

# ─────────────────────────────────────────────────────────────────────────────
# TESTS
# ─────────────────────────────────────────────────────────────────────────────

# Human-friendly plain-output variants (no coverage, verbose).
test-unit:
	$(GO) test $(TEST_UNIT_PKGS) -count=1 -timeout 60s -v

test-postgres:
	$(GO) test $(TEST_POSTGRES_PKGS) -tags=integration -count=1 \
	  -timeout 900s -parallel $(TEST_POSTGRES_PARALLEL) -v

test-integration:
	$(GO) test $(TEST_INT_PKGS) -tags=integration -count=1 -timeout 300s -v

test:
	$(GO) test ./... -count=1 -timeout 120s

# Private per-suite targets used by test-ci to write .coverage/ profiles.
_test-unit: | .coverage
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_CMD_PKGS) \
	  -race -count=1 -timeout 120s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/unit.out

_test-postgres: | .coverage
	{ $(GO) test $(TEST_POSTGRES_PKGS) \
	  -tags=integration -race -count=1 -timeout 1500s -parallel $(TEST_POSTGRES_PARALLEL) \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/postgres.out \
	  2>&1; echo $$? >.coverage/postgres.exitcode; } | tee .coverage/postgres.raw; \
	_exit=$$(cat .coverage/postgres.exitcode 2>/dev/null || echo 1); \
	[ "$$_exit" = "0" ] || { \
	  printf '\n\n=== FAILING POSTGRES TESTS (see full log above) ===\n'; \
	  grep '^--- FAIL:' .coverage/postgres.raw || printf '(no --- FAIL lines — check for DATA RACE or panic above)\n'; \
	  printf '====================================================\n\n'; \
	}; \
	exit "$$_exit"

_test-integration: | .coverage
	$(GO) test $(TEST_INT_PKGS) \
	  -tags=integration -race -count=1 -timeout 300s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/integration.out

# Merge the three per-suite profiles into a single coverage.out (max-count
# strategy — any suite covering a block wins).
_merge-coverage:
	@python3 scripts/merge_coverage.py \
	  .coverage/unit.out .coverage/postgres.out .coverage/integration.out \
	  > coverage.out
	@echo "==> coverage.out merged from all suites (max-count strategy)"

test-ci: | .coverage
	$(MAKE) -j3 _test-unit _test-postgres _test-integration
	$(MAKE) _merge-coverage

# ─────────────────────────────────────────────────────────────────────────────
# COVERAGE
# ─────────────────────────────────────────────────────────────────────────────

cover: test-ci
	$(GO) tool cover -html=coverage.out

cover-func: test-ci
	$(GO) tool cover -func=coverage.out

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

swag-check:
	bash .github/scripts/check-swagger-stale.sh

# trivy: local mirror of CI's "Trivy CVE scan" gate — build the amd64 image,
# then fail on CRITICAL/HIGH/UNKNOWN CVEs that have a fix (.trivyignore applies
# automatically). The Dockerfile uses the go_private_token build secret:
# GO_PRIVATE_TOKEN from the environment, else `gh auth token`.
TRIVY_IMAGE ?= commercial-entitlements-trivy
GO_PRIVATE_TOKEN ?= $(shell gh auth token 2>/dev/null)
export GO_PRIVATE_TOKEN

trivy:
	@command -v trivy >/dev/null || { echo "trivy not found (brew install trivy)"; exit 1; }
	@test -n "$$GO_PRIVATE_TOKEN" || { echo "GO_PRIVATE_TOKEN is empty: export it or run 'gh auth login'"; exit 1; }
	docker build --platform linux/amd64 \
	  --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
	  -t $(TRIVY_IMAGE) .
	trivy image --severity CRITICAL,HIGH,UNKNOWN --ignore-unfixed --exit-code 1 $(TRIVY_IMAGE)

vuln-check:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./internal/... ./cmd/...

check-metrics:
	bash scripts/check-metrics.sh

ci: tidy fmt-check vet lint check-metrics test-ci build

.coverage:
	@mkdir -p .coverage

clean:
	rm -rf bin .coverage
	rm -f coverage.out coverage.html
