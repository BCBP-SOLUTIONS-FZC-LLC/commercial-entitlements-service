# Workflow Team — Gap Closure Request
## Entitlements Service (`commercial-entitlements`) · LLD v1.15

| Field | Value |
|---|---|
| Document type | Action Request |
| From | Commercial Team |
| To | Workflow Team / Platform Team |
| Date | 2026-11-18 |
| Entitlements LLD | `commercial-lld-entitlements-service.md` v1.15 |
| Priority | High — blocks Phase 1 deployment |

---

## Overview

The Entitlements Service (`commercial-entitlements`) uses the **platform's existing Temporal cluster** (the same cluster the Workflow services run on) for two fan-out workflows. Before the service can be deployed and the LLD can be finalized, the Workflow team needs to:

1. **[ENT-D10]** Provision the Temporal namespace and hand over credentials **(infrastructure)**
2. **[§16.3]** Sign off on the LLD **(document review)**

Both are listed as open/pending in LLD §16.

---

## Gap 1 — ENT-D10: Temporal Namespace Provisioning

### What we need

The `commercial-entitlements` service runs a dedicated Temporal **worker** (`commercial-entitlements-worker`) on the platform cluster. For this to work, the Platform/Workflow team must provision the following:

| Item | Value needed |
|---|---|
| Namespace | `commercial-entitlements` |
| Namespace retention | Per platform standard (to be confirmed by Platform team) |
| `TEMPORAL_HOSTPORT` | Address of the platform Temporal frontend (required env var) |
| Client identity | `commercial` / `commercial-entitlements` (mTLS or credentials) |
| Egress | Allow `commercial` namespace pods to reach the Temporal frontend (NetworkPolicy) |

### What we (Commercial team) will do

Once you provide the above, we will:

- Set `TEMPORAL_HOSTPORT` and `TEMPORAL_NAMESPACE=commercial-entitlements` in the worker Deployment
- Configure mTLS / credentials in the worker's Helm chart
- Add Temporal frontend egress to the `commercial` NetworkPolicy
- Run integration tests against the provisioned namespace before Phase 1 deploy

### Two workflows that will run on this namespace

**Workflow 1 — `CatalogFanoutWorkflow`**

| Property | Value |
|---|---|
| Task queue | `ent-catalog-fanout` |
| Trigger | `PlanCatalogChanged` SNS event (via `catalog-entitlements-q` SQS) |
| What it does | Recomputes entitlements for all tenants on a changed plan, in parallel |
| Max parallel activities | `FANOUT_MAX_PARALLEL` (default: 20) |
| Workflow ID pattern | `ent-catalog-fanout:{plan_code}:{trigger_id}` — duplicate ID = no-op |
| History bounding | `continue-as-new` after every `FANOUT_PAGES_PER_RUN` pages |
| Activity timeout | 10 s, up to 5 retries (backoff: 1 s initial, 2× multiplier, 30 s max) |

**Workflow 2 — `OverrideExpiryFanoutWorkflow`**

| Property | Value |
|---|---|
| Task queue | `ent-expiry-fanout` |
| Trigger | Hourly `cmd/expiry` CronJob |
| What it does | Expires per-tenant entitlement overrides that are past their `expires_at`, recomputes entitlements |
| Max parallel activities | Same `FANOUT_MAX_PARALLEL` (default: 20) |
| Workflow ID pattern | `ent-expiry-fanout:{window_end}` — duplicate ID = no-op |
| History bounding | `continue-as-new` after every `FANOUT_PAGES_PER_RUN` pages |
| Activity timeout | Same retry policy as above |

Both workflows are served by the **same worker Deployment** (`commercial-entitlements-worker`, `cmd/worker`, replicas: 2).

### What happens if Temporal is unavailable

| Scenario | Behaviour |
|---|---|
| Temporal down when `PlanCatalogChanged` arrives | Consumer does not ack → SQS redelivers. After 5 failures → DLQ + P2 alert. The catalog-refresh CronJob (every 10 min) will restart the workflow once Temporal is back |
| Worker pods down | Workflow waits in Temporal. Resumes when workers restart. No data lost |
| Temporal down when hourly expiry CronJob runs | Kubernetes retries the Job. If all retries fail, the next hourly run picks up all overrides still due |

### Checklist for Workflow/Platform team

