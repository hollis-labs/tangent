---
name: tangent-progress-panel
description: Use when launching Tangent's `tangent_progress_panel` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent Progress Panel

Use this skill when you need to launch `tangent_progress_panel`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "panel_id": "panel-e2e",
  "items": [
    {
      "item_id": "scan",
      "label": "Scan repo",
      "status": "running"
    },
    {
      "item_id": "summary",
      "label": "Write summary",
      "status": "queued"
    }
  ],
  "summary": {
    "current_status": "running",
    "headline": "1 running"
  }
}
```

## Notes

- Seed `items` explicitly or the UI has nothing actionable.
- `updates`, `checkpoints`, and `summary` are optional but useful.
- Item `status` is exactly one of `queued`, `running`, `blocked`, `completed`, `failed`. Anything else
  (for example `done`) is refused with `host-error: room: invalid progress panel item`.
- Fields: `items` `[{item_id, label, status, detail}]`, `updates` `[{update_id, kind, item_id, status,
  summary}]`, `summary` `{current_status, headline, detail}`. `detail` and an update's `summary`
  render as markdown; `headline` is one line and does not.

## Moving the panel forward

To advance a panel you already opened, call the tool again with the **same** `panel_id`, set
`meta.roomID` to the `room_id` from the first call, use a **new** envelope `id`, and send the whole new
`items`, `updates` and `summary`. The panel advances in the same room while the operator watches. Each
call creates its own interaction (the Inbox shows one row per call for the room). Reusing the first
envelope id with a changed payload is an `IDEMPOTENCY_CONFLICT`.

The operator can also send an update from the control pane (stage, status, note, checkpoint label);
read it back with `tangent_interaction_await` or `tangent_interaction_get`.

## Through the Tether gateway

- Use `{ "completion": { "mode": "async" } }`. The result's `handle.url` is a loopback address; give the
  operator `https://tangent.nanite.cloud/r/<room_id>`. The panel also appears in the Inbox under
  **Structured interactions**.
- The operator must elevate once (token entry, any Approve or Reply action) before answering, and reload
  the room after elevating.
- Room workflows sent through the gateway were invisible to the operator before 2026-10-25
  (CW-20261025-0002); they are visible from Tangent `main` 4a03f78.

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

Recover a result three equivalent ways: `tangent_interaction_get`,
`tangent_interaction_await`, or by retrying this call with the identical
envelope id and payload. All three return the same immutable result. A retry
with a *changed* payload is an `IDEMPOTENCY_CONFLICT`; use a new envelope id.

Acknowledge with `tangent_interaction_ack` when your side has committed
to the result. Reading a result does not acknowledge it.

Full contract: `docs/room-workflow-completion.md`

## Pointers

- Manual recipe: `docs/manual-tests/progress-panel-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/progress_panel_schema.go`
- Handler: `internal/mcp/progress_panel_handler.go`
- Completion contract: `docs/room-workflow-completion.md`
