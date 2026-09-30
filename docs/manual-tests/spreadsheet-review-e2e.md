# Spreadsheet review e2e

Manual smoke for the spreadsheet-review workflow. This validates:

- seeded table render and blank-table empty state
- explicit submit and cancel through one room
- canonical sort/filter/search state in the submit payload
- row selection and bulk action capture
- named saved views that reopen from room state
- host-local refresh recovery for unsent state
- CSV export metadata persistence on the room
- same-room reopen from the latest submitted spreadsheet-review state

## 1. Boot Tangent

```bash
cd tangent  # your clone of the repo
./tangent
```

Watch for a log line like:

```text
level=INFO msg="spreadsheet-review room created" room=<roomID> envelope=<id>
```

The room **URL is not logged** — the tool logs only the room id (ADR 0002 §8:
an assembled URL in a log line is a locator that outlives the log). Build it
yourself as `http://127.0.0.1:7842/r/<roomID>`, or read it from the tool
response, which carries the room URL back to the caller.

## 2. Register Tangent in Claude Code

```bash
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp
```

Fallback:

```bash
claude mcp add --transport sse tangent http://127.0.0.1:7842/sse
```

## 3. Trigger the first spreadsheet-review turn

Ask Claude Code to call `tangent.spreadsheet-review` with a small table.

Example prompt:

> Use `tangent.spreadsheet-review` to open a table called `table-1` with
> two rows and one bulk action, then wait for my submit/cancel response.

## 4. Refresh recovery before submit

Open the `/r/<roomID>` URL. Confirm:

- the seeded rows render as a table
- the footer explains that draft changes recover in this browser until submit or cancel
- search, filters, selected rows, notes, and saved views can all change without immediately returning an MCP response

Refresh the page before clicking `Submit review`. Confirm:

- the same spreadsheet-review envelope reopens
- unsent search/filter/selection/notes state recovers from this browser
- another room or another `table_id` does not pick up this draft

## 5. Submit once

In the room:

- type a search query
- optionally add a filter
- select one or more rows
- pick a bulk action if one was provided
- save a named view
- click `Export CSV`
- click `Submit review`

Claude should receive a response shaped like:

```json
{
  "v": 1,
  "envelopeId": "<id>",
  "kind": "data",
  "status": "submitted",
  "payload": {
    "table_id": "table-1",
    "selected_row_ids": ["row-1"],
    "selected_rows": [
      { "id": "row-1", "name": "Alpha", "status": "open" }
    ],
    "query_state": {
      "search": "Alpha"
    },
    "notes": "first pass",
    "action_id": "approve"
  }
}
```

Confirm the response payload does not echo `saved_views` or `export_refs`
back to the agent, even though both are persisted to the room.

## 6. Reopen the same room

Ask Claude Code to call `tangent.spreadsheet-review` again with
`meta.roomID` pointing at the same room.

Confirm the reopened spreadsheet starts from the last submitted room
state, not the stale seed envelope:

- the prior notes are visible
- the saved search/filter state is restored
- the prior selected rows are restored
- saved views are listed
- export metadata remains associated with the room

## 7. Verify persisted room state

Have Claude call `tangent.session_get` for the room and confirm:

- `spreadsheet_review.table_id` matches the workflow table
- `spreadsheet_review.query_state` matches the latest submit
- `spreadsheet_review.notes` matches the latest submit
- `spreadsheet_review.action_id` matches the selected bulk action
- `spreadsheet_review.selected_row_ids` matches the submitted selection
- `spreadsheet_review.saved_views` contains the named view(s)
- `spreadsheet_review.export_refs` contains CSV export metadata
- `envelopes_history` includes the submitted spreadsheet-review turn

## Cancel check

Trigger one more `tangent.spreadsheet-review` turn in the same room and
click `Cancel`. Confirm Claude receives a cancelled response and the
persisted spreadsheet-review state remains unchanged.

## Mock equivalent

```bash
node scripts/spreadsheet-review-mock-call.mjs
```

That script drives submit -> reopen -> cancel against one room and
asserts the persisted spreadsheet-review projection plus room history.
