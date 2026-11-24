---
name: Feature request
about: Propose a new endpoint, limit key, event type, reconciler job, cache strategy, or service behaviour
title: '[FEAT] '
labels: enhancement
assignees: ''
---

## Problem / motivation
What problem does this solve? Which consumers or use cases are affected?

## Proposed solution
Describe the API or behaviour change you'd like.

```go
// Example: new endpoint, payload struct, limit key constant, or domain type
```

## Affected areas
- [ ] HTTP API (new or changed endpoint)
- [ ] Domain model (new limit key, entitlement state, or entity)
- [ ] Database schema (new migration required)
- [ ] Event contract (new event type or changed payload — `api/asyncapi.yaml` + `eventbus/schemas/` update needed)
- [ ] Cache strategy (new key pattern or TTL)
- [ ] Reconciler job (new or changed CronJob)
- [ ] Temporal workflow (catalog fan-out change)
- [ ] Helm / deployment config

## Event contract impact
If this adds or changes an event type, describe the downstream consumer impact (quota enforcer, billing service, audit log).

## LLD alignment
Reference the LLD section (`docs/lld/commercial-lld-entitlements-service.md`) this feature aligns with or proposes adding to.

## Alternatives considered
Other approaches you evaluated and why you ruled them out.

## Acceptance criteria
- [ ]
- [ ]
- [ ]

## Additional context
Links to related issues, HLD sections, ADRs, or prior art.
