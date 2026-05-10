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
