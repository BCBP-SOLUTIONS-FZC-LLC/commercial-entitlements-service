# Changelog

All notable changes to `commercial-entitlements` are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

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

- `ErrBillingRefAlreadyClaimed` sentinel error in `domain/errors.go`.
- Migration `000002_addon_billing_ref` — adds `billing_ref TEXT` column and
  `uq_quota_addons_billing_ref` partial unique index to `quota_addons`
  (E-1 / BL-OQ-4).

### Changed

- LLD `commercial-lld-entitlements-service.md` updated to rev 1.19: ENUM
  rename, `quota_addons` DDL alignment, migration 000002 documented,
  `addon_eligibility` response field, 5 reconciler CronJobs, `AuthorizationPolicy`
  Helm resource, `billing_ref_already_claimed` error taxonomy entry, EVT-8
  Temporal fan-out correction.
