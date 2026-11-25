# AWS IAM policy for commercial-entitlements

This directory holds the reference IAM policy that must be attached to the
service's IRSA role. The application does not create the role itself — that is
managed by platform Terraform. Copy the JSON in `policy.json` into the
Terraform module or apply it directly via `aws iam create-policy` /
`aws iam put-role-policy`.

The role is assumed by the service's Kubernetes ServiceAccount via IRSA. Wire
the ARN into Helm via `serviceAccount.annotations`:

```yaml
serviceAccount:
  create: true
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::ACCOUNT_ID:role/commercial-entitlements
```

The SAME role is assumed by every CronJob rendered from
`deploy/helm/templates/cronjobs.yaml` (all 5 reconcilers share the pod
ServiceAccount). None of them need permissions beyond the base set.

## What commercial-entitlements needs

commercial-entitlements is a snapshot-of-record service that publishes
entitlement change events and consumes billing, membership, and catalog events.
It does **not** store binary blobs, does **not** encrypt with a service-owned
KMS key, and does **not** touch S3.

## Grants breakdown

| Sid | AWS service | Actions | Purpose |
|---|---|---|---|
| `PublishEntitlementsEvents` | SNS | `Publish`, `GetTopicAttributes` | Publishes `TenantEntitlementsChanged` events to the `commercial-entitlements-events` SNS topic |
| `ConsumeInboundQueues` | SQS | `ReceiveMessage`, `DeleteMessage`, `GetQueueAttributes`, `GetQueueUrl`, `ChangeMessageVisibility`, `SendMessage` | Three SQS consumers (`billing-entl-q`, `membership-entl-q`, `catalog-entl-q`) and their DLQs. `SendMessage` is for DLQ forwarding by platform-events |
| `GlueSchemaRegistryReadOnly` | Glue | `GetRegistry`, `GetSchema`, `GetSchemaVersion`, `GetSchemaByDefinition`, `ListSchemas`, `QuerySchemaVersionMetadata` | The Glue codec pre-fetches schema version IDs on startup and validates every published envelope against `commercial-entitlements-events` |
| `CloudWatchMetrics` | CloudWatch | `PutMetricData` (scoped to `BCBP/CommercialEntitlements`) | Optional metric mirroring from the outbox runner goroutines |
| `CloudWatchLogs` | Logs | `CreateLogStream`, `PutLogEvents` | Container stdout when the OTel collector is not in the log pipeline |

## Explicitly out of scope

- **No S3.** commercial-entitlements never reads, writes, or lists S3 objects.
- **No KMS.** RDS-at-rest encryption uses an AWS-managed key.
- **No STS AssumeRole beyond IRSA.**

## Rolling out

1. Apply the new policy in Terraform / IAM console.
2. Verify each grant from within a debug pod sharing the ServiceAccount:
   ```bash
   aws sns publish --topic-arn <entitlements_topic_arn> --message test
   aws sqs get-queue-attributes --queue-url <billing_entl_queue_url>
   aws glue get-registry --registry-id.RegistryName=commercial-entitlements-events
   aws cloudwatch put-metric-data --namespace BCBP/CommercialEntitlements \
       --metric-name TestMetric --value 1
   ```
3. Deploy the new image + Helm chart.
4. Watch `platform_dlq_messages_total{operation="outbox_publish"}` — a rising rate
   immediately after rollout means the SNS Publish was denied. Roll back if so.
