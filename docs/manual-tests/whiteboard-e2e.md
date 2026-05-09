# Whiteboard e2e

Manual smoke for the v0.4 whiteboard submit loop. This validates:

- explicit `Submit board` vs `Cancel`
- server-issued `revision_id` on submit
- same-room reopen from the latest persisted snapshot
- append-only whiteboard revision history in `tangent.session_get`

## 1. Boot Tangent

```bash
cd ~/Projects-apps/tangent
./tangent
```

Watch for a log line like:

```text
level=INFO msg="whiteboard room created" room=<roomID> url=http://127.0.0.1:7842/r/<roomID> envelope=<id>
```

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

## 4. Submit once

Open the `/r/<roomID>` URL. Confirm:

- the board renders
- the footer explains that submit saves a revision and cancel does not
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
    "selection_summary": {
      "count": 1
    }
  }
}
```

## 5. Reopen the same room

Ask Claude Code to call `tangent.whiteboard` again with `meta.roomID`
pointing at the same room.

Confirm the reopened board starts from the submitted snapshot, not the
original seed. The latest revision badge and notes should reflect the
previous submit.

Submit again and verify Claude receives a new `revision_id`.

## 6. Verify persisted room state

Have Claude call `tangent.session_get` for the room and confirm:

- `whiteboard.notes` matches the second submit
- `whiteboard.revision_history` has two entries
- `envelopes_history` includes both whiteboard turns with their
  normalized submit payloads

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
