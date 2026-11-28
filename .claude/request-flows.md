# Request Flows, Concurrency & Idempotency

## Effective Set Recompute (Core Flow)

Called by billing/tenant/membership event consumers and by the HTTP `POST /entitlements/recompute` endpoint.

```
caller → EffectiveSetService.Recompute(ctx, tenantID, cause)
  1. IAMClient.FetchTenantSubscription(ctx, tenantID)
       → GET {IAM_CATALOG_BASE_URL}/internal/v1/tenants/{id}/subscription
       → {plan_code, state: "active"|"trial"|...}
  2. CatalogService.GetPlan(ctx, planCode)
       → Valkey GET ent:plan:{planCode}  → hit: use cached
       → miss → CatalogCacheRepository.GetPlan → hit: use cached, warm Valkey
       → miss → IAMClient.FetchPlan → store in DB + Valkey
  3. Build limits map from plan.BaseLimits()
  4. if state == trial:
       TrialRepository.GetAssignment(ctx, tenantID)
         → profile = assignment's, or trial_specific when no row (E-6)
         → base = trial_specific for trial_specific/custom, reduced_plan for
           reduced_plan, none for full_plan (plan row used as-is)
         → apply plan.TrialProfiles[base] (NULL column = inherit plan) (step 2)
       TrialRepository.ListOverrides(ctx, tenantID)
         → apply per-key trial overrides (step 3 override)
  5. OverrideRepository.ListByTenant(ctx, tenantID)
       → skip expired (expires_at <= now)
       → apply per-key (step 4 override)
  6. for each periodic LimitKey [llm_token_quota_monthly, api_request_budget_monthly]:
       AddonRepository.SumRemainingByTenantAndKey(ctx, tenantID, key)
         → additive: limits[key].Value += addonBalance  (step 5 addons)
         → skip if plan is unlimited for that key
  7. ENT-INV-6 no-op guard:
       SnapshotRepository.GetLatest(ctx, tenantID)
       if effectiveSetUnchanged(computed, existing):
         IncRecomputeNoop(cause) → return existing snapshot (no write, no event)
  8. nextVersion = existingSnap.Version + 1  (or 1 if none)
  9. TxRunner.RunInTx:
       SnapshotRepository.Upsert (ON CONFLICT DO UPDATE WHERE version < EXCLUDED.version)
       EventPublisher.Enqueue(TenantEntitlementsChanged{...})
       AuditRecorder.Record(...)  [if wired]
       COMMIT
 10. Cache.SetEffectiveSet(ctx, tenantID, es)  [advisory, best-effort]
 11. writeQuotaCeiling: Cache.SetQuotaCeiling(ctx, tenantID, llm_token_quota_monthly, value)  [ENT-D13]
     return es
```

**Key invariants in this flow:**
- IAMClient call is **outside** the `RunInTx` (no outbound HTTP inside a transaction).
- ENT-INV-6: if computed matches stored, writes and events are skipped — Recompute is idempotent.
- ENT-INV-1: `Upsert` SQL has `WHERE entitlement_snapshots.version < EXCLUDED.version` — no stale overwrite.
- EVT-10: `Enqueue` is called inside `RunInTx` — event and snapshot commit atomically.
- CACHE-1: cache errors are fire-and-forget; fallthrough to Postgres on next read.

## GetEffectiveSet (Read Path)

```
caller → EffectiveSetService.GetEffectiveSet(ctx, tenantID)
  1. Cache.GetEffectiveSet(ctx, tenantID)  → hit: return (ent:snapshot:{id})
  2. miss → SnapshotRepository.GetLatest(ctx, tenantID)
  3. Cache.SetEffectiveSet(ctx, tenantID, es)  [warm cache]
  4. return es
```

If no snapshot exists: `domain.ErrSnapshotNotFound` → caller may trigger a `Recompute`.

## Quota Addon Drawdown (ENT-INV-3 Idempotency)

Called by the quota enforcer per-request to deduct from the addon pool.

