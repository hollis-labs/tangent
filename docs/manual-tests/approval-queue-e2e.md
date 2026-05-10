# Approval-queue e2e

Manual smoke for the approval-queue workflow. This validates:

- explicit accept/reject/defer decisions with one submit boundary
- same-room reopen from the latest submitted approval-queue state
- evidence-pane rendering and keyboard navigation
- local refresh recovery for unsent comments
- durable `approval_queue` projection in `tangent.session_get`

## 1. Start Tangent

```bash
make build
./tangent
```

## 2. Trigger the first approval-queue turn

Ask Claude Code to call `tangent.approval_queue` with two items and one
evidence pane per item.

In the browser:

- accept item 1
- set action ID `merge`
- defer item 2 with reason `window`
- click `Export audit`
- click `Submit`

Claude should receive a structured `data` response with `queue_id`,
`current_index`, normalized `decisions`, and any export metadata.

## 3. Reopen the same room

Ask Claude Code to call `tangent.approval_queue` again with the same
`meta.roomID` and intentionally stale notes/current index.

Expected:

- the queue reopens on the previously active item
- prior decisions and defer reason are restored
- notes/export refs come from persisted room state, not the stale input

Click `Cancel` on this second turn.

## 4. Verify persisted room state

Ask Claude Code to call `tangent.session_get` for the same room and
inspect `approval_queue`.

Expected:

- `approval_queue.queue_id` matches the original queue
- `approval_queue.current_index` matches the last submitted position
- `approval_queue.decisions` contains the submitted item outcomes
- `approval_queue.export_refs` includes the exported audit metadata
- `envelopes_history` includes both turns with the second one cancelled

## 5. Optional script-only check

```bash
node scripts/approval-queue-mock-call.mjs
```
