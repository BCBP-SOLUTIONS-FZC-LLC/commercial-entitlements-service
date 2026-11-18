# Token Add-on Shortfall — Problem Analysis & Recommendations

**Service:** Entitlements Service (`commercial-entitlements`)
**Related:** CD-55, ENT-D9, ENT-D11, ENT-D12
**Date:** 2026-11-18

---

## 1. What is a Shortfall?

A shortfall happens when Usage & Metering reports that a tenant consumed **more add-on tokens than were actually available** at period close.

```
Example:
  Add-on balance available:  1,000,000 tokens
  U&M reported consumed:     1,500,000 tokens
  Shortfall:                   500,000 tokens  ← gap we need to handle
```

The tenant's balance never goes negative. But those 500K tokens were served with no balance backing them — that is a real cost to the platform.

---

## 2. Why Does It Happen?

The Gateway enforces one combined limit (base + add-on). But enforcement and metering are **two separate systems** that can disagree.

### Cause 1 — Concurrent Requests (Race Condition)

```
Add-on balance: 100 tokens

User A checks → 100 remaining → ALLOWED ─┐
User B checks → 100 remaining → ALLOWED ─┤  same millisecond
                                          │
User A uses 60 tokens                     │
User B uses 60 tokens                     │
                                          ▼
Total used: 120 tokens — Shortfall: 20 tokens
```

Both users checked before either consumed. Neither was rejected. This is called a **TOCTOU problem** (Time of Check, Time of Use).

### Cause 2 — Gateway Fail-Open (CD-57)

```
Rate-limit service goes DOWN
           ↓
Gateway has two choices:
  Option A: Fail closed → all tenants blocked → platform OUTAGE
  Option B: Fail open  → allow all traffic   → possible shortfall

XpertPMS chose Option B (availability over strict enforcement)
```

During the outage window, tenants consume without quota enforcement.

### Cause 3 — Rollover Timing Gap

```
11:59 PM Oct 31 → U&M computes: "1.5M add-on tokens used this month"
12:00 AM Nov 1  → Pack expires, balance removed
12:00 AM Nov 1  → ENT-I3 called: only 1M available now
                                   Shortfall: 500K
```

Requests in-flight at the exact month boundary can land in the wrong period's count.

### Cause 4 — Late Usage Records

Some usage records arrive after the period closes. U&M reports them in the next ENT-I3 call but they belong to the previous period's quota.

---

## 3. What XpertPMS Does Today (Current Approach)

The current approach is **reactive** — shortfalls are caught after they happen, then formally resolved.

```
Month closes
     ↓
U&M calls ENT-I3 with tokens_drawn
     ↓
ENT-I3 detects shortfall (tokens_drawn > available balance)
     ↓
Drain all packs to zero
     ↓
Insert quota_addon_shortfalls row (status = open)
     ↓
Check tolerance:
  ├── Within tolerance + not escalated → auto-accept as accepted_loss
  └── Above tolerance OR escalated     → stays open, operator reviews
```

### Tolerance Thresholds (ENT-D12)

| Config | Value | Meaning |
|---|---|---|
| `SHORTFALL_TOLERANCE_TOKENS` | 10,000 | Auto-accept if shortfall ≤ 10K tokens |
| `SHORTFALL_TOLERANCE_PCT` | 1% | AND shortfall ≤ 1% of tokens_drawn |
| `SHORTFALL_HIGH_VALUE_TOKENS` | 250,000 | Escalate immediately if shortfall ≥ 250K |
| `SHORTFALL_REPEAT_COUNT` | 2 | Escalate if tenant has 2+ shortfalls |
| `SHORTFALL_REPEAT_WINDOW_MONTHS` | 3 | …within 3 months |

### Resolution Options

| Resolution | Who decides | What it means |
|---|---|---|
| `accepted_loss` | System (auto) or operator | Platform absorbs the cost |
| `entitlement_correction` | Operator | Our balance data was wrong — we fix it |
| `metering_correction` | Operator | U&M over-reported — they fix it |
| `billing_escalation` | Commercial team | Tenant genuinely over-consumed — charge them |

### What is Good About This

- Shortfalls are never silently discarded (ENT-D9)
- Small shortfalls auto-resolved without manual work
- Large/repeated shortfalls escalated immediately (P2 alert)
- Full audit trail for every disposition
- Financial record retained 7 years

### What is Missing

- Does **not prevent** shortfalls from happening
- Prevention requires cross-team changes — AI Team owns the Rate Limit Service and Envoy config (CD-57)

---

## 4. How Other Companies Handle This

### Anthropic (Claude) and OpenAI (GPT)

Both use **atomic counters** at the enforcement layer:

```
Request arrives at Gateway
         ↓
Redis ATOMIC INCR tokens_used by request_size
         ↓
Result > quota limit?
  YES → reject (429) before inference starts
  NO  → allow
```

Redis `INCR` is atomic — only one operation at a time even under concurrent load. User A and User B cannot both pass the check for the same tokens. Race condition eliminated.

They also use **fail closed** — if the rate-limit service is down, requests are rejected. They choose strict enforcement over availability.

### AWS

Uses **token bucket** algorithm, enforced **in-process** at the Gateway:

```
No external service call needed
Gateway holds quota state locally
Atomic decrement before processing
Bucket refills at a fixed rate
```

