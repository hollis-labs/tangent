# Progress-panel e2e

Manual verification recipe for Tangent progress-panel turns.

## Goal

Verify that Tangent can:

1. open a `tangent.progress-panel` room,
2. accept an explicit update,
3. reopen with the latest accepted progress state,
4. recover browser-local operator context,
5. expose concise summary/export inspection.

## Prerequisites

- Tangent built from the current branch (`make build`).
- `tangent` running locally on `:7842`.
- A browser available for the room URL.

## Open a progress panel

Create a room:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"progress-e2e"}}}'
```

Call `tangent.progress-panel` using the returned `roomID`:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.progress-panel","arguments":{"envelope":{"v":1,"id":"progress-e2e-1","type":"tangent.progress-panel","meta":{"roomID":"<room-id>"},"data":{"panel_id":"panel-e2e","items":[{"item_id":"scan","label":"Scan repo","status":"running"},{"item_id":"summary","label":"Write summary","status":"queued"}],"summary":{"current_status":"running","headline":"1 running"}}}}}'
```

Open `http://127.0.0.1:7842/r/<room-id>` in the browser.

## Browser checks

- Confirm the room renders the two seeded items.
- Confirm the summary card renders `1 running`.
- Switch between `Timeline`, `Checkpoints`, and `Logs`.
- In `Logs`, click an update row after a submit and verify the JSON
  detail pane opens.

## Submit an update

- In the right-hand control pane, choose item `Write summary`.
- Set status to `running`.
- Enter note `Started drafting summary.`.
- Enter checkpoint label `Summary started`.
- Submit.

Expected result:

- The tool call returns `status: submitted`.
- Response payload includes `outcome: accepted`.
- Response payload includes a stable `update_id`.
- Response payload includes a stable `checkpoint_id`.

## Reopen behavior

Trigger another `tangent.progress-panel` call for the same `roomID` and
`panel_id`, then open the room again.

Expected result:

- `Write summary` reopens with status `running`.
- Timeline includes the accepted update.
- Checkpoints include `Summary started`.
- `session_get.progress_panel.summary.last_checkpoint_label` is
  `Summary started`.

## Recovery behavior

- Change the active tab to `Logs`.
- Filter to one item or update kind.
- Type an unsent note and optional checkpoint label.
- Refresh the browser tab before submitting.

Expected result:

- The room reopens on the same tab/filter selection.
- The unsent note/checkpoint draft is restored locally.
- Canonical room state is unchanged until an explicit submit occurs.

## Export snapshot

- Click `Copy snapshot`.

Expected result:

- The UI shows a success or clipboard-fallback message.
- The export payload includes:
  - `panel_id`
  - `summary`
  - `items`
  - `latest_checkpoint`
  - `update_count`

## `session_get` inspection

Run:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<room-id>"}}}' \
  | jq -r '.result.content[0].text' | jq '.progress_panel'
```

Expected result:

- `items` reflects the latest accepted statuses.
- `updates` is append-only.
- `checkpoints` includes the accepted milestone.
- `summary.current_status` matches the last accepted status.
- `summary.last_checkpoint_label` matches the accepted checkpoint.
- `summary.completion_result` is set only after a terminal status
  update.