```
caller → AddonService.RecordDrawdown(ctx, {tenantID, limitKey, tokensConsumed, requestID, periodStart})
  1. DrawdownRepository.GetByRequestID(ctx, tenantID, requestID)
       → existing: return existing drawdown (idempotent, no double-deduct)
       → nil: proceed
  2. AddonRepository.ListActiveByTenantAndKey(ctx, tenantID, limitKey)
       → sorted: expires_at ASC NULLS LAST, created_at ASC  (FIFO)
  3. iterate addons FIFO:
       portion = min(remaining_tokens, need)
       addon.RemainingTokens -= portion; addon.RecordVersion checked (CONC-2)
       if remaining_tokens == 0: addon.Status = consumed
  4. if total_drawn < tokensConsumed: raise shortfall
       ShortfallRepository.Insert({tenantID, limitKey, requested, available, deficit})
         status = auto_accepted  (if deficit ≤ auto-approval threshold)
               OR pending_approval  (if large deficit requiring operator)
  5. TxRunner.RunInTx:
       for each modified addon: AddonRepository.Update (WHERE id=$1 AND record_version=$2 → CONC-2)
       DrawdownRepository.Insert (unique on (tenant_id, request_id))
       ShortfallRepository.Insert [if shortfall]
       COMMIT
  6. EffectiveSetService.Recompute(ctx, tenantID, AddonChange)  [update quota ceiling]
  return drawdown
```

**ENT-INV-3:** `GetByRequestID` before `Insert` ensures the same `request_id` is never double-counted. Duplicate returns the previously created drawdown unchanged.

## Billing Event → Plan Change

```
SQS billing-entitlements-q → BillingEventConsumer.Handle
  → processOnce(inbox, "billing-entitlements-q", eventID, fn)
      fn:
        1. decode TenantPlanChanged / TenantSubscriptionCancelled / TenantReactivated into its own struct (B-5); only tenant_id is used — undecodable or missing tenant_id → error → retry → DLQ
        2. EffectiveSetService.Recompute(ctx, tenantID, BillingEvent)
           [this re-fetches subscription from IAM to get new plan]
      COMMIT processed_events + snapshot + TenantEntitlementsChanged event
```

## Tenant Lifecycle Event → State Change

```
SQS tenant-entitlements-q → TenantEventConsumer.Handle
  → processOnce → fn:
      TenantIdentityProvisioned/TenantSubscriptionUpdated/TenantSuspended/TenantReactivated/TrialExpired
        → EffectiveSetService.Recompute(ctx, tenantID, TenantLifecycle)
      TenantMembershipsPurged/TenantConverted/TenantOffboarded → handleOffboard:
        if TenantMembershipsPurged: AddonService.ForfeitAddonsAtPurge (in inbox tx; error → redelivery, E-9)
        TrialService.PurgeTrialForTenant(ctx, tenantID)  (error logged, not returned)
          RunInTx: DELETE tenant_trial_assignments, DELETE trial_entitlement_overrides
        EffectiveSetService.Recompute(ctx, tenantID, TenantLifecycle)
      COMMIT
```

## Membership Event → Trial Purge

```
SQS membership-entitlements-q → MembershipConsumer.Handle
  TenantMembershipsPurged → AddonService.ForfeitAddonsAtPurge (in inbox tx; active packs → forfeited,
                            AL-5 billing.token_addon.revoked; error → redelivery) → Recompute(TenantLifecycle)
  TenantStateChanged → (published by O&M via transactional outbox after DB commit — I-17 read is always safe)
    cause TrialReactivated                                  → Recompute(TenantLifecycle)
    cause TenantPlanChanged|TenantSubscriptionCancelled|
          TenantReactivated|TenantPaymentPastDue (Billing)  → Recompute(BillingEvent)   [B-3]
    plan or status changed (any other cause)                → Recompute(TenantLifecycle)
    otherwise                                               → ack
  Why B-3: Billing publishes its subscription events to both Core (billing-orgm-q)
  and billing-entitlements-q, unordered. If Entitlements reads I-17 before Core
  applies the change it keeps the old plan; Core's relay is the settling recompute.
  unknown → ackUnknown (metrics, no DLQ)
Note: TenantConverted and TenantOffboarded come from Realm Provisioner on
  iam-tenant-events → tenant-entitlements-q (TenantEventConsumer), not this queue.
```

## Catalog Change → Plan Cache Invalidation + Fan-out

```
SQS catalog-entitlements-q → CatalogConsumer.Handle
  PlanCatalogChanged{planCode, eventID}:
    1. CatalogCacheRepository.InvalidatePlan(ctx, planCode)
       Valkey DEL ent:plan:{planCode}
    2. if TemporalStarter wired:
         TemporalStarter.StartCatalogFanout(ctx, planCode)
           → Temporal workflow CatalogFanoutWorkflow
               → one RecomputeActivity per tenant on planCode
  unknown → ackUnknown
```

## Override Set

