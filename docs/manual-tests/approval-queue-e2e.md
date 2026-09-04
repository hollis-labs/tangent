# Approval-queue e2e

Manual smoke for the approval-queue workflow. This validates:

- explicit accept/reject/defer decisions with one submit boundary
- the defer-reason affordance: visibly required while an item is deferred,
  and named next to the disabled Submit when it is outstanding
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

Ask Claude Code to call `tangent.approval-queue` with two items and one
evidence pane per item.

In the browser:

- accept item 1
- set action ID `merge`
- defer item 2, and **leave the defer reason empty**
- navigate back to item 1

Expected at this point — this is the regression the workflow shipped with,
so check it every time:

- the `Defer reason` label on item 2 carries a `required` badge while the
  decision is Defer, and drops it if the decision changes
- item 2's row in the queue list is flagged `needs a defer reason`, even
  though item 1's pane is the one on screen
- the header count reads `2 items · 0 unresolved · 1 awaiting a defer reason`
- Submit is disabled, and the text beside it reads
  `Submit is disabled: "…" is deferred and still needs a defer reason.`
- clicking `Go to defer reason` switches to item 2, scrolls the field into
  view, and focuses it
- the reviewer comment is labelled `Reviewer comment`, and its help text says
  it does not stand in for the defer reason

Then finish the turn:

- fill the defer reason with `window`
- confirm Submit becomes enabled and the explanation disappears
- click `Export audit`
- click `Submit`

Claude should receive a structured `data` response with `queue_id`,
`current_index`, normalized `decisions`, and any export metadata.

## 3. Reopen the same room

Ask Claude Code to call `tangent.approval-queue` again with the same
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
