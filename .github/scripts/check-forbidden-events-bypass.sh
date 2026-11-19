#!/usr/bin/env bash
# Rejects raw aws-sdk-go-v2 SNS/SQS imports outside the single wiring file
# (cmd/server/main.go). Every publish must go through platform-events
# (events.NewSNSPublisher / events.NewSQSConsumerWithClient); every consume
# must use the registered platform-events handler. A direct SDK call bypasses
# the outbox, schema validation, DLQ, and distributed tracing integration.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0
ALLOWED="cmd/server/main.go"

# SNS SDK import.
while IFS= read -r f; do
  [[ "$f" == "$ALLOWED" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  echo "::error file=$f::aws-sdk-go-v2/service/sns imported outside $ALLOWED. Publish via port.EventPublisher through the outbox; consume via platform-events SQS handler."
  fail=1
done < <(grep -rlE '"github\.com/aws/aws-sdk-go-v2/service/sns(/[a-z0-9]+)?"' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# SQS SDK import.
while IFS= read -r f; do
  [[ "$f" == "$ALLOWED" ]] && continue
  [[ "$f" == *"_test.go" ]] && continue
  echo "::error file=$f::aws-sdk-go-v2/service/sqs imported outside $ALLOWED. Consume via platform-events SQS handler registered in cmd/server/main.go."
  fail=1
done < <(grep -rlE '"github\.com/aws/aws-sdk-go-v2/service/sqs(/[a-z0-9]+)?"' --include='*.go' cmd/ internal/ 2>/dev/null || true)

# Hand-built events.Envelope{} — must never be constructed outside platform-events internals.
hits=$(grep -rEn '\bevents\.Envelope{' --include='*.go' cmd/ internal/ 2>/dev/null | grep -v '_test.go' || true)
if [ -n "$hits" ]; then
  echo "::error::Hand-built events.Envelope{} detected. Events are wrapped by platform-events automatically."
  echo "$hits"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  events bypass check passed"
