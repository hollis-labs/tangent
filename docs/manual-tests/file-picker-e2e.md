# File Picker E2E

Manual room-backed smoke test for `tangent.file-picker`.

## Goal

Verify that Tangent can:

- open a room-backed file-picker workflow
- preserve the latest accepted selection on reopen
- keep unsent browser-local draft state separate from canonical submitted room state
- expose artifact-ref handoff metadata through `tangent.session_get`

## Setup

Start Tangent:

```bash
make build
./tangent
```

Create a room:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"file-picker-manual"}}}'
```

Copy the returned `roomID`.

## First picker turn

Dispatch a file-picker envelope:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.file-picker","arguments":{"envelope":{"v":1,"id":"picker-manual-1","type":"tangent.file-picker","title":"File picker","data":{"picker_id":"picker-manual-1","browse_roots":[{"root_id":"workspace","label":"Workspace","path":"/tmp/workspace"}],"files":[{"artifact_id":"artifact-spec","name":"spec.md","uri":"artifact://artifact-spec","mime_type":"text/markdown","root_id":"workspace","relative_path":"docs/spec.md"},{"artifact_id":"artifact-readme","name":"README.md","uri":"artifact://artifact-readme","mime_type":"text/markdown","root_id":"workspace","relative_path":"README.md"}]},"meta":{"roomID":"<room-id>"}}}}}'
```

Open `http://127.0.0.1:7842/r/<room-id>` in a browser.

In the UI:

1. Select `spec.md`.
2. Change the search box to `spec`.
3. Submit the selection.

Confirm the MCP response includes:

- `payload.outcome = "accepted"`
- `payload.selection_revision_id`
- `payload.selected_refs[0].artifact_id = "artifact-spec"`

## Reopen and draft recovery

Dispatch a reopen turn:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.file-picker","arguments":{"envelope":{"v":1,"id":"picker-manual-2","type":"tangent.file-picker","data":{"picker_id":"picker-manual-1","browse_roots":[{"root_id":"workspace","label":"Workspace","path":"/tmp/workspace"}]},"meta":{"roomID":"<room-id>"}}}}}'
```

In the reopened browser UI:

1. Confirm `spec.md` is still selected.
2. Confirm the search state is restored.
3. Change the staged selection without submitting.
4. Refresh the browser tab.
5. Confirm the unsent draft selection recovers locally.
6. Confirm the accepted room-backed selection has not changed until submit.

## Session projection

Inspect room state:

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<room-id>"}}}' | jq -r '.result.content[0].text' | jq '.file_picker'
```

Confirm the projection includes:

- `selected_refs`
- `selection_revisions`
- `submission_summary`
- `handoff.artifact_refs`

## Expected constraints

- Selections must use durable `artifact://...` refs.
- Browser-local draft recovery must not overwrite canonical room state until submit.
- Tangent does not browse remote filesystems or cloud storage in this phase.
