#!/usr/bin/env bash
# check-metrics.sh — CI validation for the Enterprise Platform Observability Standard.
#
# Validates internal/adapter/outbound/metrics/registry.go:
#   1. Namespace: "commercial"  (never "platform")
#   2. Subsystem: "entitlements"
#   3. Counter metrics (NewCounter / NewCounterVec): Name ends with _total
#   4. Histogram metrics (NewHistogram / NewHistogramVec): Name ends with _seconds
#   5. No prohibited high-cardinality labels
#   6. No uppercase in Name values
#
# Compatible with bash 3.2+ (macOS system shell).
# Exits 0 on pass, 1 on any failure.
set -euo pipefail

REGISTRY="internal/adapter/outbound/metrics/registry.go"
VERBOSE=0
[ "${1:-}" = "--verbose" ] && VERBOSE=1
FAILURES=0

log()  { [ "$VERBOSE" -eq 1 ] && echo "  OK  $*" || true; }
fail() { echo "FAIL $*"; FAILURES=$((FAILURES + 1)); }
hdr()  { echo ""; echo "=== $* ==="; }

# ── 1. Namespace / Subsystem fields ──────────────────────────────────────
hdr "Namespace and Subsystem fields"
if grep -E 'Namespace:[[:space:]]+"platform"' "$REGISTRY" >/dev/null 2>&1; then
  fail "Namespace: \"platform\" found — Tier 1 metrics are owned by the platform-events library"
else
  log "No platform namespace claims"
fi

bad_ns=$(grep -E 'Namespace:[[:space:]]+"' "$REGISTRY" | grep -v '"commercial"' || true)
if [ -n "$bad_ns" ]; then
  fail "Unexpected Namespace (expected \"commercial\"): $bad_ns"
else
  log "Namespace field OK"
fi

bad_sub=$(grep -E 'Subsystem:[[:space:]]+"' "$REGISTRY" | grep -v '"entitlements"' || true)
if [ -n "$bad_sub" ]; then
  fail "Unexpected Subsystem (expected \"entitlements\"): $bad_sub"
else
  log "Subsystem field OK"
fi

# ── 2. Build counter/histogram name lists via awk ─────────────────────────
# Strategy: scan the file. When we see NewCounter/NewHistogram, record the type.
# When we see Name: "...", classify under the most recent constructor type.
hdr "Counter and Histogram suffix validation"

TMPOUT=$(mktemp)
awk '
/NewHistogramVec|NewHistogram[^V]/ { kind = "histogram" }
/NewCounterVec|NewCounter[^V]/     { kind = "counter" }
/Name:[[:space:]]+"[a-z0-9_]+"/ {
  match($0, /"[a-z0-9_]+"/)
  name = substr($0, RSTART+1, RLENGTH-2)
  if (kind != "") print kind ":" name
}
' "$REGISTRY" > "$TMPOUT"

while IFS= read -r entry; do
  kind="${entry%%:*}"
  name="${entry##*:}"
  full="commercial_entitlements_$name"
  case "$kind" in
    counter)
      case "$name" in
        *_total) log "Counter _total OK: $full" ;;
        *)       fail "Counter missing _total suffix: $full" ;;
      esac
      ;;
    histogram)
      case "$name" in
        *_seconds) log "Histogram _seconds OK: $full" ;;
        *)         fail "Histogram missing _seconds suffix: $full" ;;
      esac
      ;;
  esac
done < "$TMPOUT"

NAME_COUNT=$(wc -l < "$TMPOUT" | tr -d ' ')
rm -f "$TMPOUT"
echo "  Validated $NAME_COUNT metric definitions."

# ── 3. Prohibited high-cardinality labels ─────────────────────────────────
hdr "Prohibited high-cardinality labels"
for label in tenant_id user_id email request_id event_id session_id; do
  if grep -E "\"$label\"" "$REGISTRY" >/dev/null 2>&1; then
    fail "Prohibited high-cardinality label found: $label"
  else
    log "No '$label' label (OK)"
  fi
done

# ── 4. Lowercase Name values ──────────────────────────────────────────────
hdr "Lowercase metric names"
TMPNAMES=$(mktemp)
grep -E 'Name:[[:space:]]+"[a-z0-9_]+"' "$REGISTRY" \
  | grep -oE '"[a-z0-9_]+"' | tr -d '"' > "$TMPNAMES"

while IFS= read -r name; do
  [ -z "$name" ] && continue
  if echo "$name" | grep -qE '[A-Z]'; then
    fail "Metric Name contains uppercase: $name"
  else
    log "Lowercase OK: $name"
  fi
done < "$TMPNAMES"
rm -f "$TMPNAMES"

# ── Summary ───────────────────────────────────────────────────────────────
echo ""
if [ "$FAILURES" -gt 0 ]; then
  echo "check-metrics: $FAILURES failure(s). See output above."
  exit 1
else
  echo "check-metrics: all checks passed ($NAME_COUNT metrics validated)."
  exit 0
fi
