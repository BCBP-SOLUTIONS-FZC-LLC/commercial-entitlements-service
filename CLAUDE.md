# commercial-entitlements

Full context lives in [`.claude/CLAUDE.md`](.claude/CLAUDE.md).

Detail files:

| File | Covers |
|------|--------|
| [`.claude/architecture.md`](.claude/architecture.md) | Package tree, shared library integration, arch boundary rules |
| [`.claude/database-schema.md`](.claude/database-schema.md) | Tables, RLS, triggers, migrations, concurrency invariants |
| [`.claude/api-caching-events.md`](.claude/api-caching-events.md) | Routes, Valkey keys/TTLs, produced & consumed events |
| [`.claude/key-files.md`](.claude/key-files.md) | Per-file descriptions for every critical Go file |
| [`.claude/operations.md`](.claude/operations.md) | Metrics, reconciler jobs, Helm deployment, env vars |
| [`.claude/request-flows.md`](.claude/request-flows.md) | Step-by-step flows: recompute, drawdown, consumers, overrides |
