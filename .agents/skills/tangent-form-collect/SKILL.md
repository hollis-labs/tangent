---
name: tangent-form-collect
description: Use when launching Tangent's `tangent_form_collect` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Form Collect

Use this skill when you need to launch `tangent_form_collect`.

## Copy/paste example

```text
Use `tangent_form_collect` with one required text field, one priority selector, one optional notify checkbox, and one submit action. Wait for my submit/cancel response.
```

## Schema shape (the tool rejects anything else)

Fields are keyed by `id` (not `name`) and each needs `id`, `type` and `label`. Group fields in
`sections`; a section is `{id, title, fields: [...]}` and **holds its own fields**. Do not give a flat
field a `section` key pointing at a section. Use `schema.fields` for an ungrouped form.

```json
{
  "envelope": {
    "v": 1, "id": "release-held-tasks", "type": "tangent.form-collect", "title": "Release held tasks",
    "data": {
      "form_id": "release-held-tasks",
      "intent": "Start each held task as designed, change it, or keep it held.",
      "schema": {
        "sections": [{
          "id": "policy", "title": "Policy decisions",
          "fields": [
            { "id": "push", "type": "select", "label": "Agents and push", "default": "ask",
              "options": [{ "value": "ask", "label": "Ask first" }, { "value": "branch", "label": "Own branch only" }] },
            { "id": "push_note", "type": "textarea", "label": "Comment", "required": false }
          ]
        }]
      }
    }
  },
  "completion": { "mode": "async", "reply_to": "msg://agent/agent-mux/agt_YOURS" }
}
```

## Notes

- `action_id` in the submitted payload is optional.
- If you need a literal schema payload, use the full manual recipe.

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

- Manual recipe: `docs/manual-tests/form-collect-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/form_collect_schema.go`
- Handler: registered on the dispatcher by `RegisterFormCollectOnDispatcher` in `internal/mcp/triage_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
