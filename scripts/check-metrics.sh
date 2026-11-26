#!/usr/bin/env bash
# check-metrics.sh — CI validation for the Enterprise Platform Observability Standard.
#
# Validates internal/adapter/outbound/metrics/registry.go:
#   1. Namespace: "commercial"  (never "platform")  [struct-style, legacy check]
#   2. Subsystem: "entitlements"                     [struct-style, legacy check]
#   3. Counter metrics (NewCounter / NewCounterVec): Name ends with _total
#   4. Histogram metrics (NewHistogram / NewHistogramVec): Name ends with _seconds
#   5. No prohibited high-cardinality labels
#   6. No uppercase in metric name strings
#   7. Label vocabulary compliance
#
# Supports both the legacy prometheus struct-literal style (Namespace/Subsystem/Name
# fields) and the current gincommon call style where metric names appear as the
# second string argument to gincommon.NewCounterVec(reg, "name", ...) etc.
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

# ── 1. Namespace / Subsystem fields (legacy struct-style check) ───────────
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

# ── 2. Build counter/histogram name lists ─────────────────────────────────
# Supports both styles:
#   Legacy struct:   NewCounterVec(prometheus.CounterOpts{Name: "foo_total"}, ...)
#   gincommon call:  gincommon.NewCounterVec(nil, "commercial_entitlements_foo_total", ...)
hdr "Counter and Histogram suffix validation"

TMPOUT=$(mktemp)
awk '
# Track the most recent constructor kind seen.
# Handles both single-line and multi-line calls.

# Legacy struct-literal style constructors and gincommon wrappers
/NewHistogramVec|NewHistogram[^V]/ { kind = "histogram" }
/NewCounterVec|NewCounter[^V]/     { kind = "counter" }

# Legacy struct-literal style Name field
/Name:[[:space:]]+"[a-z0-9_]+"/ {
  match($0, /"[a-z0-9_]+"/)
  name = substr($0, RSTART+1, RLENGTH-2)
  if (kind != "") { print kind ":" name; kind = "" }
  next
}

# gincommon call style: the name "commercial_entitlements_..." may appear on
# the same line as the constructor or on the following line.
match($0, /"commercial_entitlements_[a-z0-9_]+"/) {
  name = substr($0, RSTART+1, RLENGTH-2)
  if (kind != "") { print kind ":" name; kind = "" }
}
' "$REGISTRY" > "$TMPOUT"

while IFS= read -r entry; do
  kind="${entry%%:*}"
  name="${entry##*:}"
  case "$kind" in
    counter)
      case "$name" in
        *_total) log "Counter _total OK: $name" ;;
        *)       fail "Counter missing _total suffix: $name" ;;
      esac
      ;;
    histogram)
      case "$name" in
        *_seconds) log "Histogram _seconds OK: $name" ;;
        *)         fail "Histogram missing _seconds suffix: $name" ;;
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

# ── 4. Lowercase metric names ──────────────────────────────────────────────
# Extract metric name strings: both struct Name: "..." and gincommon inline names.
hdr "Lowercase metric names"
TMPNAMES=$(mktemp)
{
  # Legacy struct style
  grep -E 'Name:[[:space:]]+"[a-z0-9_]+"' "$REGISTRY" \
    | grep -oE '"[a-z0-9_]+"' | tr -d '"' || true
  # gincommon call style
  grep -oE '"commercial_entitlements_[a-z0-9_]+"' "$REGISTRY" | tr -d '"' || true
} | sort -u > "$TMPNAMES"

while IFS= read -r name; do
  [ -z "$name" ] && continue
  if echo "$name" | grep -qE '[A-Z]'; then
    fail "Metric Name contains uppercase: $name"
  else
    log "Lowercase OK: $name"
  fi
done < "$TMPNAMES"
rm -f "$TMPNAMES"

# ── 5. Label vocabulary compliance ────────────────────────────────────────
# Only labels from the approved platform/domain vocabulary may appear in
# []string{...} label arrays or variadic label arguments. High-cardinality
# identifiers are caught by check 3 above; this enforces the positive allowlist.
hdr "Label vocabulary compliance"

# Approved label vocabulary (Platform Observability Standard, Tier 3).
APPROVED_LABELS="cause outcome limit_key disposition status shortfall kind consumer event_type profile reason violation_type"

TMPLAB=$(mktemp)
# Extract labels from both styles:
#   Legacy struct:  []string{"cause", "outcome"}
#   gincommon:      variadic args after help string — these appear on lines like
#                   		"cause", "outcome"))
#                   i.e. lines where every token is a short quoted label or punctuation.
{
  # Legacy []string{...} label arrays
  grep -oE '\[\]string\{[^}]+\}' "$REGISTRY" 2>/dev/null \
    | grep -oE '"[a-z_]+"' | tr -d '"' || true

  # Variadic label argument lines: lines that contain quoted short identifiers
  # followed by one or more closing parentheses (gincommon call close).
  # This excludes bare import strings like \t"sync" which have no closing paren.
  grep -E '^[[:space:]]+"[a-z][a-z_]+"(,[[:space:]]+"[a-z][a-z_]+")*\)+[,]?$' \
    "$REGISTRY" 2>/dev/null \
    | grep -oE '"[a-z][a-z_]+"' | tr -d '"' \
    | grep -v '^commercial_entitlements_' || true
} | sort -u > "$TMPLAB"

while IFS= read -r label; do
  [ -z "$label" ] && continue
  found=0
  for approved in $APPROVED_LABELS; do
    [ "$label" = "$approved" ] && found=1 && break
  done
  if [ "$found" -eq 0 ]; then
    fail "Unapproved label '$label' — not in platform/domain vocabulary: {$APPROVED_LABELS}"
  else
    log "Label '$label' approved"
  fi
done < "$TMPLAB"
rm -f "$TMPLAB"

# ── Summary ───────────────────────────────────────────────────────────────
echo ""
if [ "$FAILURES" -gt 0 ]; then
  echo "check-metrics: $FAILURES failure(s). See output above."
  exit 1
else
  echo "check-metrics: all checks passed ($NAME_COUNT metrics validated)."
  exit 0
fi
