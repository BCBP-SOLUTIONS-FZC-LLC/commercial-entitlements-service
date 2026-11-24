---
name: Bug report
about: Report a defect in the commercial-entitlements service (HTTP API, event publishing, caching, reconciler, or database layer)
title: '[BUG] '
labels: bug
assignees: ''
---

## Description
A clear description of the bug.

## Service version
`github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements` — tag / commit SHA:

## Go version
`go version goX.Y.Z ...`

## Environment
- [ ] Local dev (`make run`)
- [ ] Docker Compose (`make docker-up`)
- [ ] Staging
- [ ] Production

## Affected area
- [ ] HTTP API (endpoint: `METHOD /internal/v1/...`)
- [ ] Event publishing (`tenant_entitlements_changed`)
- [ ] Event consumption (queue: `billing/tenant/catalog/membership-entitlements-q`)
- [ ] Cache (Valkey — effective set, quota ceiling, usage counter, plan cache)
- [ ] Database / migrations
- [ ] Reconciler job (job name: `override-expiry` / `addon-expiry` / `outbox-prune` / `processed-events-prune` / `quota-reset`)
- [ ] Outbox relay
- [ ] Temporal workflow (catalog fan-out)

## Steps to reproduce
1.
2.
3.

## Expected behaviour
What you expected to happen.

## Actual behaviour
What actually happened. Include error messages, HTTP status codes, log output, or stack traces.

```
// paste relevant log output or error here
```

## Minimal reproduction
```go
// paste the smallest snippet or curl command that triggers the bug
```

## Additional context
Any other relevant context (Postgres version, PgBouncer mode, VALKEY_URL set, TEMPORAL_HOST_PORT set, related issues, tenant ID if safe to share).
