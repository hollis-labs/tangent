---
name: tangent-wizard
description: Use when launching Tangent's `tangent_wizard` workflow. Provides a minimal valid copy/paste payload and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Wizard

Use this skill when you need to launch `tangent_wizard`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "wizard_id": "wizard-1",
  "title": "Release wizard",
  "current_step_id": "step-scope",
  "steps": [
    {
      "step_id": "step-scope",
      "title": "Scope",
      "kind": "form",
      "fields": {
        "fields": [
          {
            "field_id": "scope",
            "label": "Scope",
            "kind": "textarea"
          }
        ]
      },
      "branches": [
        {
          "branch_id": "review",
          "label": "Review",
          "target_step_id": "step-review"
        }
      ]
    },
    {
      "step_id": "step-review",
      "title": "Review",
      "kind": "review"
    }
  ]
}
```

## Notes

- `current_step_id` must match a real `steps[].step_id`.
- Every branch `target_step_id` must point at a real step.
- Use the exact payload above unless you intentionally need a different flow.

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

- Manual recipe: `docs/manual-tests/wizard-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/wizard_schema.go`
- Handler: `internal/mcp/wizard_handler.go`
- State validator: `internal/room/wizard_state.go`
- Completion contract: `docs/room-workflow-completion.md`
