# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.x     | ✅ Active |

Older major versions are not patched. Deploy the latest 1.x image.

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Email: vijay@bcbpsolutions.com
Subject: `[commercial-entitlements] Security vulnerability`

Include in your report:
- Description of the vulnerability and the affected component (HTTP handler, RLS policy, event payload, cache, reconciler job, etc.)
- Steps to reproduce
- Potential impact (tenant data leakage, RLS bypass, quota bypass, privilege escalation, DoS, etc.)
- Suggested fix or patch (if any)

### Response timeline

| Step | Target |
|------|--------|
| Initial acknowledgement | 48 hours |
| Severity assessment | 5 business days |
| Patch release (critical/high) | 14 days |
| Public disclosure | After patch ships |

We follow responsible disclosure. Reporters will be credited in release notes unless anonymity is requested.

## Scope

Areas of particular sensitivity in this service:

- **Row-Level Security (RLS)** — tenant isolation enforced at the Postgres layer; a bypass would expose cross-tenant entitlement data, quota balances, and override history.
- **GUC injection** — `app.tenant_id` is set per-request via `pgcommon.GUCSetFromContext` (SET LOCAL); incorrect values would allow cross-tenant writes or reads.
- **Quota addon drawdown** — `POST /addons/drawdown` decrements `remaining_tokens`; a replay or idempotency bypass could allow unlimited quota consumption.
- **Override writes** — `PUT /overrides/:limit_key` sets hard limits on tenant capabilities; unauthorized writes could suppress or inflate tenant limits.
- **Outbox payloads** — `tenant_entitlements_changed` events are validated against JSON Schemas before insert; a bypass could propagate malformed data to the quota enforcer and downstream billing systems.
- **Valkey quota ceiling** — the `quota:ceiling:*` keys gate quota enforcement; a cache poisoning attack could artificially inflate or suppress tenant quotas.
