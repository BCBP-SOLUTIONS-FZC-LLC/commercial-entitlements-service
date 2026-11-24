#!/usr/bin/env bash
# Rejects direct database/sql imports, bare pgxpool/pgx construction, and
# improper system-pool configuration in production Go source.
# All DB connections must go through platform-pgcommon (pgcommon.NewPool,
# RunInTx, ConfigFromEnv). Mirrors the guard in .claude/hooks/guard-invariants.sh
# so the same invariant is enforced both locally (pre-edit) and in CI.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

# 1. database/sql import — never use in production Go.
hits=$(grep -rn '"database/sql"' --include='*.go' cmd/ internal/ 2>/dev/null | grep -v '_test.go' || true)
if [ -n "$hits" ]; then
  echo "::error::database/sql import detected in production code. Use pgx through pgcommon (internal/adapter/outbound/postgres)."
  echo "$hits"
  fail=1
fi

# 2. Direct pgxpool construction — must go through pgcommon.NewPool.
hits=$(grep -rEn '\bpgxpool\.(New|NewWithConfig|Connect)\b' --include='*.go' cmd/ internal/ 2>/dev/null | grep -v '_test.go' || true)
if [ -n "$hits" ]; then
  echo "::error::Direct pgxpool construction detected. Use pgcommon.NewPool (internal/adapter/outbound/postgres/db.go)."
  echo "$hits"
  fail=1
fi

# 3. Direct pgx.Connect/ConnectConfig — must go through pgcommon.NewPool.
hits=$(grep -rEn '\bpgx\.Connect(Config)?\b' --include='*.go' cmd/ internal/ 2>/dev/null | grep -v '_test.go' || true)
if [ -n "$hits" ]; then
  echo "::error::Direct pgx.Connect detected. Use pgcommon.NewPool."
  echo "$hits"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "  ✔  database access check passed"
