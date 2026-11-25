# Runbook: Outbox Dead Letter

**Alert:** `CommercialEntitlementsOutboxDeadLetters`
**Severity:** Critical — `TenantEntitlementsChanged` events are being lost; downstream billing and quota enforcement may be stale.

## Symptoms

- `platform_dlq_messages_total{operation="outbox_publish",reason="max_attempts"}` rising
- `TenantEntitlementsChanged` events not reaching downstream consumers

## Immediate Actions

1. Query dead-lettered records (platform-events moves them from `outbox_events` to `outbox_dead_letters` after OUTBOX_RETENTION_DAYS retries):
   ```sql
   SELECT id, event_type, tenant_id, attempts, last_error, failed_at
   FROM outbox_dead_letters
   ORDER BY failed_at DESC
   LIMIT 20;
   ```
2. Inspect `last_error` for the failure reason.
3. Check Glue Schema Registry — is `TenantEntitlementsChanged` `AVAILABLE`?
   ```bash
   make schema-verify
   ```
4. Check SNS topic ARN: `SNS_TOPIC_ENTITLEMENTS_ARN` is set, IAM role has `sns:Publish`, and the topic exists.

## Root Causes

| Error | Cause | Fix |
|---|---|---|
| `NoCredentialProviders` | IRSA not configured; `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` missing | Configure IRSA role or set secret values |
| `InvalidSchemaVersionIDException` | Schema version not `AVAILABLE` in Glue registry | Run `make schema-register` or trigger `schema-registry.yml` |
| `AuthorizationError` | IRSA role lacks `sns:Publish` on the topic | Update `deploy/aws-iam/policy.json` and re-apply |
| `connection refused` | SNS endpoint unreachable (incorrect `AWS_REGION` or VPC endpoint issue) | Check AWS_REGION and VPC configuration |

## Resolution

1. Fix the root cause (see table above).
2. Replay dead-lettered records using platform-events' `outbox.Runner.ReprocessDeadLetters(ctx, limit)` — **never** hand-write SQL against `outbox_dead_letters` or `outbox_events`.
3. Monitor `platform_dlq_messages_total{operation="outbox_publish"}` — it should stop rising once the fix is deployed.

## ENT-INV-3 — Drawdown idempotency

If the dead-lettered event was caused by a drawdown, the drawdown itself is durable (committed to Postgres). Replaying the outbox event does not double-count.
