#!/usr/bin/env python3
"""
commercial-entitlements automated test runner.

Exercises the HTTP API against a running local instance and writes pass/fail
results to an Excel sheet (mirrors the IAM org-membership test runner pattern).

Usage:
    python3 scripts/test_runner.py

Prerequisites:
    make docker-up   (Postgres + Valkey + Floci/LocalStack)
    make migrate     (apply schema migrations)
    make run         (service on :8080)
"""
import json
import re
import time
import uuid
import urllib.request
import urllib.error

try:
    import openpyxl
    from openpyxl.styles import PatternFill, Font, Alignment
    HAS_EXCEL = True
except ImportError:
    HAS_EXCEL = False

# ── Config ────────────────────────────────────────────────────────────────────
BASE       = "http://localhost:8080"
EXCEL_PATH = "/Users/sharmila/bcbp-solutions/XpertPMS/Commercial/Entitlements/Testing/commercial-entitlements-testing.xlsx"
SHEET_NAME = "Final"

# ── Fixed test identities ─────────────────────────────────────────────────────
# These match seed data inserted by init-db.sql + migration fixtures.
TENANT_A = "cccc0001-ent1-0001-0001-000000000001"   # Active tenant with plan
TENANT_B = "cccc0002-ent1-0001-0001-000000000002"   # Trial tenant
TENANT_C = "cccc0003-ent1-0001-0001-000000000003"   # Suspended tenant
SYS_USER = "00000000-0000-0000-0000-000000000001"
SYS_TEN  = "aaaa0001-0001-0001-0001-000000000001"
NIL_UUID = "00000000-0000-0000-0000-000000000000"

PLAN_BASIC    = "plan_basic_v1"
PLAN_PRO      = "plan_pro_v1"
LIMIT_USERS   = "max_users"
LIMIT_STORAGE = "max_storage_gb"
LIMIT_API     = "max_api_calls_per_month"

# ── Auth header sets ──────────────────────────────────────────────────────────
def H(user, tenant, roles):
    return {
        "Content-Type": "application/json",
        "x-user-id":    user,
        "x-tenant-id":  tenant,
        "x-tenant-roles": roles,
    }

H_SYS       = H(SYS_USER, SYS_TEN,  "iam-system")
H_OP        = H(SYS_USER, TENANT_A, "platform_operator")
H_TENANT    = H(SYS_USER, TENANT_A, "tenant_owner")
H_NO_AUTH   = {"Content-Type": "application/json"}
H_CROSS     = H(SYS_USER, TENANT_B, "platform_operator")  # used for cross-tenant tests

# ── HTTP helper ───────────────────────────────────────────────────────────────
def call(method, url, body=None, hdrs=None):
    if hdrs is None:
        hdrs = H_SYS
    data = json.dumps(body).encode() if body is not None else None
    req  = urllib.request.Request(url, data=data, headers=hdrs, method=method)
    try:
        r = urllib.request.urlopen(req, timeout=8)
        return r.status, json.loads(r.read() or b'{}')
    except urllib.error.HTTPError as e:
        try:    return e.code, json.loads(e.read())
        except: return e.code, {}
    except Exception as ex:
        return 0, {"_error": str(ex)}

def get_rv(url, hdrs=None):
    _, b = call("GET", url, None, hdrs)
    return b.get("record_version", 1)

# ── Dynamic state ─────────────────────────────────────────────────────────────
state: dict = {
    "addon_id":    None,
    "shortfall_id": None,
}

_tenant_counter = 100

def fresh_tenant():
    global _tenant_counter
    _tenant_counter += 1
    return f"cccc{_tenant_counter:04d}-ent1-0001-0001-000000000001"

def fresh_id():
    return str(uuid.uuid4())

# ── Test cases ────────────────────────────────────────────────────────────────
# Format: (test_code, category, scenario, method, url_template, expected_status)
#
# Placeholders resolved: {id}=TENANT_A, {limitKey}=LIMIT_USERS,
#   {addonId}=state["addon_id"], {shortfallId}=state["shortfall_id"]

