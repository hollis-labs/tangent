---
name: tangent-approval-queue
description: Use when launching Tangent's `tangent.approval-queue` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Approval Queue

Use this skill when you need to launch `tangent.approval-queue`.

## Copy/paste example

```text
Use `tangent.approval-queue` with two review items and let me make one decision on one queued item. Wait for my submit/cancel response.
```

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async" } }
```

It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Omitting `completion` keeps the inline
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

- Manual recipe: `docs/manual-tests/approval-queue-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/approval_queue_schema.go`
- Handler: `internal/mcp/approval_queue_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