No network hop = no fail-open risk. Concurrent requests handled atomically.

### Stripe / Twilio (B2B SaaS model)

Both use **contracted overage pricing** as a commercial layer on top of atomic enforcement. Noted as a future commercial option for XpertPMS if the `billing_escalation` path needs a pre-agreed rate — not a technical gap, a business decision.

---

## 5. Recommended Improvement for XpertPMS

Only one genuine technical gap exists — **prevention**. Price and Absorb are already handled by the LLD.

| What | Already in LLD? | Gap? |
|---|---|---|
| Absorb small shortfalls (`accepted_loss`, tolerance thresholds) | ✅ Yes — ENT-D11, ENT-D12 | None |
| Charge tenant (`billing_escalation`, operator review) | ✅ Yes — ENT-D11, ENT-O7 | None |
| Prevent shortfalls from happening | ❌ No | **This is the gap** |

### The Only Gap — Prevention (Two sides)

Prevention needs work on **both** sides — Entitlements Service AND AI Team.

---

#### Side 1 — Entitlements Service (our responsibility)

Entitlements already writes the computed effective quota to Valkey as a snapshot cache. The gap is writing it in a **dedicated key format** that the Rate Limit Service can read directly for enforcement.

**What needs to be implemented here:**

```
On every recompute (TenantEntitlementsChanged):
  Write to Valkey:
    Key:   quota:tenant:{tenant_id}:llm_token_quota_monthly
    Value: effective quota (base + addon balance)
    TTL:   short (e.g. 60s — refreshed on every entitlement change)
```

This ensures the Rate Limit Service always has an up-to-date quota value to enforce from, even if it cannot reach Entitlements directly.

**Also needed — reset the enforcement counter at period rollover:**

```
On TenantEntitlementsChanged (caused by drawdown at month close):
  Reset Valkey counter:
    Key:   quota:usage:{tenant_id}:llm_token_quota_monthly:{period}
    Value: 0  (fresh period starts)
```

Valkey is **already in the stack** (`VALKEY_URL` env var, `go-redis/v9` already used). This is a low-cost addition to the existing Valkey adapter (`adapter/outbound/valkey/cache.go`).

---

#### Side 2 — AI Team (Rate Limit Service + Envoy, CD-57)

The Rate Limit Service needs to:
1. Read quota from the Valkey key Entitlements writes
2. Use atomic `INCR` against a usage counter per tenant per period
3. Fall back to the local Valkey cache when upstream is unreachable — no fail-open

```
Current flow (fail-open):
  Request → Gateway → Rate Limit Service → Entitlements (ENT-I1) → decision
                              ↑
                    Entitlements DOWN → fail-open → shortfall

Fixed flow (local cache):
  Request → Gateway → Rate Limit Service
                              ↓
                    Valkey INCR usage counter  (atomic — no race condition)
                              ↓
                    usage > quota:tenant:{id}:llm_token_quota_monthly ?
                      YES → 429
                      NO  → allow
                              ↓
                    Works even if Entitlements is unreachable
```

---

**Result:**

| Cause | Owner | Fixed? |
|---|---|---|
| Race condition (concurrent requests) | AI Team | ✅ Atomic Valkey INCR in Rate Limit Service |
| Fail-open during outage (CD-57) | AI Team | ✅ Local Valkey cache — no fail-open needed |
| Quota data missing from Valkey | **Entitlements (us)** | ✅ Write dedicated quota key on every recompute |
| Counter reset at period rollover | **Entitlements (us)** | ✅ Reset usage counter on drawdown recompute |
| Rollover timing gap | U&M | Mitigated by request period tagging |
| Late records | U&M | Mitigated by late-record cutoff window |

---

## 6. Summary

```
                    TODAY                        RECOMMENDED
                 ┌────────────────────┐       ┌──────────────────────────────────┐
PREVENTION       │  None              │  →    │  Entitlements (us):              │
(new work)       │  Shortfalls happen │       │   Write quota key to Valkey      │
                 └────────────────────┘       │   Reset usage counter at rollover│
                                              │                                  │
                                              │  AI Team (CD-57):                │
                                              │   Atomic INCR in Rate Limit Svc  │
                                              │   Local Valkey cache (no fail-open│
                                              └──────────────────────────────────┘

                 ┌────────────────────┐
PRICE            │  billing_escalation│  Already in LLD — no change needed
                 │  operator review   │
                 └────────────────────┘

                 ┌────────────────────┐
ABSORB           │  accepted_loss     │  Already in LLD — no change needed
                 │  tolerance 10K/1%  │
                 └────────────────────┘
```

### Why not fail closed like OpenAI?

XpertPMS tenants are **businesses** with active tender workflows. A platform outage during a Rate Limit Service failure is worse than a rare small shortfall. The local cache approach achieves both — availability AND enforcement. No fail-open needed.

---

## 7. Open Items

| Item | Owner | Description |
|---|---|---|
| ENT-D11 | Commercial + Product | Resolution policy — ✅ resolved, implemented |
| ENT-D12 | Commercial + Product | Tolerance thresholds — ✅ resolved, implemented |
| **New** | **Entitlements (us)** | Write dedicated quota key to Valkey on every recompute; reset usage counter at period rollover |
| CD-57 | **AI Team** | Fail-open during Rate Limit Service outage — fix: atomic Valkey INCR + local quota cache in Rate Limit Service |
