#!/usr/bin/env bash
# Structural isolation of platform-events seams:
#   outbox.Enqueue* → only internal/adapter/outbound/eventbus/publisher.go
#   inbox.NewStore / store.Process → only internal/adapter/outbound/postgres/idempotency_repository.go
#   events.InitMetrics → cmd/server, cmd/worker, cmd/reconciler, cmd/catalog-refresh (any binary with a pool+outbox)
# Complements check-forbidden-events-bypass.sh (WHAT is used) with WHERE it may be called.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0
EVENTBUS_PUBLISHER="internal/adapter/outbound/eventbus/publisher.go"
IDEMPOTENCY_REPO="internal/adapter/outbound/postgres/idempotency_repository.go"

# 1. outbox.Enqueue*/EnqueueOrdered outside the designated publisher.
# Two-pass: find candidate files, then confirm the pattern on a non-comment line.
# Doc comments that mention "outbox.Enqueue" for documentation are excluded.
while IFS= read -r f; do
  [[ "$f" == "$EVENTBUS_PUBLISHER" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  if grep -v '^\s*//' "$f" 2>/dev/null | grep -qE '\boutbox\.(Enqueue|EnqueueOrdered)\b'; then
    echo "::error file=$f::outbox.Enqueue* call outside $EVENTBUS_PUBLISHER. All outbox writes go through eventbus.Publisher."
    fail=1
  fi
done < <(grep -rlE '\boutbox\.(Enqueue|EnqueueOrdered)\b' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# 2. Direct import of platform-events pkg/inbox outside the designated repository.
# This catches files that bypass the port.Inbox abstraction and instantiate
# platformInbox.Store directly. The port.Inbox interface (which internal/adapter/
# inbound/consumer/dedup.go uses via port.Inbox.ProcessOnce) is explicitly
# allowed — it is the correct abstraction layer.
while IFS= read -r f; do
  [[ "$f" == "$IDEMPOTENCY_REPO" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  echo "::error file=$f::Direct import of platform-events pkg/inbox outside $IDEMPOTENCY_REPO. All inbox operations go through port.Inbox / InboxRepository."
  fail=1
done < <(grep -rl '"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/inbox"' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# 3. events.InitMetrics — allowed in any binary that owns a Postgres pool and
# an outbox publisher. Currently: cmd/server, cmd/worker, cmd/reconciler,
# cmd/catalog-refresh. All four create outboxPublisher + pgcommon.NewPool.
init_hits=$(grep -rln '\bevents\.InitMetrics\b' --include='*.go' cmd/ 2>/dev/null || true)
bad_init=$(echo "$init_hits" | grep -vE '^cmd/(server|worker|reconciler|catalog-refresh)/main\.go$' || true)
if [ -n "$bad_init" ]; then
  echo "::error::events.InitMetrics called in an unexpected binary (expected cmd/{server,worker,reconciler,catalog-refresh}/main.go):"
  echo "$bad_init"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  events platform isolation check passed"
