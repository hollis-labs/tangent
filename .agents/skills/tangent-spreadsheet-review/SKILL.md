---
name: tangent-spreadsheet-review
description: Use when launching Tangent's `tangent_spreadsheet_review` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Spreadsheet Review

Use this skill when you need to launch `tangent_spreadsheet_review`.

## Copy/paste example

```text
Use `tangent_spreadsheet_review` to open a table called `table-1` with two rows and one bulk action, then wait for my submit/cancel response.
```

## Notes

- Seed real `columns`, `rows`, and usually `row_actions`.
- For deterministic smoke runs, copy the exact payload from the manual recipe.

## Completion and recovery

Prefer async so the call returns as soon as the request is durable:

```json
{ "completion": { "mode": "async", "reply_to": "msg://agent/agent-mux/agt_YOURS" } }
```

`reply_to` is **required**: your own Tether URN (`tether_whoami` returns it). When
the operator answers, or the item ends without an answer, Tangent mails the
outcome there. It arrives on your next turn, or at once if you are idle, so
send the request with `mode: "async"` and carry on; you do not need to poll or
await. The mail's `tangent.response` holds the operator's answer. The
polling tools below remain as recovery.


It returns a successful receipt with `"status": "pending"`, a durable handle,
and the room URL to hand a human. Without `mode: "async"` the call keeps the inline
default: wait up to 45 seconds, return the normal response if the operator
answers in time, and otherwise return that same pending receipt — never an
error, and never a cancellation.

Recover a result three equivalent ways: `tangent_interaction_get`,
`tangent_interaction_await`, or by retrying this call with the identical
envelope id and payload. All three return the same immutable result. A retry
with a *changed* payload is an `IDEMPOTENCY_CONFLICT`; use a new envelope id.

Acknowledge with `tangent_interaction_ack` when your side has committed
to the result. Reading a result does not acknowledge it.

Full contract: `docs/room-workflow-completion.md`

## Pointers

- Manual recipe: `docs/manual-tests/spreadsheet-review-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Envelope extension: `internal/envelope/extensions/spreadsheet_review.go`
- Data schema: `internal/envelope/extensions/packages/tangent.review/spreadsheet-review/request.schema.json`
- Handler: `internal/mcp/spreadsheet_review_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
