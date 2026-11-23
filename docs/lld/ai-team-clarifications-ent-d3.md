# AI Team Clarifications — ENT-D3 (`model_tier` Model Names)

**Raised by:** Commercial Team  
**Date:** 2026-11-22  
**Reference:** ENT-D3, CD-41, PD-LLM-1  
**Blocking:** Product approval of PD-LLM-1 → Usage & Metering Gateway allow-list

> **Note to AI team:** Nothing in this document is decided or confirmed yet.
> All model assignments below are **proposals only**. For each question we need you to either
> **confirm the proposal is acceptable** or **suggest an alternative** with your reasoning.

---

## Background

The Entitlements service returns a `model_tier` label (`basic` / `standard` / `premium`) for
every tenant based on their plan (Starter / Pro / Enterprise). Usage & Metering reads this
label to build the **Gateway allow-list** — which actual AI models that tenant can call.

The allow-list cannot be built until the model-to-tier mapping is confirmed by the AI team
and approved by Product (PD-LLM-1).

---

## Proposed Model Table — NOT YET DECIDED, Needs AI Team Confirmation

| Task class | `basic` (Starter) | `standard` (Pro) | `premium` (Enterprise) |
|---|---|---|---|
| `draft` | Claude Haiku | Claude Sonnet | Claude Sonnet (default); Claude Opus for specific scenarios (see Q3) |
| `judge` | Amazon Nova Pro | Amazon Nova Premier *(or Nova Pro — not decided)* | Claude Sonnet for an Opus draft; Nova Premier for a Sonnet draft |
| `extract`, `refine-instruction` | Claude Haiku | Claude Haiku | Claude Haiku |
| `refine-query` | Amazon Nova Micro | Amazon Nova Micro | Amazon Nova Micro |
| `embed` / `rerank` | `bge-m3` / `bge-reranker-v2-m3` (self-hosted) | same | same |

**For each row: please confirm it is acceptable OR propose an alternative.**

---

## Questions for AI Team

---

### Q1 — Bedrock Model Availability in `ap-south-1` (R-1)

**Not confirmed yet. We need you to tell us.**

Which of the following models are available in AWS Bedrock region **`ap-south-1`** today,
**without** a cross-region inference profile? *(Cross-region e.g. `me-central-1` is planned
for a later phase — not Phase 1.)*

| Model | Available in `ap-south-1`? | Exact Bedrock Model ID |
|---|---|---|
| Claude Haiku | **?** | **?** |
| Claude Sonnet | **?** | **?** |
| Claude Opus | **?** | **?** |
| Amazon Nova Micro | **?** | **?** |
| Amazon Nova Pro | **?** | **?** |
| Amazon Nova Premier | **?** | **?** |

If any model in the proposed table above is **not available** in `ap-south-1`, please suggest
an acceptable alternative for that task class and tier.

**Expected output:** Filled table above + flag any unavailable models with suggested alternatives.

---

### Q2 — English + Arabic Quality Evaluation (R-2)

**Not run yet. We need your evaluation results.**

Please run quality evaluation on the following draft/judge pairs for **both English and Arabic**.
These pairs are proposals — if the evaluation shows a pair is not acceptable, please suggest
an alternative pairing.

| Plan | Proposed Draft | Proposed Judge | Evaluation needed |
|---|---|---|---|
| Starter (`basic`) | Claude Haiku | Amazon Nova Pro | English + Arabic |
| Pro (`standard`) | Claude Sonnet | Amazon Nova Premier | English + Arabic |
| Enterprise (`premium`) | Claude Sonnet / Claude Opus | Claude Sonnet | English + Arabic |

**Evaluation criteria:**
- Output quality for tender document drafting (long-form, structured)
- Arabic language accuracy and fluency (LLM IG-2)
- Judge scoring consistency across both languages
- Compliance section quality (Enterprise Opus path)

**Constraint:** Judge must be from a **different model family** than the drafter (VER-3).

**Expected output:** Pass/fail per pair per language. If a pair fails, propose an acceptable
alternative.

---

### Q3 — Enterprise Opus Routing Rule (R-4)

**Not decided yet. We need your input.**

For Enterprise (`premium`), Opus is proposed only for specific scenarios — not all drafts.
The proposed trigger condition is:

> Opus when: section is **compliance-critical** (Domain Profile flag) **OR** run is
> **long-form** (above a token threshold — threshold not decided yet).

Please answer:

1. Is "compliance-critical section" a flag that already exists in the Domain Profile at
   request time — or does it need to be added?
2. What token threshold would you recommend for "long-form run" to trigger Opus?
3. Should the condition be **OR** (either triggers Opus) or **AND** (both required)?
4. Is this proposal acceptable, or do you suggest a different routing rule entirely?

**Expected output:** Confirmed or revised routing rule for `draft-premium-max` (Opus trigger).

---

### Q4 — Exact Bedrock Model IDs

**Not confirmed yet. Needed after Q1 is answered.**

Once Q1 confirms which models are available, please provide the **exact Bedrock model IDs**
for each confirmed model:

| Model | Exact Bedrock Model ID |
|---|---|
| Claude Haiku | ? |
| Claude Sonnet | ? |
| Claude Opus | ? |
| Amazon Nova Micro | ? |
| Amazon Nova Pro | ? |
| Amazon Nova Premier | ? |

**Expected output:** Exact IDs to pin in Model Profiles configuration.

---

## Summary — What We Need Back

| # | Question | Confirm proposal OR suggest alternative |
|---|---|---|
| Q1 | Bedrock availability in `ap-south-1` | Fill availability table + flag unavailable models |
| Q2 | English + Arabic evaluation on 3 pairs | Pass/fail report + alternative if any pair fails |
| Q3 | Enterprise Opus routing rule | Confirm proposed trigger OR suggest different rule |
| Q4 | Exact Bedrock model IDs | Fill model ID table (after Q1) |

**Once AI team responds → Finance runs R-3 cost validation → Product approves PD-LLM-1
→ AI team writes Gateway allow-list → Usage & Metering is unblocked.**

---

## What Does NOT Change in Entitlements

- `model_tier` in ENT-I1 continues to return `basic` / `standard` / `premium` — no change.
- No new endpoints, no schema changes, no migration needed.
- This gap lives entirely in AI team + Usage & Metering + Product.
