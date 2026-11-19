## Summary

<!-- One paragraph: what this PR does and why. Link to the LLD section or issue it implements. -->

## Type of change

- [ ] Bug fix
- [ ] New feature / endpoint
- [ ] Domain model change (new field, entity, or limit key)
- [ ] Database migration (new `NNNNNN_<name>.{up,down}.sql`)
- [ ] Event contract change (`api/asyncapi.yaml` or `eventbus/schemas/`)
- [ ] Cache strategy change (new key pattern or TTL)
- [ ] Reconciler job change (`cmd/reconciler/jobs/`)
- [ ] Helm / deployment configuration
- [ ] Refactoring / test coverage
- [ ] Documentation / observability

## Checklist

### Domain & invariants
- [ ] No ENT-INV-* invariants violated (run `/invariant-reviewer` agent if unsure)
- [ ] No adapter imported from `internal/core/domain` or `internal/core/port`
- [ ] New limit keys added to `domain.KnownLimitKeys`

### Database
- [ ] New migration file follows `NNNNNN_<name>.{up,down}.sql` naming (MIG-1)
- [ ] New tables have `ENABLE ROW LEVEL SECURITY`, `FORCE ROW LEVEL SECURITY`, `tenant_isolation` policy, `REVOKE ALL FROM PUBLIC`, and explicit `GRANT`
- [ ] `touch_row()` trigger added for tables with `record_version`
- [ ] Optimistic lock check (`WHERE id=$1 AND record_version=$2`) on UPDATE

### Events & cache
- [ ] New event type has a JSON Schema in `eventbus/schemas/` and entry in `api/asyncapi.yaml`
- [ ] New cache keys documented in `.claude/api-caching-events.md`
- [ ] Cache errors are advisory only — business operation does not fail on Valkey error

### API
- [ ] `make swag` run and `docs/swagger/` committed (or no handler annotations changed)
- [ ] New routes registered in `internal/adapter/inbound/http/router.go`

### Quality
- [ ] `make ci` passes locally (`tidy` + `fmt-check` + `vet` + `lint` + `test-ci` + `build`)
- [ ] `CHANGELOG.md` updated under `[Unreleased]`

## Migration notes

<!-- If this PR adds a migration: state what the migration does, whether it is backward-compatible with the current running version, and whether a reconciler run is needed after deploy. -->

## Test coverage

<!-- Describe which paths are covered by new/updated tests. If no tests added, explain why. -->

## Reviewer focus

<!-- Where should reviewers concentrate? E.g. "Please scrutinise the RLS policy on the new table" or "Check the drawdown FIFO ordering". -->
