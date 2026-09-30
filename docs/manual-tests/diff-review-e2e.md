# Diff Review E2E

Manual room-backed smoke test for `tangent.diff-review`.

## Setup

Start Tangent:

```bash
cd tangent  # your clone of the repo
make build
./tangent
```

Create a room and invoke the workflow:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"diff-review-manual"}}}'
```

Copy the `roomID`, then send:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.diff-review","arguments":{"envelope":{"v":1,"id":"diff-manual-1","type":"tangent.diff-review","title":"Diff review","data":{"review_id":"review-manual-1","files":[{"id":"file-1","path":"pkg/app.go","summary":"Tighten validation","hunks":[{"id":"hunk-1","header":"@@ -1,3 +1,4 @@","before":"return nil","after":"return validate()"}]},{"id":"file-2","path":"ui/view.tsx","summary":"Polish spacing","hunks":[{"id":"hunk-2","header":"@@ -2,3 +2,4 @@","before":"gap-2","after":"gap-3"}]}],"before_ref":{"artifact_id":"artifact-before","name":"before.patch"},"after_ref":{"artifact_id":"artifact-after","name":"after.patch"}},"meta":{"roomID":"<room-id>"}}}}}'
```

Open `http://127.0.0.1:7842/r/<room-id>`.

## Expected behavior

- The room shows a file list, before/after artifact labels, and one active file panel.
- Per-hunk decisions support `Accept`, `Request Changes`, and `Comment`.
- `Accept File` / `Request Changes` applies the decision across the current file.
- `Export Summary` downloads a markdown summary and records lightweight export metadata.
- Refresh keeps the pending room alive and restores unsent draft state from browser storage.

## Reopen check

After submit, re-run the workflow with the same `review_id` and `roomID` but stale file text:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.diff-review","arguments":{"envelope":{"v":1,"id":"diff-manual-2","type":"tangent.diff-review","data":{"review_id":"review-manual-1","files":[{"id":"file-1","path":"stale"}]},"meta":{"roomID":"<room-id>"}}}}}'
```

Expected:

- The room reopens with the persisted canonical files rather than the stale seed.
- Prior decisions, comments, `current_file`, and export refs are visible again.
- `tangent.session_get` returns a `diff_review` projection with the same persisted review state.
