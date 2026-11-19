# commercial-entitlements

Go service. Module `github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements`. Manages tenant entitlement snapshots, quota addon packs, manual overrides, trial assignments, and shortfall workflows for the XpertPMS commercial domain. Hexagonal architecture with strict layer boundaries enforced by `.go-arch-lint.yml`.

## Context files in this directory

| File | Covers |
|------|--------|
| [`architecture.md`](architecture.md) | Package tree, shared library integration, go-arch-lint boundary table, Temporal fan-out |
| [`database-schema.md`](database-schema.md) | Tables, ENUMs, RLS policies, triggers, migration rules, concurrency invariants |
| [`api-caching-events.md`](api-caching-events.md) | Full route catalogue, Valkey key patterns/TTLs, produced and consumed events, schema governance |
| [`key-files.md`](key-files.md) | Per-file descriptions for every critical Go file |
| [`operations.md`](operations.md) | Prometheus metrics, reconciler jobs, deployment (Helm), configuration env vars, security |
| [`request-flows.md`](request-flows.md) | Step-by-step flows for recompute, drawdown, consumers, override, trial, concurrency patterns |

## Essential commands

```bash
make setup          # cp .env-example .env  (first time only)
make docker-up      # start postgres, valkey, temporal, localstack
make run            # source .env && go run ./cmd/server  :8080
make migrate        # run cmd/migrate (applies pending migrations)
make build          # compile all 6 binaries into bin/
make test-unit      # unit tests (no docker)
make test           # all tests (docker up first)
make test-ci        # race + coverage — what CI runs
make lint           # golangci-lint ./...
make arch-lint      # go-arch-lint boundary check
make fmt-check      # CI gofmt gate
make swag           # regenerate docs/swagger/ from annotations
make ci             # tidy + fmt-check + vet + lint + test-ci + build
```

## Binaries

| Binary | Source | Purpose |
|--------|--------|---------|
| `server` | `cmd/server` | HTTP API + 4 SQS consumers + outbox runner + audit runner |
| `reconciler` | `cmd/reconciler` | CronJob dispatcher (`--job=<name>`) |
| `worker` | `cmd/worker` | Temporal workflow worker |
| `migrate` | `cmd/migrate` | Schema migration (Helm pre-upgrade hook) |
| `expiry` | `cmd/expiry` | Addon/override expiry sweep (can run standalone) |
| `catalog-refresh` | `cmd/catalog-refresh` | Pull IAM plan catalog into Postgres + Valkey cache |

## Infrastructure dependencies

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL 16 | 5432 | State, RLS, transactional outbox (`outbox_events`), audit (`pending_audit_entries`) |
| Valkey 7 | 6379 | Advisory cache (effective set, quota ceilings, usage counters, plan definitions) |
| Temporal | 7233 | Optional — catalog fan-out workflow; skipped when `TEMPORAL_HOST_PORT` unset |
| LocalStack / AWS | 4566 | SNS (produced events), SQS (consumed events), Glue schema registry |

## Invariants (abbreviated — full list in LLD and in `.claude/agents/invariant-reviewer.md`)

| ID | Rule |
|----|------|
| ENT-INV-1 | Snapshot version monotonically increasing; upsert only if `incoming > stored` |
| ENT-INV-3 | Drawdown idempotent via `request_id`; duplicate returns existing, no double-deduct |
| ENT-INV-4 | Expired overrides/addons excluded from effective set |
| ENT-INV-5 | Trial overrides only applied when `state == EntitlementStateTrial` |
| ENT-INV-6 | Recompute is a no-op when computed set equals latest snapshot (no write, no event) |
| CONC-1 | Optimistic locking on 7 tables; `record_version` in WHERE; `ErrOptimisticLockConflict` on 0 rows |
| EVT-10 | `Enqueue` inside `RunInTx`; event and state commit atomically |
| CONS-1..4 | Transactional outbox; never hand-roll `outbox_events` INSERT |
| RLS-1 | Every tenant-scoped table: RLS enabled+forced, `tenant_isolation` policy, GUC transaction-local |
| RLS-6 | GUC is `SET LOCAL`, never session-scoped; never leaks across PgBouncer pooled backends |
| MIG-1 | Migrations on `origin/main` are immutable; always add a new numbered file |
| CACHE-1 | Cache errors degrade gracefully; Valkey is never a correctness dependency |

## Hooks in this session

| Trigger | Hook | Blocks |
|---------|------|--------|
| PreToolUse Edit/Write/MultiEdit | `hooks/guard-invariants.sh` | `.env`, immutable migrations (MIG-1), session-scoped RLS SET (RLS-6), `database/sql`, raw `pgxpool`/`pgx.Connect`, SNS/SQS SDK outside `cmd/server/main.go`, `outbox.Enqueue*` outside `eventbus/publisher.go`, adapter imports in `internal/core/*` |
| PostToolUse Edit/Write/MultiEdit | `hooks/gofmt.sh` | Runs `gofmt -w` on the touched `.go` file |

## Agents

| Agent | Use for |
|-------|---------|
| `invariant-reviewer` | Before any PR — checks all ENT-INV-*, CONC, EVT, RLS, MIG, CACHE invariants against the diff |
| `docs-consistency-reviewer` | Before any PR that changes behaviour, routes, events, env vars, CronJob names — catches stale docs |

## Skills

| Skill | Invocation |
|-------|-----------|
| new-migration | `/new-migration <name>` — scaffolds numbered migration pair with RLS + touch_row() + GRANT |
| swag-regen | `/swag-regen` — `make swag` + `git diff docs/swagger/` |
