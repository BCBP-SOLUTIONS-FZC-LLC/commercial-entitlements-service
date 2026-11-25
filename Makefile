# -----------------------------
# CONFIG
# -----------------------------
-include .env

APP_NAME      ?= commercial-entitlements
APP_ENV       ?= dev
GO            ?= go
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Private modules — resolved via SSH (git@github.com:) using the global URL
# rewrite. No tokens needed for local dev; an SSH key registered with the
# BCBP-SOLUTIONS-FZC-LLC org is required.
export GOPRIVATE  ?= github.com/BCBP-SOLUTIONS-FZC-LLC/*
export GONOSUMDB  ?= github.com/BCBP-SOLUTIONS-FZC-LLC/*

# Docker socket for testcontainers-go (Mac Docker Desktop uses a user socket).
DOCKER_SOCKET     := $(shell [ -S /Users/$(USER)/.docker/run/docker.sock ] && echo unix:///Users/$(USER)/.docker/run/docker.sock || echo unix:///var/run/docker.sock)
export DOCKER_HOST ?= $(DOCKER_SOCKET)

export APP_NAME APP_ENV BUILD_VERSION

# Test package groups (explicit to handle per-group build tags cleanly).
TEST_UNIT_PKGS     := ./test/unit/...
TEST_POSTGRES_PKGS := ./test/postgres/...
TEST_INT_PKGS      := ./test/integration/...
TEST_E2E_PKGS      := ./test/e2e/...
TEST_HELM_PKGS     := ./test/helm/...

# White-box (package-internal) tests included in unit runs.
TEST_INTERNAL_PKGS := ./cmd/server/... \
                      ./cmd/reconciler/jobs/... \
                      ./internal/core/service/... \
                      ./internal/adapter/inbound/http/... \
                      ./internal/adapter/inbound/consumer/... \
                      ./internal/adapter/inbound/sqspipeline/... \
                      ./internal/adapter/outbound/eventbus/... \
                      ./internal/adapter/outbound/postgres/... \
                      ./internal/adapter/outbound/metrics/... \
                      ./internal/adapter/outbound/iamclient/... \
                      ./internal/adapter/outbound/auditrecorder/... \
                      ./internal/adapter/outbound/valkey/... \
                      ./internal/core/port/...

# Every test/postgres/*.go test func calls t.Parallel() and spins its own
# testcontainers Postgres. Capped to avoid Docker resource-contention flakes.
# Override on the CLI (e.g. `make test-postgres TEST_POSTGRES_PARALLEL=8`).
TEST_POSTGRES_PARALLEL ?= 4

# Every build tag the test/ tree declares. vet and lint run a second pass with
# all of them — CI's quality gate calls `make vet`/`make lint`, so without it
# tagged test files are never vetted or linted.
ALL_TEST_TAGS := integration,e2e,helm

# Full package list under coverage measurement — computed once at parse time.
# No pkg/ in this service (unlike iam-org-membership which has pkg/requestctx).
COVER_PKG_LIST := $(shell $(GO) list ./internal/... 2>/dev/null | tr '\n' ',' | sed 's/,$$//')

SCHEMA_GOV_IMAGE    ?= ghcr.io/bcbp-solutions-fzc-llc/platform-schemagov:0.4
# Image is published linux/amd64 only; Apple Silicon needs this for pull/run.
SCHEMA_GOV_PLATFORM ?= linux/amd64

# -----------------------------
# SETUP
# -----------------------------

.PHONY: setup
setup:
	@test -f .env || cp .env-example .env
	@mkdir -p .git/hooks
	@test -f .githooks/pre-commit && cp .githooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit || true
	@test -f .githooks/pre-push   && cp .githooks/pre-push   .git/hooks/pre-push   && chmod +x .git/hooks/pre-push   || true
	@echo "Environment ready (.env)"

.PHONY: install-hooks
install-hooks:
	@mkdir -p .git/hooks
	@cp .githooks/pre-commit .git/hooks/pre-commit
	@cp .githooks/pre-push   .git/hooks/pre-push
	@chmod +x .git/hooks/pre-commit .git/hooks/pre-push
	@echo "Installed git hooks"

# godoc: serve package documentation locally using pkgsite.
.PHONY: godoc
godoc:
	@echo "Starting pkgsite at http://localhost:8080 — press Ctrl-C to stop"
	$(GO) run golang.org/x/pkgsite/cmd/pkgsite@latest -open .

# pin-base-images: fetch and pin the current SHA digests for Dockerfile base
# images. Writes the digests both to the Dockerfile FROM lines and to
# .docker-digests (a checked-in provenance record). CI verifies the two match.
.PHONY: pin-base-images
pin-base-images:
	@echo "Fetching SHA digests for Dockerfile base images..."
	@GOLANG_DIGEST=$$(docker buildx imagetools inspect golang:1.26.11-alpine --format '{{.Manifest.Digest}}') && \
	 DEBIAN_DIGEST=$$(docker buildx imagetools inspect debian:bookworm-slim --format '{{.Manifest.Digest}}') && \
	 DISTROLESS_DIGEST=$$(docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot --format '{{.Manifest.Digest}}') && \
	 sed -i.bak -E \
	   -e "s|^FROM golang:1.26.11-alpine(@sha256:[0-9a-f]+)?|FROM golang:1.26.11-alpine@$$GOLANG_DIGEST|" \
	   -e "s|^FROM debian:bookworm-slim(@sha256:[0-9a-f]+)?|FROM debian:bookworm-slim@$$DEBIAN_DIGEST|" \
	   -e "s|^FROM gcr.io/distroless/static-debian12:nonroot(@sha256:[0-9a-f]+)?|FROM gcr.io/distroless/static-debian12:nonroot@$$DISTROLESS_DIGEST|" \
	   Dockerfile && rm -f Dockerfile.bak && \
	 echo "golang:1.26.11-alpine $$GOLANG_DIGEST"                        > .docker-digests && \
	 echo "gcr.io/distroless/static-debian12:nonroot $$DISTROLESS_DIGEST" >> .docker-digests && \
	 echo "debian:bookworm-slim $$DEBIAN_DIGEST"                          >> .docker-digests && \
	 echo "Digests written to .docker-digests — commit both Dockerfile and .docker-digests"

.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make setup               - copy .env-example to .env if missing; install .githooks/pre-commit + pre-push"
	@echo "  make tidy                - go mod tidy"
	@echo "  make fmt                 - go fmt ./..."
	@echo "  make fmt-check           - verify gofmt formatting (mirrors CI)"
	@echo "  make vet                 - go vet (default build + every test build tag)"
	@echo "  make lint                - run golangci-lint (default build + every test build tag)"
	@echo "  make arch-lint           - run go-arch-lint against .go-arch-lint.yml"
	@echo "  make test                - unit + postgres + integration tests (requires Docker)"
	@echo "  make test-ci             - test with race detector + coverage (used in CI)"
	@echo "  make test-unit           - unit tests only (no Docker required)"
	@echo "  make test-postgres       - Postgres + RLS integration tests (requires Docker)"
	@echo "  make test-integration    - cross-layer integration tests"
	@echo "  make test-smoke          - CI-only image gate: startup-check"
	@echo "  make race                - all tests with -race flag"
	@echo "  make run                 - run the server locally (go run)"
	@echo "  make build               - compile all 6 binaries to bin/"
	@echo "  make cover               - coverage HTML report"
	@echo "  make cover-func          - coverage summary by function"
	@echo "  make ci                  - tidy + fmt-check + vet + lint + arch-lint + check-metrics + test-ci + build"
	@echo "  make helm-check          - helm lint + template (3 value combos) + kubeconform"
	@echo "  make docker-up           - start local infra: postgres + pgbouncer + valkey + floci + temporal"
	@echo "  make docker-down         - stop local containers"
	@echo "  make mod-verify          - go mod verify"
	@echo "  make vuln-check          - govulncheck on internal + cmd"
	@echo "  make trivy               - build the image and Trivy-scan it like CI (CRITICAL/HIGH/UNKNOWN, fixed only)"
	@echo "  make check-metrics       - validate metric names, labels, suffixes, and tier compliance"
	@echo "  make metrics-lint        - platform-gincommon metricslint over a real /metrics scrape"
	@echo "  make metrics-inventory   - regenerate docs/observability/metric-registry.md"
	@echo "  make extract-schemas     - derive internal/adapter/outbound/eventbus/schemas/*.json from api/asyncapi.yaml"
	@echo "  make swag                - regenerate docs/swagger/ from handler annotations"
	@echo "  make swag-check          - fail if Swagger regeneration would change docs/swagger/ (CI drift gate)"
	@echo "  make install-hooks       - install .githooks/pre-commit + pre-push into .git/hooks"
	@echo "  make godoc               - serve local godoc/pkgsite at http://localhost:8080"
	@echo "  make pin-base-images     - fetch + pin SHA digests for Dockerfile base images"
	@echo "  make clean               - remove build artefacts"
	@echo ""
	@echo "Schema governance (platform-schemagov 0.4):"
	@echo "  make schema-pull         - pull the schema-gov Docker image"
	@echo "  make schema-validate     - validate AsyncAPI + event schemas (no AWS needed)"
	@echo "  make schema-diff         - diff two schemas: CURRENT=<path> PROPOSED=<path>"
	@echo "  make schema-register     - register TenantEntitlementsChanged to commercial-entitlements-events (requires AWS)"
	@echo "  make schema-verify       - pre-deploy check: schema registered + AVAILABLE in Glue (requires AWS)"
	@echo "  make schema-prune        - dry-run: list orphaned Glue schemas"

# -----------------------------
# GO BASICS
# -----------------------------

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: fmt
fmt:
	@gofmt -l -w .

.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l . 2>/dev/null); \
	if [ -n "$$unformatted" ]; then \
		echo "FAIL: unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "gofmt: all files formatted"

.PHONY: mod-verify
mod-verify:
	$(GO) mod verify

.PHONY: vet
vet:
	$(GO) vet ./...
	$(GO) vet -tags=$(ALL_TEST_TAGS) ./...

.PHONY: vuln-check
vuln-check:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./internal/... ./cmd/...

# trivy: local mirror of ci.yml's "Trivy CVE scan" gate — build the amd64
# image, then fail on CRITICAL/HIGH/UNKNOWN CVEs that have a fix (.trivyignore
# applies automatically). GO_PRIVATE_TOKEN from the environment, else `gh auth token`.
TRIVY_IMAGE      ?= commercial-entitlements-trivy
GO_PRIVATE_TOKEN ?= $(shell gh auth token 2>/dev/null)
export GO_PRIVATE_TOKEN

.PHONY: trivy
trivy:
	@command -v trivy >/dev/null || { echo "trivy not found (brew install trivy)"; exit 1; }
	@test -n "$$GO_PRIVATE_TOKEN" || { echo "GO_PRIVATE_TOKEN is empty: export it or run 'gh auth login'"; exit 1; }
	docker build --platform linux/amd64 \
	  --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
	  -t $(TRIVY_IMAGE) .
	trivy image --severity CRITICAL,HIGH,UNKNOWN --ignore-unfixed --exit-code 1 $(TRIVY_IMAGE)

# -----------------------------
# LINT
# -----------------------------

.PHONY: lint
lint:
	@echo "Running linter..."
	$(GO) tool golangci-lint run ./...
	$(GO) tool golangci-lint run --build-tags=$(ALL_TEST_TAGS) ./...

# metrics-lint: platform-gincommon's metricslint over a real /metrics scrape
# and the alert/dashboard/runbook references (same script CI runs).
.PHONY: metrics-lint
metrics-lint:
	bash .github/scripts/metricslint.sh

# metrics-inventory: regenerate docs/observability/metric-registry.md from the
# pinned registry and a real scrape (metrics-lint fails when it is stale).
.PHONY: metrics-inventory
metrics-inventory:
	METRICS_INVENTORY_WRITE=1 bash .github/scripts/metricslint.sh

# check-metrics: validate metric names, labels, suffixes, and tier compliance.
.PHONY: check-metrics
check-metrics:
	bash scripts/check-metrics.sh

# arch-lint: enforce .go-arch-lint.yml component boundaries — the same script
# CI's validate-test.yml runs.
.PHONY: arch-lint
arch-lint:
	bash .github/scripts/arch-lint.sh

# -----------------------------
# TESTS
# -----------------------------

.PHONY: _test-unit
_test-unit: | .coverage
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) \
	  -race -count=1 -timeout 120s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/unit.out

.PHONY: _test-postgres
_test-postgres: | .coverage
	{ $(GO) test $(TEST_POSTGRES_PKGS) \
	  -tags=integration -race -count=1 -timeout 1500s -parallel $(TEST_POSTGRES_PARALLEL) \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/postgres.out \
	  2>&1; echo $$? >.coverage/postgres.exitcode; } | tee .coverage/postgres.raw; \
	_exit=$$(cat .coverage/postgres.exitcode 2>/dev/null || echo 1); \
	[ "$$_exit" = "0" ] || { \
	  printf '\n\n=== FAILING POSTGRES TESTS (see full log above for details) ===\n'; \
	  grep '^--- FAIL:' .coverage/postgres.raw || printf '(no --- FAIL lines — check for DATA RACE or panic above)\n'; \
	  printf '=============================================================\n\n'; \
	}; \
	exit "$$_exit"

.PHONY: _test-integration
_test-integration: | .coverage
	$(GO) test $(TEST_INT_PKGS) \
	  -tags=integration -race -count=1 -timeout 300s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/integration.out

# Merge the three per-suite profiles into a single coverage.out (max-count
# strategy — any suite covering a block wins).
.PHONY: _merge-coverage
_merge-coverage:
	@python3 scripts/merge_coverage.py \
	  .coverage/unit.out .coverage/postgres.out .coverage/integration.out \
	  > coverage.out
	@echo "==> coverage.out merged from all suites (max-count strategy)"

.PHONY: test-ci
test-ci: | .coverage
	$(MAKE) -j3 _test-unit _test-postgres _test-integration
	$(MAKE) _merge-coverage

# Human-friendly plain-output variants (no coverage, verbose).
.PHONY: test-unit
test-unit:
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) -count=1 -timeout 60s -v

.PHONY: test-postgres
test-postgres:
	$(GO) test $(TEST_POSTGRES_PKGS) -tags=integration -count=1 \
	  -timeout 1500s -parallel $(TEST_POSTGRES_PARALLEL) -v

.PHONY: test-integration
test-integration:
	$(GO) test $(TEST_INT_PKGS) -tags=integration -count=1 -timeout 300s -v

.PHONY: test
test:
	$(MAKE) -j3 _test-unit-plain _test-postgres-plain _test-integration-plain

.PHONY: _test-unit-plain _test-postgres-plain _test-integration-plain
_test-unit-plain:
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) -count=1 -timeout 120s -v
_test-postgres-plain:
	$(GO) test $(TEST_POSTGRES_PKGS) -tags=integration -count=1 -timeout 1500s -parallel $(TEST_POSTGRES_PARALLEL) -v
_test-integration-plain:
	$(GO) test $(TEST_INT_PKGS) -tags=integration -count=1 -timeout 300s -v

.PHONY: test-e2e
test-e2e:
	$(GO) test $(TEST_E2E_PKGS) -tags=e2e -count=1 -timeout 300s -v

.PHONY: test-helm
test-helm:
	$(GO) test $(TEST_HELM_PKGS) -tags=helm -count=1 -timeout 60s -v

.PHONY: helm-access-check
helm-access-check: test-helm

.PHONY: test-smoke
test-smoke:
	bash .github/scripts/smoke-tests.sh

.PHONY: race
race:
	$(MAKE) -j3 _test-unit _test-postgres _test-integration

# -----------------------------
# RUN
# -----------------------------

.PHONY: run
run:
	@-lsof -ti :$${APP_PORT:-8080} | xargs kill -9 2>/dev/null; true
	bash -c 'set -a && source .env && set +a && BUILD_VERSION=$(BUILD_VERSION) $(GO) run ./cmd/server'

.PHONY: migrate
migrate:
	bash -c 'set -a && source .env && set +a && $(GO) run ./cmd/migrate'

# -----------------------------
# BUILD
# -----------------------------

.PHONY: build
build:
	@echo "Building binaries..."
	@mkdir -p bin
	$(GO) build -ldflags "-X main.buildVersion=$(BUILD_VERSION)" -o bin/$(APP_NAME)     ./cmd/server
	$(GO) build -ldflags "-X main.buildVersion=$(BUILD_VERSION)" -o bin/reconciler      ./cmd/reconciler
	$(GO) build -ldflags "-s -w"                                 -o bin/migrate         ./cmd/migrate
	$(GO) build -ldflags "-s -w"                                 -o bin/worker          ./cmd/worker
	$(GO) build -ldflags "-s -w"                                 -o bin/expiry          ./cmd/expiry
	$(GO) build -ldflags "-s -w"                                 -o bin/catalog-refresh ./cmd/catalog-refresh
	@echo "Verifying library packages compile..."
	$(GO) build ./internal/...

# -----------------------------
# DOCKER
# -----------------------------

.PHONY: docker-up
docker-up:
	@echo "Starting local PostgreSQL + PgBouncer + Valkey + Floci (SNS/SQS/Glue) + Temporal..."
	docker compose up -d postgres pgbouncer redis floci temporal

.PHONY: docker-down
docker-down:
	@echo "Stopping local containers..."
	docker compose down

.PHONY: docker-build
docker-build:
	@test -n "$$GO_PRIVATE_TOKEN" || { echo "GO_PRIVATE_TOKEN is empty: export it or run 'gh auth login'"; exit 1; }
	docker build --platform linux/amd64 \
	  --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
	  -t $(APP_NAME):local .

# -----------------------------
# CI
# -----------------------------

.PHONY: ci
ci: tidy fmt-check vet lint arch-lint check-metrics test-ci build

# helm-check: lint, render 3 values combos, and kubeconform-validate the chart.
# Mirrors what validate-quality.yml runs in CI.
.PHONY: helm-check
helm-check:
	bash .github/scripts/helm-check.sh

# -----------------------------
# COVERAGE
# -----------------------------

.PHONY: cover
cover: test-ci
	$(GO) tool cover -html=coverage.out

.PHONY: cover-func
cover-func: test-ci
	$(GO) tool cover -func=coverage.out

# -----------------------------
# SCHEMA GOVERNANCE
# -----------------------------

.PHONY: schema-pull
schema-pull:
	docker pull --platform "$(SCHEMA_GOV_PLATFORM)" "$(SCHEMA_GOV_IMAGE)"

.PHONY: extract-schemas
extract-schemas:
	@echo "Extracting event schemas from api/asyncapi.yaml..."
	docker run --rm --platform "$(SCHEMA_GOV_PLATFORM)" \
	  -v "$(CURDIR)":/workspace \
	  "$(SCHEMA_GOV_IMAGE)" extract \
	  --asyncapi   api/asyncapi.yaml \
	  --schema-dir internal/adapter/outbound/eventbus/schemas
	@echo "Done. Run 'git add internal/adapter/outbound/eventbus/schemas/' to stage."

.PHONY: schema-validate
schema-validate: extract-schemas
	docker run --rm --platform "$(SCHEMA_GOV_PLATFORM)" \
	  -v "$(CURDIR)":/workspace \
	  "$(SCHEMA_GOV_IMAGE)" validate \
	  --asyncapi   api/asyncapi.yaml \
	  --schema-dir internal/adapter/outbound/eventbus/schemas

.PHONY: schema-diff
schema-diff:
	@test -n "$(CURRENT)" && test -n "$(PROPOSED)" || { \
	  echo "Usage: make schema-diff CURRENT=<current.json> PROPOSED=<proposed.json>"; \
	  exit 1; \
	}
	docker run --rm --platform "$(SCHEMA_GOV_PLATFORM)" \
	  -v "$(CURDIR)":/workspace \
	  "$(SCHEMA_GOV_IMAGE)" diff \
	  --current     "$(CURRENT)" \
	  --proposed    "$(PROPOSED)" \
	  --schema-name "$(or $(SCHEMA_NAME),$(notdir $(basename $(PROPOSED))))"

# schema-register: register TenantEntitlementsChanged to the single
# commercial-entitlements-events Glue registry.
# Note: `make docker-up` already registers via scripts/init-floci.sh for local dev.
.PHONY: schema-register
schema-register:
	@test -n "$(GLUE_REGISTRY_ENTITLEMENTS_NAME)" || { \
	  echo "GLUE_REGISTRY_ENTITLEMENTS_NAME is not set — add it to .env"; \
	  exit 1; \
	}
	@rm -rf .tmp/glue-entitlements
	@mkdir -p .tmp/glue-entitlements
	@cp internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json \
	    .tmp/glue-entitlements/TenantEntitlementsChanged.json
	docker run --rm --platform "$(SCHEMA_GOV_PLATFORM)" \
	  -v "$(CURDIR)":/workspace \
	  -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN \
	  -e AWS_REGION="$(AWS_REGION)" \
	  -e AWS_ENDPOINT_URL="$(AWS_ENDPOINT_URL)" \
	  "$(SCHEMA_GOV_IMAGE)" register \
	  --registry   "$(GLUE_REGISTRY_ENTITLEMENTS_NAME)" \
	  --schema-dir .tmp/glue-entitlements
	@rm -rf .tmp/glue-entitlements

# schema-verify: fail unless TenantEntitlementsChanged has an AVAILABLE version
# in Glue whose definition matches this checkout's schemas file. Surfaces
# "pod would CrashLoop on NewGlueCodec" pre-deploy.
.PHONY: schema-verify
schema-verify:
	@test -n "$(GLUE_REGISTRY_ENTITLEMENTS_NAME)" || { \
	  echo "GLUE_REGISTRY_ENTITLEMENTS_NAME is not set — add it to .env"; \
	  exit 1; \
	}
	@file=internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json; \
	name=TenantEntitlementsChanged; \
	def=$$(python3 -c 'import json,sys; sys.stdout.write(json.dumps(json.load(open(sys.argv[1])), separators=(",", ":")))' "$$file"); \
	status=$$(aws glue get-schema-by-definition \
	    --schema-id "RegistryName=$(GLUE_REGISTRY_ENTITLEMENTS_NAME),SchemaName=$$name" \
	    --schema-definition "$$def" \
	    --region "$(AWS_REGION)" --query Status --output text 2>/dev/null); \
	if [ "$$status" != "AVAILABLE" ]; then \
	  echo "FAIL: $(GLUE_REGISTRY_ENTITLEMENTS_NAME):$$name status=($${status:-not-registered})"; \
	  echo "      run 'make schema-register' (or wait for schema-registry.yml) to register it"; \
	  exit 1; \
	fi; \
	echo "OK: TenantEntitlementsChanged is registered and AVAILABLE in $(GLUE_REGISTRY_ENTITLEMENTS_NAME)"

# schema-prune: dry-run scan for orphaned Glue schemas. Pass EXECUTE=true to delete.
.PHONY: schema-prune
schema-prune:
	@test -n "$(GLUE_REGISTRY_ENTITLEMENTS_NAME)" || { echo "GLUE_REGISTRY_ENTITLEMENTS_NAME is not set"; exit 1; }
	@rm -rf .tmp/prune-entitlements
	@mkdir -p .tmp/prune-entitlements
	@cp internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json \
	    .tmp/prune-entitlements/TenantEntitlementsChanged.json
	docker run --rm --platform "$(SCHEMA_GOV_PLATFORM)" \
	  -v "$(CURDIR)":/workspace \
	  -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN \
	  -e AWS_REGION="$(AWS_REGION)" \
	  -e AWS_ENDPOINT_URL="$(AWS_ENDPOINT_URL)" \
	  "$(SCHEMA_GOV_IMAGE)" prune \
	  --registry "$(GLUE_REGISTRY_ENTITLEMENTS_NAME)" \
	  --schema-dir .tmp/prune-entitlements \
	  $(if $(filter true,$(EXECUTE)),--execute,)
	@rm -rf .tmp/prune-entitlements

# -----------------------------
# CLEAN
# -----------------------------

.coverage:
	@mkdir -p .coverage

.PHONY: clean
clean:
	rm -rf bin .coverage
	rm -f coverage.out coverage.html

# -----------------------------
# DOCS
# -----------------------------
# OpenAPI (docs/swagger/) is generated from swag annotations via `make swag`.
# AsyncAPI (api/asyncapi.yaml) is hand-maintained; the binary embeds it via
# //go:embed in api/embed.go and serves it at /asyncapi.yaml. No sync needed.

.PHONY: swag
swag:
	@echo "Generating Swagger docs..."
	$(GO) tool swag init \
	  -g swagger_info.go \
	  -d cmd/server,internal/adapter/inbound/http \
	  --output docs/swagger \
	  --parseDependency \
	  --parseInternal
	@python3 scripts/patch-swagger-extensions.py
	@echo "Swagger docs written to docs/swagger/"

.PHONY: swag-check
swag-check:
	bash .github/scripts/check-swagger-stale.sh

.PHONY: docs-serve
docs-serve: run
	@echo "Docs available at:"
	@echo "  http://localhost:8080/          — landing page"
	@echo "  http://localhost:8080/swagger   — Swagger UI (REST APIs)"
	@echo "  http://localhost:8080/asyncapi  — AsyncAPI Studio (Events)"
