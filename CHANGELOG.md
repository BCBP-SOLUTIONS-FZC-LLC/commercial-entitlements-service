# Changelog

All notable changes to this service are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning aligned with SemVer.

This service has never been deployed to any environment — there is no released version yet, so everything lives under `[Unreleased]`. Because of that, this file tracks only what is currently true and what is still outstanding, not a phase-by-phase build history; the full development diary is available via `git log` if it's ever needed.

## [Unreleased]

### Fixed — trial profile resolution and ENT-O3 audit (HLD v0.68 reply, E-6 / E-8)

- **E-6 Default trial tenants received the paid plan's limits.** `computeEffectiveSet` applied Catalog trial-profile values only when a `tenant_trial_assignments` row existed; with no row (the default Trial-Specific case) nothing was applied, so Enterprise trials were unlimited. No row now means `trial_specific`, and ENT-I1 returns `trial_profile: "trial_specific"`. `custom` now uses the `trial_specific` entry as its base (it looked up a `custom` entry that Catalog never publishes); `full_plan` keeps the plan row. New tests cover the no-row, `trial_specific`, `reduced_plan` (with `NULL` inherit), `full_plan`, `custom` and non-trial cases.
- **E-8 ENT-O3 had no reason and no audit.** `PUT /operator/tenants/:id/entitlements/trial-profile` now requires `reason` and writes AL-5 `config.trial_profile.set` (with `previous_profile` on change) in the assignment transaction. `recordAddonAudit` renamed `recordAudit`, since it is now shared. Swagger regenerated. **Pending:** Audit Log has to add `config.trial_profile.set` to its taxonomy; until then the entries wait in `pending_audit_entries` (platform-audit treats the 422 as version skew).
- **ENT-INV-6 guard ignored `trial_profile`.** `effectiveSetUnchanged` compared plan, state, model tier, flags and limits only, so a profile change with identical limits (e.g. `trial_specific` → `custom`, or the E-6 `NULL` → `trial_specific` backfill) left ENT-I1 stale. Existing trial tenants get one snapshot write and one `TenantEntitlementsChanged` on their next recompute. A Catalog plan with no entry for the resolved trial profile now logs a warning (the trial falls back to the plan limits).
- `scripts/test_runner.py`, `scripts/run_need_test.py`: ENT-O3 body matched to the real contract (it never matched `AssignTrialProfileBody`).
- LLD rev 1.24: ENT-O3 rewritten to the implemented contract; operator trial route paths corrected (`/entitlements/trial-profile`, `/entitlements/trial-overrides/:key`).

### Changed — dependencies updated to latest (2026-11-27)

- Every module in `go.mod` updated to its latest version (`go get -u -t ./...`, `go get -u tool`, `go mod tidy`). Runtime: `github.com/redis/go-redis/v9` v9.24.0 → v9.24.1 and indirect patch/minor bumps (e.g. `aws/smithy-go`); platform libraries were already current. Tooling: `golangci-lint` v2.5.0 → v2.15.0 (with its linters), swag's `go-openapi` dependencies (generated Swagger unchanged). Test stack: testcontainers' `moby`, `klauspost/compress`, `gopsutil`.
- `.golangci.yml`: the test-path exclusions were under the golangci-lint v1 key `issues.exclude-rules`, which v2 ignores (`config verify` failed). Moved to `linters.exclusions.rules`; with v2.15's stricter `noctx` they now apply as intended.
- Valkey adapter tests: the shared container now also waits for the mapped port, and per-test setup uses a separate client with normal timeouts. The service client's deliberate 50 ms read / 100 ms dial budget (CACHE-1) made the cold first command intermittently time out under `-race`.

### Fixed — Billing hand-off F-3 / F-4 (BL-OQ-20 W-5, W-6)

