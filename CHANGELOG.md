# Changelog

All notable changes to this service are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning aligned with SemVer.

This service has never been deployed to any environment — there is no released version yet, so everything lives under `[Unreleased]`. Because of that, this file tracks only what is currently true and what is still outstanding, not a phase-by-phase build history; the full development diary is available via `git log` if it's ever needed.

## [Unreleased]

### Fixed

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
