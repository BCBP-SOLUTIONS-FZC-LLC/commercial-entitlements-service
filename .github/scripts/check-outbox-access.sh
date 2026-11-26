#!/usr/bin/env bash
# Every outbox_events write/read/prune must go through platform-events
# (outbox.EnqueueOrdered / outbox.Runner.PrunePublished), never hand-rolled
# SQL against outbox_events. Guards against regression of the pattern where
# a direct INSERT INTO outbox_events bypassed schema validation and DLQ routing.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

# 1. Hand-rolled DML SQL against outbox_events table.
# Excludes GRANT/REVOKE lines (DDL privilege management in migrations is legitimate)
# and excludes comment-only lines in Go files. Only actual DML (INSERT/UPDATE/DELETE/
# SELECT) statements written by application code are blocked.
hits=$(grep -rEn '(INSERT|UPDATE|DELETE|SELECT)[[:space:]]+.*outbox_events' \
         --include='*.go' --include='*.sql' cmd/ internal/ 2>/dev/null \
       | grep -v '_test.go' \
       | grep -v 'outbox\.ApplySchema' \
       | grep -vE '^\s*--' \
       | grep -vE '^\S+:[0-9]+:\s*(GRANT|REVOKE)\b' \
       || true)
if [ -n "$hits" ]; then
  echo "::error::Direct SQL against outbox_events detected. Use outbox.EnqueueOrdered (via eventbus.Publisher) and outbox.Runner.PrunePublished."
  echo "$hits"
  fail=1
fi

# 2. outbox.Enqueue*/EnqueueOrdered is only called from eventbus/publisher.go.
# (Belt-and-suspenders with check-events-platform.sh.)
# Two-pass: find candidate files, then confirm the pattern on a non-comment line.
# Doc comments that mention "outbox.Enqueue" for documentation are excluded.
EVENTBUS_PUBLISHER="internal/adapter/outbound/eventbus/publisher.go"
while IFS= read -r f; do
  [[ "$f" == "$EVENTBUS_PUBLISHER" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  if grep -v '^\s*//' "$f" 2>/dev/null | grep -qE '\boutbox\.(Enqueue|EnqueueOrdered)\b'; then
    echo "::error file=$f::outbox.Enqueue* call outside $EVENTBUS_PUBLISHER."
    fail=1
  fi
done < <(grep -rlE '\boutbox\.(Enqueue|EnqueueOrdered)\b' --include='*.go' internal/ cmd/ 2>/dev/null || true)

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  outbox access check passed"
