---
name: swag-regen
description: Regenerates the OpenAPI/Swagger docs from handler annotations by running `make swag`, then shows a git diff of docs/swagger/ so you can verify the change before committing. Use after adding, renaming, or removing any HTTP route, request type, or response type.
disable-model-invocation: true
---

## What this skill does

Runs `make swag` to regenerate `docs/swagger/` from the swag annotations in
`cmd/server/swagger_info.go` and `internal/adapter/inbound/http/*.go`.

Then shows the diff so you can confirm that only the intended routes/types
changed before committing.

## Steps

```bash
# 1. Regenerate
make swag

# 2. Show what changed
git diff docs/swagger/
```

## Reminder

Commit the updated `docs/swagger/` files together with the handler changes —
CI will fail the `swag-check` step if the generated docs are stale.

If `make swag` exits non-zero (e.g. a missing annotation tag), fix the annotation
in the handler file, then re-run this skill.
