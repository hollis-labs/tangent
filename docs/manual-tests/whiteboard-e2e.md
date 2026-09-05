# Whiteboard e2e

Manual smoke for the whiteboard submit loop. This validates:

- host-local autosave for in-progress edits while the envelope is open
- refresh recovery scoped to the active room + board
- PNG export from the room footer
- explicit `Submit board` vs `Cancel`
- server-issued `revision_id` on submit
- same-room reopen from the latest persisted snapshot
- artifact-backed reference-image refs surviving room reload
- append-only whiteboard revision history in `tangent.session_get`
- prior-revision reopen + explicit `Continue from here` lineage

## 1. Boot Tangent

```bash
cd ~/dev/hollis-labs/apps/tangent
./tangent
```

Watch for a log line like:

```text
level=INFO msg="whiteboard room created" room=<roomID> envelope=<id>
```

The room **URL is not logged** — the tool logs only the room id (ADR 0002 §8:
an assembled URL in a log line is a locator that outlives the log). Build it
yourself as `http://127.0.0.1:7842/r/<roomID>`, or read it from the tool
response, which carries the room URL back to the caller.

## 2. Register Tangent in Claude Code

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

Fallback:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

## 3. Trigger the first whiteboard turn

Ask Claude Code to call `tangent.whiteboard` with a small starter scene.

Example prompt:

> Use `tangent.whiteboard` to open a board called `board-1` so I can sketch
> a simple layout, then wait for my submit/cancel response.

## 4. Refresh recovery before submit

Open the `/r/<roomID>` URL. Confirm:

- the board renders
- the footer explains that drafts autosave locally until submit/cancel
- editing the board changes the canvas and notes without returning an MCP response yet

Refresh the page before clicking `Submit board`. Confirm:

- the same whiteboard envelope reopens
- the unsent canvas edits and notes recover from this browser
- another room or another `board_id` does not pick up this draft
- if the seeded board included artifact-backed `reference_images`, they
  still render after refresh
- if an artifact ref is stale or missing, the whiteboard shows a warning
  instead of failing the entire board

## 5. Submit once

Open the `/r/<roomID>` URL. Confirm:

- the board renders
- clicking `Export PNG` downloads a `.png`
- editing the board and clicking `Submit board` returns a `data` response

Claude should receive a response shaped like:

```json
{
  "v": 1,
  "envelopeId": "<id>",
  "kind": "data",
  "status": "submitted",
  "payload": {
    "board_id": "board-1",
    "revision_id": "board-1-r1",
    "scene": { "...": "full snapshot" },
    "assets": [],
    "notes": "revision one",
    "export_refs": [
      {
        "kind": "png",
        "name": "board-1-draft.png"
      }
    ],
    "selection_summary": {
      "count": 1
    }
  }
}
```

## 6. Reopen the same room

Ask Claude Code to call `tangent.whiteboard` again with `meta.roomID`
pointing at the same room.

Confirm the reopened board starts from the submitted snapshot, not the
original seed. The latest revision badge and notes should reflect the
previous submit, and the revision browser should list the prior saved
revision(s).

Submit again and verify Claude receives a new `revision_id`.

## 7. Continue from revision N

Inside the revision browser:

- click `Reopen snapshot` on the older revision and confirm the board
  loads that scene while submit is disabled
- click `Continue from here` on that older revision
- confirm the board becomes submittable again and shows a continuation
  indicator for the selected prior revision
- submit once more

Claude should now receive a payload that includes:

```json
{
  "payload": {
    "revision_id": "board-1-r3",
    "continued_from_revision_id": "board-1-r1"
  }
}
```

## 8. Verify persisted room state

Have Claude call `tangent.session_get` for the room and confirm:

- `whiteboard.notes` matches the latest submit
- `whiteboard.export_refs` contains the latest PNG export metadata
- `whiteboard.assets` retains artifact-backed reference-image refs
- `whiteboard.revision_history` has the expected entries
- the continued revision carries `continued_from_revision_id`
- `envelopes_history` includes both whiteboard turns with their
  normalized submit payloads
- reopening after the second submit starts from the canonical submitted
  snapshot, not the stale pre-submit local draft

## Cancel check

Trigger one more `tangent.whiteboard` turn in the same room and click
`Cancel`. Confirm Claude receives a cancelled response and
`whiteboard.revision_history` does not grow.

## Mock equivalent

```bash
node scripts/whiteboard-mock-call.mjs
```

That script drives submit -> reopen -> submit against one room and
asserts the persisted latest snapshot plus revision-aware history.