- [ ] Provision namespace `commercial-entitlements` on the platform Temporal cluster
- [ ] Configure namespace retention (confirm value with Commercial team)
- [ ] Share `TEMPORAL_HOSTPORT` (platform Temporal frontend address)
- [ ] Issue mTLS certificate or credentials for identity `commercial` / `commercial-entitlements`
- [ ] Confirm NetworkPolicy: allow egress from `commercial` namespace to Temporal frontend
- [ ] Confirm: Commercial team can use `tctl --namespace commercial-entitlements` for runbook operations (§20.5, §20.3 of LLD)

---

## Gap 2 — §16.3: LLD Sign-off (Workflow Team)

The Workflow team is listed as a required sign-off on the Entitlements LLD (`commercial-lld-entitlements-service.md` v1.15, §16.3). The sign-off is pending.

### What you need to review

The LLD has two areas that directly affect the Workflow team:

#### A. ENT-I1 — Entitlements read endpoint (your services call this)

Both Workflow services call `GET /api/v1/internal/tenants/:id/entitlements` (ENT-I1) to read the effective entitlement set for a tenant.

| Item | Value |
|---|---|
| Endpoint | `GET /api/v1/internal/tenants/:id/entitlements` |
| Caller namespaces admitted | `workflow-app` |
| Admitted ServiceAccounts | `workflow-definition-service`, `workflow-execution-service-api` |
| Auth | mTLS (Istio mesh) + `x-user-id` and `x-tenant-id` headers |
| Cache TTL | 60 s (Valkey) — response may be up to 60 s stale |
| Fail-closed rule | On 503 from Entitlements, callers must surface an error — **never default to unlimited or Starter limits** (CD-30) |

This was agreed in CD-29 (confirmed 2026-11-02). Please verify the ServiceAccount names and namespace are still correct before signing off.

#### B. `dedicated_workflow_queue` feature flag

The `feature_flags` field in the ENT-I1 response includes `dedicated_workflow_queue` for Enterprise tenants. The Workflow Execution service reads this flag at W-4 to decide whether to register a per-tenant task queue.

Please confirm that the flag name `dedicated_workflow_queue` and the behaviour described above still match your implementation.

#### C. `TenantEntitlementsChanged` event subscription

The Workflow team may subscribe to `TenantEntitlementsChanged` on the `entitlements-workflow-q` queue (if planned). Please confirm whether the Workflow team needs this subscriber queue provisioned, or if polling ENT-I1 on-demand is sufficient.

### Sign-off checklist for Workflow team

- [ ] Confirm `workflow-app` namespace and both ServiceAccount names are correct (CD-29)
- [ ] Confirm `dedicated_workflow_queue` flag name and behaviour matches Workflow Execution (W-4)
- [ ] Confirm whether Workflow team needs `TenantEntitlementsChanged` subscriber queue or not
- [ ] Confirm fail-closed behaviour: Workflow services return an error (not default limits) when ENT-I1 returns 503
- [ ] Sign off on LLD v1.15 (§16.3 — add name and date to the sign-off register)

---

## Already Resolved — No action needed

These items were previously open but are now closed. Listed here for reference only.

| Decision | What was decided | Closed |
|---|---|---|
| CD-29 | Workflow namespace = `workflow-app`; `workflow-definition-service` and `workflow-execution-service-api` ServiceAccounts admitted to ENT-I1 | 2026-11-02 |
| ENT-D1 | `PlanCatalogChanged` fan-out uses Temporal workflow (not a CronJob batch loop) | 2026-11-17 |
| ENT-D4 | Override expiry fan-out also uses Temporal workflow (same model as ENT-D1) | 2026-11-17 |

---

## Summary of actions required

| # | Action | Owner | Blocking |
|---|---|---|---|
| 1 | Provision `commercial-entitlements` namespace on platform Temporal cluster | Platform + Workflow team | Yes — service cannot deploy without this |
| 2 | Provide `TEMPORAL_HOSTPORT` and mTLS / credentials | Platform + Workflow team | Yes — same as above |
| 3 | Confirm NetworkPolicy egress for `commercial` namespace to Temporal frontend | Platform + Workflow team | Yes — same as above |
| 4 | Confirm ServiceAccount names and namespace (CD-29 still valid) | Workflow team | LLD sign-off |
| 5 | Confirm `dedicated_workflow_queue` flag name and behaviour | Workflow team | LLD sign-off |
| 6 | Confirm `TenantEntitlementsChanged` subscriber queue need | Workflow team | LLD sign-off |
| 7 | Sign off on LLD v1.15 §16.3 | Workflow team | LLD finalisation |

---

## Contact

For questions on this document, reach the Commercial Team via the usual channel or raise against the Entitlements LLD (`commercial-lld-entitlements-service.md`).
