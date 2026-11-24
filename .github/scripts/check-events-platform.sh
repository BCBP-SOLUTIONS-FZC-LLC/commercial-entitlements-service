#!/usr/bin/env bash
# Structural isolation of platform-events seams:
#   outbox.Enqueue* → only internal/adapter/outbound/eventbus/publisher.go
#   inbox.NewStore / store.Process → only internal/adapter/outbound/postgres/idempotency_repository.go
#   events.InitMetrics → both cmd/server/main.go and cmd/worker/main.go
# Complements check-forbidden-events-bypass.sh (WHAT is used) with WHERE it may be called.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0
EVENTBUS_PUBLISHER="internal/adapter/outbound/eventbus/publisher.go"
IDEMPOTENCY_REPO="internal/adapter/outbound/postgres/idempotency_repository.go"

# 1. outbox.Enqueue*/EnqueueOrdered outside the designated publisher.
while IFS= read -r f; do
  [[ "$f" == "$EVENTBUS_PUBLISHER" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  echo "::error file=$f::outbox.Enqueue* call outside $EVENTBUS_PUBLISHER. All outbox writes go through eventbus.Publisher."
  fail=1
done < <(grep -rlE '\boutbox\.(Enqueue|EnqueueOrdered)\b' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# 2. inbox.NewStore / inbox.Store.Process outside the designated repository.
while IFS= read -r f; do
  [[ "$f" == "$IDEMPOTENCY_REPO" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  echo "::error file=$f::inbox.NewStore or store.Process call outside $IDEMPOTENCY_REPO. All inbox operations go through InboxRepository."
  fail=1
done < <(grep -rlE '\binbox\.(NewStore|Store)\b|\.ProcessOnce\b' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# 3. events.InitMetrics — must appear in cmd/server/main.go and optionally cmd/worker/main.go.
init_hits=$(grep -rln '\bevents\.InitMetrics\b' --include='*.go' cmd/ 2>/dev/null || true)
bad_init=$(echo "$init_hits" | grep -vE '^cmd/(server|worker)/main\.go$' || true)
if [ -n "$bad_init" ]; then
  echo "::error::events.InitMetrics called outside cmd/server/main.go or cmd/worker/main.go:"
  echo "$bad_init"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  events platform isolation check passed"