TESTS = [
    # ── Infrastructure ─────────────────────────────────────────────────────
    ("INFRA-1",  "happy path",  "GET /healthz returns 200",   "GET",  "/healthz",          200),
    ("INFRA-2",  "happy path",  "GET /readyz returns 200",    "GET",  "/readyz",           200),

    # ── ENT-I1: internal effective-set ─────────────────────────────────────
    ("ENT-I1-1",  "happy path",    "GET internal effective-set",          "GET",  "/api/v1/internal/tenants/{id}/entitlements",         200),
    ("ENT-I1-2",  "authorization", "no auth headers → 401",              "GET",  "/api/v1/internal/tenants/{id}/entitlements",         401),
    ("ENT-I1-3",  "validation",    "invalid tenant UUID → 400",          "GET",  "/api/v1/internal/tenants/not-a-uuid/entitlements",   400),
    ("ENT-I1-4",  "not found",     "unknown tenant → 404",               "GET",  "/api/v1/internal/tenants/{unknown}/entitlements",    404),

    # ── ENT-I1b: recompute ─────────────────────────────────────────────────
    ("ENT-I1b-1", "happy path",    "POST recompute returns 200",         "POST", "/api/v1/internal/tenants/{id}/entitlements/recompute", 200),
    ("ENT-I1b-2", "idempotent",    "POST recompute twice → 200 (noop)",  "POST", "/api/v1/internal/tenants/{id}/entitlements/recompute", 200),

    # ── ENT-I2: current plan ───────────────────────────────────────────────
    ("ENT-I2-1",  "happy path",    "GET current plan returns 200",       "GET",  "/api/v1/internal/tenants/{id}/plans/current",        200),

    # ── ENT-I3: drawdown ──────────────────────────────────────────────────
    ("ENT-I3-1",  "happy path",    "POST drawdown succeeds",             "POST", "/api/v1/internal/tenants/{id}/entitlements/drawdown", 200),
    ("ENT-I3-2",  "idempotent",    "POST drawdown with same request_id → same result (ENT-INV-3)", "POST", "/api/v1/internal/tenants/{id}/entitlements/drawdown", 200),

    # ── ENT-T1: tenant effective-set ──────────────────────────────────────
    ("ENT-T1-1",  "happy path",    "GET tenant effective-set",           "GET",  "/api/v1/tenants/{id}/entitlements",                  200),
    ("ENT-T1-2",  "security",      "cross-tenant access → 403",          "GET",  "/api/v1/tenants/{id}/entitlements",                  403),
    ("ENT-T1-3",  "authorization", "no auth → 401",                     "GET",  "/api/v1/tenants/{id}/entitlements",                  401),

    # ── ENT-O1: list overrides ────────────────────────────────────────────
    ("ENT-O1-1",  "happy path",    "GET operator overrides list",        "GET",  "/api/v1/operator/tenants/{id}/entitlements/overrides",         200),
    ("ENT-O1-2",  "authorization", "tenant role → 403",                  "GET",  "/api/v1/operator/tenants/{id}/entitlements/overrides",         403),

    # ── ENT-O2: set/remove override ───────────────────────────────────────
    ("ENT-O2-1",  "happy path",    "PUT override sets value",            "PUT",  "/api/v1/operator/tenants/{id}/entitlements/overrides/{limitKey}", 200),
    ("ENT-O2-2",  "happy path",    "GET override returns set value",     "GET",  "/api/v1/operator/tenants/{id}/entitlements/overrides/{limitKey}", 200),
    ("ENT-O2-3",  "happy path",    "GET override history",               "GET",  "/api/v1/operator/tenants/{id}/entitlements/overrides/{limitKey}/history", 200),
    ("ENT-O2-4",  "happy path",    "DELETE override removes it",         "DELETE", "/api/v1/operator/tenants/{id}/entitlements/overrides/{limitKey}", 204),
    ("ENT-O2-5",  "not found",     "GET override after delete → 404",   "GET",  "/api/v1/operator/tenants/{id}/entitlements/overrides/{limitKey}", 404),

    # ── ENT-O5: addon management ──────────────────────────────────────────
    ("ENT-O5-1",  "happy path",    "GET addons list",                    "GET",  "/api/v1/operator/tenants/{id}/entitlements/addons",   200),
    ("ENT-O5-2",  "happy path",    "POST add addon",                     "POST", "/api/v1/operator/tenants/{id}/entitlements/addons",   201),
    ("ENT-O5-3",  "happy path",    "GET drawdowns for addon",            "GET",  "/api/v1/operator/tenants/{id}/entitlements/addons/drawdowns", 200),

    # ── ENT-O3: trial profile ─────────────────────────────────────────────
    ("ENT-O3-1",  "happy path",    "PUT trial profile assigns",          "PUT",  "/api/v1/operator/tenants/{id}/entitlements/trial-profile", 200),
    ("ENT-O3-2",  "happy path",    "GET trial profile returns",          "GET",  "/api/v1/operator/tenants/{id}/entitlements/trial-profile", 200),
    ("ENT-O3-3",  "happy path",    "DELETE trial profile purges",        "DELETE", "/api/v1/operator/tenants/{id}/entitlements/trial-profile", 204),

    # ── Plans ─────────────────────────────────────────────────────────────
    ("PLANS-1",   "happy path",    "GET operator plans list",            "GET",  "/api/v1/operator/plans",                             200),
]

# ── Request builder ───────────────────────────────────────────────────────────
_drawdown_request_id = fresh_id()