- **F-3 Billing refunds could be applied twice.** A refund was idempotent only on the envelope ID, and `processed_events` is pruned after 8 days, so a DLQ redrive, a visibility-timeout cycle past the prune, or a re-publish under a new envelope ID removed tokens again. New append-only ledger `quota_addon_refunds` (migration `000005`, RLS, SELECT/INSERT grant) keyed by Billing `refund_id`; `ApplyBillingRefund` claims it with `ON CONFLICT DO NOTHING` in the same transaction as the pack update (replay → no change; `refund_id` of another tenant, or reused for another purchase of the same tenant → `commercial_entitlements_billing_ref_conflict_total{reason="refund_ref_other_tenant"|"refund_ref_other_purchase"}`, ack). `ApplyBillingRefundRequest.BillingRef` and the refund event's `billing_ref` are now `uuid.UUID`. `AddonService.WithRefundRepository`; without it refunds fail closed.
- **ENT-D16 — `total_tokens` is immutable; refunds live in the ledger and pack status.** A refund no longer touches `total_tokens` (the old in-memory decrement was never persisted anyway, so refunded packs looked partly used and could not be removed). A pack whose refunds cover its whole purchased quantity moves to the new status `refunded` (migration `000006`); one emptied by draws plus refunds becomes `consumed`. `RemoveAddon` uses consumed = total − remaining − refunded (refunds are not use) and rejects non-active packs with `invalid_action`. `ListAddons` and the operator listing return `refunded_tokens`, `consumed_tokens` and `removable` (Swagger regenerated). `RefundRepository.SumByAddon` reads the ledger.
- **F-4** ENT-D15 recorded: `TenantEntitlementsChanged{cause: addon_change}` (including drawdowns) is allowed under S-8. LLD §7.1 `cause` enum aligned with AsyncAPI/code; `domain.EffectiveSetCauses` + a drift test over the domain, the schema and the LLD. The `CommercialEntitlementsBillingRefConflict` alert now groups by `reason`.

### Fixed — Billing ↔ Entitlements compatibility (Billing hand-off BL-OQ-28)