```
caller → PUT /overrides/:limit_key
  → OverrideService.SetOverride(ctx, {tenantID, limitKey, value, expiresAt, setBy, reason})
      1. validateLimitKey(limitKey)
      2. TxRunner.RunInTx:
           OverrideRepository.Upsert (ON CONFLICT (tenant_id, limit_key) WHERE deleted_at IS NULL DO UPDATE)
           OverrideHistory INSERT {action: set, old/new value}
           EventPublisher.Enqueue(TenantEntitlementsChanged) — trigger downstream recompute
           COMMIT
      3. EffectiveSetService.Recompute(ctx, tenantID, OverrideChange)
         [this is the read-path trigger; also warms quota ceiling]
      4. IncOverrideSet(limitKey)
```

## Trial Assignment Set (ENT-O3)

```
caller → PUT /api/v1/operator/tenants/:id/entitlements/trial-profile
  → TrialService.AssignTrialProfile(ctx, {tenantID, planCode, profile, startsAt, endsAt, setBy, reason})
      1. validate: profile ∈ ValidTrialProfiles (invalid_profile), ends_at in future
         (trial_ends_at_in_past), reason non-empty (reason_required). No state guard:
         a non-trial assignment is stored but only applies during a trial (ENT-INV-5).
      2. existing := TrialRepository.GetAssignment → version = existing+1 or 1
         (a read error other than not-found fails the request)
      3. TxRunner.RunInTx:
           TrialRepository.UpsertAssignment({...})
           recordAudit(AL-5 config.trial_profile.set; reason, previous_profile on change)
           COMMIT
      4. EffectiveSetService.Recompute(ctx, tenantID, TrialChange)  [advisory; failure logged]
```

## Consumer Idempotency Pattern (IDEMP-1..4)

All 4 SQS consumers use `processOnce` from `consumer/dedup.go`:

```go
func processOnce(ctx context.Context, inbox port.Inbox, consumer string, env events.Envelope[...], fn func(txCtx) error) error {
    return inbox.ProcessOnce(ctx, consumer, env.ID, func(txCtx context.Context) error {
        return fn(txCtx)  // fn's DB writes share the same pgx.Tx
    })
}
```

`inbox.ProcessOnce` (platform-events `inbox.Store.Process`):
1. `INSERT INTO processed_events (event_id, consumer) ON CONFLICT DO NOTHING` as first statement.
2. If 0 rows inserted: duplicate. Skip. Ack.
3. If 1 row: proceed with `fn` on the same transaction. `fn`'s writes (snapshot Upsert, event Enqueue) commit atomically with the processed_events claim.

**IDEMP-4:** `processed_events` retention is 8 days (`PROCESSED_EVENTS_TTL_DAYS`), which is strictly > 7-day SQS maximum retention, so a consumer cannot see a message after its `processed_events` row has been pruned.

## Shortfall Lifecycle

```
shortfall raised by RecordDrawdown → status = auto_accepted or pending_approval
  if pending_approval:
    operator POSTS /shortfalls/:id/resolve {disposition: approve|reject}
      → ShortfallService.ResolveShortfall(ctx, {shortfallID, disposition, actor})
          TxRunner.RunInTx:
            ShortfallRepository.UpdateStatus (record_version checked, CONC-2)
            EventPublisher.Enqueue(TenantEntitlementsChanged) [if approved]
            COMMIT
          if approved: EffectiveSetService.Recompute(ctx, tenantID, ShortfallResolution)
  status terminal: once approved/rejected, record is immutable
```

## Concurrency: Optimistic Locking

**All repositories using `record_version`:**

```go
// Generic pattern
tag, err := tx.Exec(ctx, `UPDATE <table> SET ... WHERE id=$1 AND record_version=$2`, id, currentVersion)
if tag.RowsAffected() == 0 {
    return domain.ErrOptimisticLockConflict
}
// touch_row() trigger auto-increments record_version and updated_at on the DB side
```

The 409 response includes the current `record_version` from `GetByID` for the client to retry with.

**Snapshot Upsert is different:** uses `ON CONFLICT DO UPDATE WHERE version < EXCLUDED.version` to silently skip a stale recompute — no error returned, just 0 rows affected (ENT-INV-1).

## EffectiveSet No-op Guard (ENT-INV-6)

```go
func effectiveSetUnchanged(es *EffectiveSet, snap *EntitlementSnapshot) bool {
    if es.PlanCode != snap.Plan || es.State != snap.State || es.ModelTier != snap.ModelTier {
        return false
    }
    for k, v := range es.Limits {
        sv, ok := snap.Limits[k]
        if !ok || v.IsUnlimited() != sv.IsUnlimited() { return false }
        if !v.IsUnlimited() && *v.Value != *sv.Value { return false }
    }
    return true
}
```

If `effectiveSetUnchanged` returns true, `Recompute` skips the write AND the `TenantEntitlementsChanged` event emission. `IncRecomputeNoop(cause)` is called. No DB write, no network.
