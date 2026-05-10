# Wizard E2E

## Goal

Verify the room-backed wizard workflow can reopen accepted progress, recover unsent local draft state, and complete through an explicit final submit.

## Setup

1. Run `make dev`.
2. Open a browser room URL produced by a `tangent.wizard` MCP call.
3. Use a wizard definition with at least:
   - one form-like step with a text field
   - one branch option leading to a review step
   - a final review/completion step

## Minimal launch payload

`current_step_id` must match a real `step_id`, and every branch
`target_step_id` must point at another real step. This minimal payload
is valid:

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

If you want to launch it directly over MCP, use:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.wizard","arguments":{"envelope":{"v":1,"id":"wizard-1","type":"tangent.wizard","data":{"wizard_id":"wizard-1","title":"Release wizard","current_step_id":"step-scope","steps":[{"step_id":"step-scope","title":"Scope","kind":"form","fields":{"fields":[{"field_id":"scope","label":"Scope","kind":"textarea"}]},"branches":[{"branch_id":"review","label":"Review","target_step_id":"step-review"}]},{"step_id":"step-review","title":"Review","kind":"review"}]}}}}}'
```

## Flow

1. Enter text on the first step and click `Save Progress`.
   Expect:
   - the MCP call resolves with `status: "partial"`
   - `session_get.wizard.progress` contains the saved response
   - `session_get.wizard.current_step_id` stays on the same step

2. Choose a branch and click `Submit Step`.
   Expect:
   - the MCP call resolves with `status: "partial"`
   - `session_get.wizard.branch_selections` includes the chosen branch
   - `session_get.wizard.current_step_id` advances to the selected target step

3. Refresh the browser before submitting a second edit.
   Expect:
   - the host shows the recovered local draft banner when unsent local state exists
   - accepted room-backed progress still matches `session_get.wizard`

4. Use `Back` and `Next Step`.
   Expect:
   - both emit partial updates
   - reopening the room shows the last accepted `current_step_id`

5. Complete the final step with `Complete Wizard`.
   Expect:
   - the MCP call resolves with `status: "submitted"`
   - `session_get.wizard.summary.status == "completed"`
   - `session_get.wizard.summary.completed_at` is set

## Notes

- Local draft recovery is browser-only and must not overwrite canonical room state until the user submits.
- `session_get.wizard` is the authority for reopened wizard state.