def build_request(tc, method, url_tmpl, exp_code):
    tc_up = tc.upper()
    url   = BASE + url_tmpl

    # Resolve placeholders
    url = url.replace("{id}",         TENANT_A)
    url = url.replace("{unknown}",    fresh_tenant())
    url = url.replace("{limitKey}",   LIMIT_USERS)
    url = url.replace("{addonId}",    state.get("addon_id") or NIL_UUID)
    url = url.replace("{shortfallId}", state.get("shortfall_id") or NIL_UUID)

    hdrs = H_OP   # most operator routes need platform_operator
    body = None

    # Cross-tenant test
    if "cross-tenant" in tc_up or "CROSS" in tc_up:
        hdrs = H_CROSS

    # No auth
    if "no auth" in tc_up or "NO_AUTH" in tc_up or "401" in tc_up:
        hdrs = H_NO_AUTH

    # Tenant-role (not operator)
    if "tenant role" in tc_up:
        hdrs = H_TENANT

    # Internal routes use iam-system
    if "/internal/" in url:
        hdrs = H_SYS

    # Tenant routes use tenant_owner
    if "/api/v1/tenants/" in url and "/internal/" not in url and "/operator/" not in url:
        hdrs = H_TENANT
        if "cross-tenant" in tc_up:
            hdrs = H_CROSS

    # Bodies
    if method in ("POST", "PUT", "PATCH"):
        if "recompute" in url:
            body = None
        elif "drawdown" in url:
            body = {
                "request_id": _drawdown_request_id if "idempotent" in tc_up else fresh_id(),
                "limit_key": LIMIT_API,
                "quantity": 100,
            }
        elif "overrides" in url and method == "PUT":
            body = {"value": {"max_users": 500}, "record_version": 1}
        elif "trial-profile" in url and method == "PUT":
            body = {
                "plan_code": PLAN_BASIC,
                "profile": "trial_specific",
                "starts_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "ends_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 14 * 86400)),
                "reason": "need-test trial assignment",
            }
        elif "trial-overrides" in url and method == "PUT":
            body = {"value": {"max_users": 10}}
        elif "addons" in url and method == "POST":
            body = {"limit_key": LIMIT_USERS, "quantity": 50, "billing_ref": fresh_id()}
        elif "shortfalls" in url and "resolve" in url:
            body = {"accepted": True}
        elif "refresh" in url:
            body = None

    return url, hdrs, body


# ── Coloured Excel output ──────────────────────────────────────────────────────
GREEN  = "FF92D050"
RED    = "FFFF0000"
YELLOW = "FFFFFF00"
GREY   = "FFD9D9D9"

def run_all():
    results = []
    passed = failed = skipped = 0

    for (tc, category, scenario, method, url_tmpl, exp_code) in TESTS:
        # Skip tests that need state from prior steps
        if state.get("addon_id") is None and ("{addonId}" in url_tmpl or "shortfall" in url_tmpl.lower()):
            skipped += 1
            results.append((tc, category, scenario, exp_code, "SKIP", "—", "awaiting addon creation"))
            continue

        url, hdrs, body = build_request(tc, method, url_tmpl, exp_code)
        status, resp    = call(method, url, body, hdrs)
        passed_flag     = (status == exp_code)

        if passed_flag:
            passed += 1
            verdict = "PASS"
        else:
            failed += 1
            verdict = "FAIL"

        # Capture ids from creation responses
        if "ENT-O5-2" in tc and status == 201:
            state["addon_id"] = resp.get("id")
        if "ENT-O6" in tc and status == 200 and resp.get("items"):
            state["shortfall_id"] = resp["items"][0].get("id")

        note = resp.get("code", "") or resp.get("error", "") or ""
        results.append((tc, category, scenario, exp_code, status, verdict, str(note)[:80]))
        print(f"  [{verdict}] {tc} {method} {url_tmpl} → {status} (expected {exp_code})")

    print(f"\nResults: {passed} passed, {failed} failed, {skipped} skipped / {len(TESTS)} total")

    if HAS_EXCEL:
        _write_excel(results)

    return failed == 0


def _write_excel(results):
    try:
        wb = openpyxl.load_workbook(EXCEL_PATH)
    except FileNotFoundError:
        wb = openpyxl.Workbook()

    if SHEET_NAME in wb.sheetnames:
        ws = wb[SHEET_NAME]
    else:
        ws = wb.create_sheet(SHEET_NAME)

    headers = ["Test Code", "Category", "Scenario", "Expected", "Actual", "Result", "Note"]
    for col, h in enumerate(headers, 1):
        cell = ws.cell(row=1, column=col, value=h)
        cell.font = Font(bold=True)
        cell.fill = PatternFill("solid", fgColor=GREY)

    for row_idx, (tc, cat, scenario, exp, actual, verdict, note) in enumerate(results, 2):
        ws.cell(row=row_idx, column=1, value=tc)
        ws.cell(row=row_idx, column=2, value=cat)
        ws.cell(row=row_idx, column=3, value=scenario)
        ws.cell(row=row_idx, column=4, value=exp)
        ws.cell(row=row_idx, column=5, value=actual)

        v_cell = ws.cell(row=row_idx, column=6, value=verdict)
        if verdict == "PASS":
            v_cell.fill = PatternFill("solid", fgColor=GREEN)
        elif verdict == "FAIL":
            v_cell.fill = PatternFill("solid", fgColor=RED)
        else:
            v_cell.fill = PatternFill("solid", fgColor=YELLOW)

        ws.cell(row=row_idx, column=7, value=note)

    wb.save(EXCEL_PATH)
    print(f"Results written to {EXCEL_PATH} [{SHEET_NAME}]")


if __name__ == "__main__":
    import sys
    ok = run_all()
    sys.exit(0 if ok else 1)