- **B-1 Glue-framed events were dead-lettered.** `sqspipeline.NewConsumer` now adds `events.WithConsumerCodec(events.GlueDecodeCodec{})` for all four queues. Outside dev every producer publishes Glue-framed messages; platform-events v2.3.0 fails a `schema_id`-bearing message on a consumer with no codec, so a paid add-on was charged but never granted.
- **B-2 Billing could not call ENT-I1.** `internalServiceRequired` admits Billing by its mTLS principal (last `X-Forwarded-Client-Cert` URI = `BILLING_CALLER_PRINCIPAL`), and `billingReadOnly` keys on that principal (still GET ENT-I1 only). Istio rule changed from the non-existent `*/effective-set` / `*/quota-ceilings` to `GET /api/v1/internal/tenants/*`; the Deployment gets `BILLING_CALLER_PRINCIPAL`. `release.yml` namespace default `default` → `commercial`.
- **B-3 Billing subscription changes raced Core.** `TenantStateChanged` now recomputes for Billing causes (`TenantPlanChanged`, `TenantSubscriptionCancelled`, `TenantReactivated`, `TenantPaymentPastDue`) and whenever plan or status changed. Snapshots now record Core's tenant `record_version` in `source_record_version` (it was always 0), and the upsert refuses a snapshot computed from an older one — otherwise a recompute that read I-17 before Core committed, but wrote last, would overwrite the relay's result.
- **B-4 Consumed schemas were never enforced.** `ValidatingCodec.ValidateConsumed(queue, eventType)` maps each inbound schema per queue; a violation is dead-lettered on first delivery.
- **B-5** `InboundBillingEvent` replaced by `InboundBillingPlanChangedEvent`, `InboundBillingSubscriptionCancelledEvent`, `InboundBillingReactivatedEvent` (Billing's wire shapes); undecodable subscription events are dead-lettered instead of acked. Schemas `inbound_billing_tenant_*.json` added (AsyncAPI 1.6.0, B-6).
- **B-8 Re-claimed `billing_ref`.** `AddonRepository.Insert` uses `ON CONFLICT … DO NOTHING`: the old unique violation aborted the inbox transaction, so the "drop and ack" never committed and the event dead-lettered silently. New metric `commercial_entitlements_billing_ref_conflict_total{event_type,reason}` and critical alert `CommercialEntitlementsBillingRefConflict`.
- **B-7** LLD rev 1.22 aligned with the code (ENT-I1 callers and wire shape, §7.2, §10.2, §13.1).
- **E-4** decision recorded (LLD ENT-D14): purchased add-on balance stays on the trial snapshot after a conversion reversal; no code change.

### Added

- **Enterprise Platform Observability Standard — metrics, logs, traces, DB, events all through platform libs only:**

  - **Two new Tier-3 metrics** (`internal/adapter/outbound/metrics/`):
    - `commercial_entitlements_consumed_event_rejected_total{consumer,event_type,reason}` — incremented in `sqspipeline.Handler` when `ValidatingCodec` rejects an inbound payload before it reaches the domain handler; `reason=schema_violation`; drives `CommercialEntitlementsConsumedEventSchemaViolation` alert.
    - `commercial_entitlements_rls_violations_total{violation_type}` — incremented in `postgres.wrapConnErr` when PostgreSQL returns SQLSTATE 42501 + "row-level security" message; `violation_type=cross_tenant_access`; drives `CommercialEntitlementsRLSCrossTenantAccess` compliance alert.

  - **Metric construction migrated to `gincommon.New*`** — `internal/adapter/outbound/metrics/registry.go` now uses `gincommon.NewCounterVec`, `gincommon.NewHistogramVec`, `gincommon.NewCounter` (never raw `prometheus.New*`). The `"commercial"` domain was ratified in platform-gincommon v2.7.0 and `OBSERVABILITY_DOMAIN` now defaults to `"commercial"` in all binaries (was `"billing"` pending ratification).

  - **`events.InitMetrics` and `pgmetrics.InitWithIdentity`** added to `cmd/worker/main.go` and `cmd/catalog-refresh/main.go`. Both binaries create an outboxPublisher and pool; without initialisation the `platform_outbox_*` and `platform_db_*` counters were silently dropped.

  - **`pgcommon.ConfigFromEnv()` warnings now logged** in `cmd/worker/main.go` and `cmd/catalog-refresh/main.go` (were silently discarded with `_`).

  - **`isRLSViolation` now uses `pgcommon.SQLState(err)`** (`postgres/db.go`) instead of a plain string match for `"42501"`. This is the correct pgcommon API for SQLSTATE inspection without importing pgconn directly.

  - **`ErrNoSchema` sentinel fixed** (`eventbus/validating_codec.go`) — changed from `fmt.Errorf("no schema")` to `errors.New("no schema")`, making `errors.Is` identity checks correct across the package. The dead `err.Error() == "no schema"` string fallback in `isNoSchemaErr` was removed.

- **`check-metrics` target added to `make ci`** — `Makefile` runs `bash scripts/check-metrics.sh` as part of the CI gate, enforcing namespace classification, naming conventions, counter/histogram suffix rules, prohibited high-cardinality labels, and approved label vocabulary (18 metrics validated).

### Changed

- **`OBSERVABILITY_DOMAIN` default: `"billing"` → `"commercial"`** in `cmd/server`, `cmd/reconciler`. `cmd/worker`, `cmd/catalog-refresh`, `cmd/expiry` already hardcoded `"commercial"`. All binaries are now consistent; `deploy/helm/values.yaml` updated accordingly.

- **`RECONCILER_TIMEOUT` default: `5m` → `15m`** (`deploy/helm/values.yaml`) — addon-expiry triggers one IAM call (10 s timeout) per pack; 1 000 packs × 10 s = 167 min worst-case; 15 min is a safe default for typical fleet sizes.

- **AWS SDK v2 patch update** (5 direct deps, 11 transitive):

  | Module | Before | After |
  |---|---|---|
  | `aws-sdk-go-v2/config` | v1.33.21 | v1.33.22 |
  | `aws-sdk-go-v2/credentials` | v1.21.3 | v1.21.4 |
  | `aws-sdk-go-v2/service/glue` | v1.177.0 | v1.177.1 |
  | `aws-sdk-go-v2/service/sns` | v1.48.0 | v1.48.1 |
  | `aws-sdk-go-v2/service/sqs` | v1.53.0 | v1.53.1 |

  `govulncheck ./internal/... ./cmd/...` — **no vulnerabilities found**.

### Fixed

- **IAM upstream compatibility gaps (iam-catalog-admin, iam-org-membership, iam-realm-provisioner):**

  - **iamclient: split base URL + correct URL paths** — `baseURL` split into `catalogBaseURL` (`IAM_CATALOG_BASE_URL`, targets iam-catalog-admin) and `orgBaseURL` (`IAM_ORG_BASE_URL`, targets iam-org-membership). URL prefix corrected from `/internal/v1/` to `/api/v1/internal/` for both `FetchPlan` and `FetchTenantSubscription`. `FetchTenantSubscription` path corrected to `.../plan-context` (was `.../subscription`). Both upstreams use Istio mTLS — dead `x-api-key` header and `IAM_CATALOG_API_KEY` env var removed.

  - **iamclient: `FetchAllPlans` added** — calls `GET {IAM_CATALOG_BASE_URL}/api/v1/internal/plans` (CAT-I2 bulk); added to `port.IAMClient`. `cmd/catalog-refresh` now bootstraps an empty `plan_catalog_cache` via `FetchAllPlans` on first deploy rather than failing silently with zero plan codes.

  - **`domain.Plan` JSON tags** — all `Plan` fields now carry explicit `json:"..."` tags matching iam-catalog-admin's `PlanResponse` snake_case shape (`"llm_token_quota_monthly"`, `"api_request_budget_monthly"`, etc.). Without these tags Go's JSON decoder decoded snake_case keys silently to nil, breaking quota enforcement for every tenant.

  - **`port.TenantSubscription` wire alignment** — JSON tags added matching I-17 shape (`PlanCode→"plan"`, `State→"status"`); `FeatureFlags []string` added (`json:"feature_flags,omitempty"`) for per-tenant O-4 operator delta; dead `TrialProfile` field removed (commercial-entitlements is the system of record for trial profiles via `tenant_trial_assignments`).

  - **`mapSubscriptionState` extended** — `past_due`/`cancelled` → `grace_period`; `trial_expired` → `suspended`. Previously these were unhandled and fell through to the default `suspended`, losing the grace-period distinction.

  - **`mergeFeatureFlags` in `effective_set.go`** — plan-baseline feature flags are now unioned with the per-tenant O-4 delta from I-17 `feature_flags[]`. Result is deduped and sorted for stable `effectiveSetUnchanged` comparison (ENT-INV-6). Previously only plan flags were used, ignoring operator-set per-tenant flags entirely.

  - **`TenantSuspended`, `TenantReactivated`, `TrialExpired` added to `tenant_consumer.go`** — these events arrive from iam-realm-provisioner on `tenant-entitlements-q` (CD-6, CD-49) but were previously silently ACK'd without calling Recompute, so entitlement snapshots were never updated on suspension, reactivation, or trial expiry.

  - **`dedup.go` `knownEventTypes` corrected** — `TenantProvisioned` (never published) replaced with `TenantIdentityProvisioned`; `TrialExpired`, `TrialReactivated`, `TenantConverted`, `TenantMembershipsPurged`, `TenantSubscriptionUpdated` added; comments updated to group by queue.

  - **Inbound event wire shapes fixed** — `InboundTenantEvent` trimmed to `{TenantID}` only (RP publishes no extra fields); `InboundMembershipEvent` gains `actor_id` matching `TenantMembershipsPurgedPayload`; `InboundCatalogEvent` fields corrected (`plan_code`→`code`, stale `record_version`/`occurred_at` removed, `change_type` and `feature_set` added).

  - **cmd/server startup validation** — `IAM_ORG_BASE_URL` added to required env-var check; server aborts at startup if unset.

  - **LLD** updated to rev 1.21: §3.2 iamclient paragraph, §3.3.3 event list, §7.2 consumed events (added `TenantIdentityProvisioned` and `TenantSubscriptionUpdated` rows), §12 config (`IAM_CORE_BASE_URL` → `IAM_ORG_BASE_URL`, `IAM_CATALOG_API_KEY` removed, `IAM_CATALOG_TIMEOUT` added), §18.1 and §18.2 integration details. AsyncAPI bumped to v1.5.0: `tenantEntitlementsQueue` channel and `consumeTenantLifecycleEvents` operation added.

- **E-3 read-race fix: conversion-revert recompute** — `membership-entitlements-q`
  consumer now subscribes to `TenantStateChanged{cause:TrialReactivated}` from
  iam-org-membership instead of relying on Realm Provisioner's `TrialReactivated`
  event. Because iam-org-membership publishes via transactional outbox *after* its
  own DB commit, an I-17 call on receipt always returns the post-revert `trial` state.
  Dead `TenantConverted` and `TenantOffboarded` case handlers removed from
  `membership_consumer.go` — those events arrive on `tenant-entitlements-q` from
  Realm Provisioner, not from iam-org-membership. `dedup.go` `knownEventTypes`
  updated accordingly.

- **Stale env var names in LLD §14 config table** — `SNS_TOPIC_ARN` →
  `SNS_TOPIC_ENTITLEMENTS_ARN`; `SQS_BILLING_QUEUE_URL` →
  `SQS_BILLING_ENTITLEMENTS_QUEUE_URL`; `SQS_TENANT_QUEUE_URL` →
  `SQS_TENANT_ENTITLEMENTS_QUEUE_URL`; `SQS_CATALOG_QUEUE_URL` →
  `SQS_CATALOG_ENTITLEMENTS_QUEUE_URL`; `SQS_MEMBERSHIP_QUEUE_URL` →
  `SQS_MEMBERSHIP_ENTITLEMENTS_QUEUE_URL`. Names now match `.env-example` and
  `cmd/server/main.go`.

- **ENT-D7 decision note** — corrected incorrect file reference from
  `membership_consumer.go → handleConverted` to `tenant_consumer.go → handleOffboard`.

- **Consumer event names (BL-OQ-14 / billing gap)** — Billing consumer was
  listening for `BillingSubscriptionActivated`, `BillingSubscriptionUpdated`,
  `BillingSubscriptionSuspended`, `BillingSubscriptionReactivated`,
  `BillingTrialStarted`, `BillingTrialExpired` — none of which Billing
  publishes. Corrected to `TenantPlanChanged`, `TenantSubscriptionCancelled`,
  `TenantReactivated`. Trial cause now derived from `payload.TrialProfile`
  instead of the event-type name.

- **E-2 / BL-OQ-20 — Refund lookup used wrong key** — `ApplyBillingRefund`
  was calling `GetByID(purchase_ref)` (the addon's internal UUID), which never
  matches. Changed to `GetByBillingRef(tenantID, purchaseRef.String())` so the
  lookup correctly finds the grant by Billing's purchase ID stored in
  `billing_ref`.

- **Review-1 — Malformed purchase/refund events silently acked** —
  `handleAddonChanged`, `handleAddonPurchase`, and `handleAddonRefund` were
  returning `nil` on unmarshal failure, causing silent message loss. Now return
  `fmt.Errorf(...)` so the consumer NACKs and the message goes to DLQ after
  max retries.

- **Review-2 — Failed recompute after grant swallowed** — `ApplyBillingPurchase`
  and `ApplyBillingRefund` were logging a warning and returning `nil` on
  recompute failure. Now propagate the error so the consumer NACKs and retries.
  Grant insert is idempotent via `billing_ref`, so retries are safe.

- **Review-3 — Add-on expiry used 365 days instead of calendar 12 months** —
  `ApplyBillingPurchase` used `time.Hour * 24 * 365`. Changed to
  `AddDate(1, 0, 0)` for correct calendar-month expiry.

- **Review-4 — Cross-tenant `billing_ref` triggered retry loop** —
  `AddonRepository.Insert` now detects postgres unique violation
  (`23505`) on `uq_quota_addons_billing_ref` and returns
  `ErrBillingRefAlreadyClaimed`. `ApplyBillingPurchase` handles this with an
  `Error`-level log and silently acks (retrying is pointless; the unique index
  would keep firing).

### Added

- `InboundTenantStateChangedEvent` domain type (`domain/event.go`) — payload for
  iam-org-membership's `TenantStateChanged` event (fields: `tenant_id`, `status`,
  `previous_status`, `plan`, `previous_plan`, `changed_at`, `cause`).

- `ErrBillingRefAlreadyClaimed` sentinel error in `domain/errors.go`.
- Migration `000002_addon_billing_ref` — adds `billing_ref TEXT` column and
  `uq_quota_addons_billing_ref` partial unique index to `quota_addons`
  (E-1 / BL-OQ-4).

### Changed

- LLD updated to rev 1.20: `TenantStateChanged` consumer row added (§7.2),
  EVT-10 invariant added (§7.7), package layout updated, ENV var names corrected
  (§14), ENT-D7 file reference corrected, AsyncAPI bumped to v1.4.0.

- LLD `commercial-lld-entitlements-service.md` updated to rev 1.19: ENUM
  rename, `quota_addons` DDL alignment, migration 000002 documented,
  `addon_eligibility` response field, 5 reconciler CronJobs, `AuthorizationPolicy`
  Helm resource, `billing_ref_already_claimed` error taxonomy entry, EVT-8
  Temporal fan-out correction.
