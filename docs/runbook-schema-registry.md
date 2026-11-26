# Runbook — AWS Glue Schema Registry (commercial-entitlements-events)

## The contract

`commercial-entitlements` publishes to one SNS topic backed by one AWS Glue Schema Registry. At startup, `eventbus.NewGlueCodec` resolves the produced schema's version UUID **by definition**: `glue:GetSchemaByDefinition` with the binary's own embedded `schemas/tenant_entitlements_changed.json`. The version must be `AVAILABLE`.

- Each build stamps the version that describes its payloads — never the registry's latest.
- The UUID is fixed for the life of the pod. There is **no refresher**.
- **Startup fails fast** if the definition is not registered — the pod will not accept traffic.

On the consume side: `events.GlueDecodeCodec` strips the 18-byte header without a registry call. Consumed payloads are validated against embedded schemas in `internal/adapter/inbound/sqspipeline/`.

## Registry

| Registry | Env var | Schema | Glue name |
|---|---|---|---|
| `commercial-entitlements-events` | `GLUE_REGISTRY_ENTITLEMENTS_NAME` | `tenant_entitlements_changed.json` | `TenantEntitlementsChanged` |

## Schema version lifecycle

Schema changes must be **backward-compatible** with all running pods before the registry is updated. The process:

1. Update `api/asyncapi.yaml` (source of truth) with the new field.
2. Run `schema-gov extract --asyncapi api/asyncapi.yaml --schema-dir internal/adapter/outbound/eventbus/schemas` to regenerate the JSON schema.
3. Push to `main` — `schema-registry.yml` CI workflow validates and registers the new version in staging.
4. Deploy the new service image (which embeds the new schema version).
5. Trigger `schema-registry.yml` with `environment=production` to register in production.

## Troubleshooting

### Pod fails to start: "schema version not AVAILABLE"

```bash
# Verify the schema version status in Glue:
aws glue get-schema-by-definition \
  --schema-id "RegistryName=commercial-entitlements-events,SchemaName=TenantEntitlementsChanged" \
  --schema-definition "$(cat internal/adapter/outbound/eventbus/schemas/tenant_entitlements_changed.json)" \
  --region ap-south-1

# If version not found or status is PENDING/FAILURE:
# Trigger schema-registry.yml workflow manually.
```

### Outbox events dead-lettered: "InvalidSchemaVersionIDException"

The pod registered a version UUID at startup that the registry no longer serves (deleted, or wrong registry). This should not happen in normal operation. Resolution: redeploy the pod after ensuring the schema version is `AVAILABLE`.

### DLQ messages: "schema_violation"

A consumed event's payload fails the embedded consumed-event schema. The event is forwarded to the DLQ with `DLQReason=schema_violation`. The producing service has broken its contract or this service's `api/asyncapi.yaml` is stale.

1. Inspect the DLQ message body.
2. Compare against `api/asyncapi.yaml` consumed schema for the event type.
3. Fix the wrong side (producer or this service) and redrive.

## Schema freeze

If `SCHEMA_FREEZE=true` is set in the GitHub environment, schema registration is blocked. The `freeze-watchdog.yml` workflow alerts after 24h/72h. To lift:

```bash
gh variable delete SCHEMA_FREEZE --env staging
gh variable delete SCHEMA_FREEZE --env production
```
