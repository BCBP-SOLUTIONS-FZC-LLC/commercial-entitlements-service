#!/usr/bin/env python3
"""
run_need_test.py — Need_test sheet automation for commercial-entitlements.

Each test GROUP gets its own freshly-provisioned tenant so tests never
contaminate each other.

Prerequisites:
    make docker-up   (Postgres + Valkey + Floci/LocalStack)
    make migrate     (apply schema migrations)
    make run         (service on :8080)

The script reads test cases from the Need_test sheet in the Excel workbook
and writes pass/fail results back. Each row is expected to have:
  Col A: Test Code  (e.g. ENT-I1-1)
  Col B: Category   (happy path | validation | authorization | security | idempotent | not found)
  Col C: Scenario   (description)
  Col D: Method     (GET | POST | PUT | PATCH | DELETE)
  Col E: API        (path template, e.g. /api/v1/internal/tenants/{id}/entitlements)
  Col F: Expected   (expected HTTP status code)
  Col G: Notes      (optional notes; "Must" = skip)
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
SHEET_NAME = "Need_test"

# ── Fixed test identities ─────────────────────────────────────────────────────
SYS_USER = "00000000-0000-0000-0000-000000000001"
SYS_TEN  = "aaaa0001-0001-0001-0001-000000000001"
NIL_UUID = "00000000-0000-0000-0000-000000000000"
PLAN_BASIC = "plan_basic_v1"
LIMIT_USERS = "max_users"

def H(user, tenant, roles):
    return {
        "Content-Type":    "application/json",
        "x-user-id":       user,
        "x-tenant-id":     tenant,
        "x-tenant-roles":  roles,
    }

H_SYS     = H(SYS_USER, SYS_TEN, "iam-system")
H_NO_AUTH = {"Content-Type": "application/json"}

# ── Per-group tenant state ────────────────────────────────────────────────────
_group_counter = 0

class TenantState:
    """Holds a freshly provisioned tenant for one test group."""
    def __init__(self):
        global _group_counter
        _group_counter += 1
        self.tid      = f"test{_group_counter:04d}-ent1-0001-0001-000000000001"
        self.addon_id  = None
        self.override_limit_key = LIMIT_USERS

    def provision(self):
        """Bootstrap the tenant via the internal recompute endpoint (or seed directly)."""
        # In a fully wired environment, the tenant row comes from a TenantIdentityProvisioned
        # event. For test purposes, POST a recompute to seed from the catalog.
        call("POST", BASE + f"/api/v1/internal/tenants/{self.tid}/entitlements/recompute", None, H_SYS)
        return self

def fresh_id():
    return str(uuid.uuid4())

# ── HTTP ──────────────────────────────────────────────────────────────────────
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

# ── Skip determination ────────────────────────────────────────────────────────
SKIP_PATTERNS = [
    "EVT-", "PERF-", "LOAD-",
    "event emitted", "outbox", "consumer",
    "concurrent", "race condition",
    "downstream dep",
]

def should_skip(tc, category, scenario, notes):
    if str(notes).strip().lower() == "must":
        return True
    combined = (tc + category + scenario).lower()
    return any(p.lower() in combined for p in SKIP_PATTERNS)

# ── Request builder ───────────────────────────────────────────────────────────
def build_request(tc, category, scenario, method, api_tmpl, ts: TenantState):
    url  = BASE + api_tmpl
    tc_u = tc.upper()
    cat  = category.lower()

    # Resolve placeholders
    url = re.sub(r'\{id\}',         ts.tid,         url)
    url = re.sub(r':id\b',          ts.tid,         url)
    url = re.sub(r'\{limitKey\}',   ts.override_limit_key, url)
    url = re.sub(r'\{addonId\}',    ts.addon_id or NIL_UUID, url)
    url = re.sub(r'/bad-?uuid\b|/not-a-uuid\b', '/bad-uuid', url)
    url = re.sub(r'/badid\b',       '/bad-uuid',    url)

    hdrs = H_SYS
    body = None

    if "no auth" in cat or "unauthorized" in cat:
        hdrs = H_NO_AUTH
    elif "cross" in cat or "cross-tenant" in tc_u:
        # Use a different tenant in the header than the path
        hdrs = H(SYS_USER, fresh_id(), "platform_operator")
    elif "/operator/" in url:
        hdrs = H(SYS_USER, ts.tid, "platform_operator")
    elif "/internal/" in url:
        hdrs = H_SYS
    elif "/tenants/" in url and "/operator/" not in url and "/internal/" not in url:
        hdrs = H(SYS_USER, ts.tid, "tenant_owner")

    if method in ("POST", "PUT", "PATCH"):
        if "recompute" in url:
            body = None
        elif "drawdown" in url:
            body = {"request_id": fresh_id(), "limit_key": LIMIT_USERS, "quantity": 1}
        elif "overrides" in url and method == "PUT":
            body = {"value": {"max_users": 100}, "record_version": 1}
        elif "trial-profile" in url and method == "PUT":
            body = {
                "plan_code": PLAN_BASIC,
                "profile": "trial_specific",
                "starts_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "ends_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 14 * 86400)),
                "reason": "need-test trial assignment",
            }
        elif "addons" in url and method == "POST":
            body = {"limit_key": LIMIT_USERS, "quantity": 50, "billing_ref": fresh_id()}
        elif "resolve" in url:
            body = {"accepted": True}

    return url, hdrs, body

# ── Main ──────────────────────────────────────────────────────────────────────
GREEN  = "FF92D050"
RED    = "FFFF0000"
YELLOW = "FFFFFF00"
GREY   = "FFD9D9D9"

def run():
    if not HAS_EXCEL:
        print("openpyxl not installed — run: pip install openpyxl")
        return

    try:
        wb = openpyxl.load_workbook(EXCEL_PATH)
    except FileNotFoundError:
        print(f"Excel file not found: {EXCEL_PATH}")
        print("Create the file with a 'Need_test' sheet first.")
        return

    ws = wb[SHEET_NAME]

    # Detect header row
    header_row = 1
    result_col  = None
    for col in range(1, ws.max_column + 2):
        val = str(ws.cell(row=header_row, column=col).value or "").lower()
        if val in ("result", "pass/fail", "status"):
            result_col = col
            break
    if result_col is None:
        result_col = ws.max_column + 1
        ws.cell(row=header_row, column=result_col, value="Result").font = Font(bold=True)

    _last_prefix = None
    _tenant_state: TenantState = None
    passed = failed = skipped = 0

    for row in ws.iter_rows(min_row=2, values_only=False):
        tc       = str(row[0].value or "").strip()
        category = str(row[1].value or "").strip()
        scenario = str(row[2].value or "").strip()
        method   = str(row[3].value or "GET").strip().upper()
        api_tmpl = str(row[4].value or "").strip()
        try:
            exp_code = int(row[5].value or 200)
        except (TypeError, ValueError):
            exp_code = 200
        notes = str(row[6].value if len(row) > 6 else "").strip()

        if not tc or not api_tmpl:
            continue

        if should_skip(tc, category, scenario, notes):
            skipped += 1
            cell = row[result_col - 1] if result_col - 1 < len(row) else ws.cell(row=row[0].row, column=result_col)
            cell = ws.cell(row=row[0].row, column=result_col)
            cell.value = "SKIP"
            cell.fill  = PatternFill("solid", fgColor=YELLOW)
            continue

        # New test group → fresh tenant
        prefix = re.match(r'^([A-Z]+-[A-Z0-9]+)', tc.upper())
        pfx    = prefix.group(1) if prefix else tc[:6]
        if pfx != _last_prefix:
            _last_prefix  = pfx
            _tenant_state = TenantState().provision()

        url, hdrs, body = build_request(tc, category, scenario, method, api_tmpl, _tenant_state)
        status, resp    = call(method, url, body, hdrs)
        ok              = (status == exp_code)

        # Capture state
        if status == 201 and "addons" in url:
            _tenant_state.addon_id = resp.get("id")

        result_cell = ws.cell(row=row[0].row, column=result_col)
        if ok:
            passed += 1
            result_cell.value = "PASS"
            result_cell.fill  = PatternFill("solid", fgColor=GREEN)
        else:
            failed += 1
            result_cell.value = f"FAIL ({status})"
            result_cell.fill  = PatternFill("solid", fgColor=RED)

        print(f"  [{'PASS' if ok else 'FAIL'}] {tc} {method} {url} → {status} (exp {exp_code})")

    wb.save(EXCEL_PATH)
    print(f"\n{passed} passed | {failed} failed | {skipped} skipped")
    print(f"Results written to {EXCEL_PATH} [{SHEET_NAME}]")

if __name__ == "__main__":
    run()
