---
name: tangent-form-collect
description: Use when launching Tangent's `tangent.form-collect` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Form Collect

Use this skill when you need to launch `tangent.form-collect`.

## Copy/paste example

```text
Use `tangent.form-collect` with one required text field, one priority selector, one optional notify checkbox, and one submit action. Wait for my submit/cancel response.
```

## Notes

- `action_id` in the submitted payload is optional.
- If you need a literal schema payload, use the full manual recipe.

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async" } }
```

It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Omitting `completion` keeps the v0.12
default: wait up to 45 seconds, return the normal response if the operator
answers in time, and otherwise return that same pending receipt — never an
error, and never a cancellation.

Recover a result three equivalent ways: `tangent.interaction_get`,
`tangent.interaction_await`, or by retrying this call with the identical
envelope id and payload. All three return the same immutable result. A retry
with a *changed* payload is an `IDEMPOTENCY_CONFLICT`; use a new envelope id.

Acknowledge with `tangent.interaction_acknowledge` when your side has committed
to the result. Reading a result does not acknowledge it.

Full contract: `docs/room-workflow-completion.md`

## Pointers

- Manual recipe: `docs/manual-tests/form-collect-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/form_collect_schema.go`
- Handler: `internal/mcp/form_collect_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
