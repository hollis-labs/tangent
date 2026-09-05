# Durable HITL inbox e2e

Use this recipe to verify the persistent operator-owned `/hitl` surface. It is
separate from the room-backed `tangent.approval-queue` workflow and does not
require an active room.

## Automated joined gate and browser limitation

`TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart` is the automated
cross-layer gate. It starts the real Tangent HTTP mux on a loopback ephemeral
port, enters through stateless Streamable HTTP MCP, checks durable SQLite and
the REST/SSE operator projection, mounts the production React `App` and its
real `/hitl` route
against that live server through `ui/src/test-drivers/hitl-inbox-e2e.tsx`, then
restarts the server and retrieves the terminal results through fresh MCP Get
and Await calls. Run it with the supported Node baseline:

```bash
mise --no-config exec node@22.12.0 -- make build-ui
mise --no-config exec node@22.12.0 -- \
  go test -race ./internal/server \
  -run '^TestHITLInboxJoinsPublicMCPBrowserProjectionAndRestart$' -count=1 -v
```

This gate runs headless by design: **there is no browser in CI**, and no
standalone browser fallback is used. That is a standing property of this
repository's verification, not a fault of one session's environment. The
joined gate instead uses
the repository's source-linked happy-dom/vite-node driver, the same class of
production-module driver used by the room lifecycle regressions. It verifies
real React behavior, accessibility roles, focus/keyboard handling, live SSE,
and HTTP effects, but it is not pixel-level visual or OS-notification testing.
Keep the manual checks below for responsive layout, actual browser rendering,
visible focus/contrast, and confirmation that no native notification appears.

## Start from an isolated database

```bash
make build
TANGENT_DB_PATH=/tmp/tangent-hitl-e2e.db TANGENT_HTTP_PORT=7843 ./tangent
```

Open `http://127.0.0.1:7843/hitl`. Before enqueueing anything, verify the page
shows a useful empty pending stream and no room is created.

## Enqueue two callers into the global FIFO

Send two stateless MCP calls, changing the JSON-RPC `id`, source, and
`idempotency_key` on the second call:

```bash
curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.hitl_enqueue","arguments":{"contract_version":"1.0","kind":"approval","idempotency_key":"hitl-e2e:deploy","title":"Approve production deployment","summary":"The supported build and restart suite passed.","request":"Approve or deny promoting release 1.4.0.","source":{"application_id":"codex","application_label":"Codex","agent_id":"release-worker","agent_label":"Release worker"},"recommendation":"Approve after checking the rollback window.","impact":{"approve":"The caller may promote the release.","deny":"Production remains unchanged."},"correlations":{"project":{"authority":"torque","id":"PRJ-1","label":"Tangent"},"task":{"authority":"torque","id":"CW-1","label":"Release"},"session":{"authority":"codex","id":"session-1"}},"evidence":[{"type":"markdown","label":"Release notes","content":"## Validation\\n\\n- 42 checks passed\\n- [Run deployment](javascript:alert(1)) must remain inert"},{"type":"text","label":"Operator note","content":"The rollback window remains open for 30 minutes."},{"type":"diff","label":"Version bump","format":"unified","base_label":"1.3.9","head_label":"1.4.0","content":"--- a/VERSION\\n+++ b/VERSION\\n@@ -1 +1 @@\\n-1.3.9\\n+1.4.0"},{"type":"tangent_reference","label":"Durable inbox surface","surface_id":"surface_hitl_default","description":"Read the retained surface record without opening a room connection."},{"type":"artifact_ref","label":"Signed test report","authority":"torque","artifact_id":"artifact-544","digest":"sha256:1df0a9","media_type":"application/pdf","logical_kind":"test-report","size_bytes":18342,"sensitivity":"internal","retrieval_capability_id":"pdf-preview-v1"}]}}}'

curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.hitl_enqueue","arguments":{"contract_version":"1.0","kind":"attention","idempotency_key":"hitl-e2e:worker-warning","title":"Review worker recovery","summary":"A worker recovered after a transient fault.","request":"Acknowledge this durable warning after reviewing it.","source":{"application_id":"nanite","application_label":"Nanite","agent_id":"recovery-worker","agent_label":"Recovery worker"},"action_labels":{"acknowledge":"Mark seen","acknowledge_with_note":"Log context","reply":"Respond to worker"}}}}'
```

Verify the browser updates without a toast, OS notification, or new window.
The two rows must stay ordered by `queue_sequence`, even after selecting either
row. Switch among All, Approvals, and Attention and verify each is only a
projection: returning to All restores the same two rows in the same sequence
with no duplicates. Source, project/task/session, age, request,
recommendation, and both approval impact paths should be readable in detail.

## Inspect evidence in context

Open **Evidence → Open case file** on the approval item and verify:

1. The drawer keeps the request and FIFO rail in context on desktop and becomes
   a focused full-width surface on mobile. Escape and the close button return
   focus to **Open case file**.
2. Markdown headings/lists render as document structure, but the sample
   `javascript:` link is inert and labeled “link withheld.” The text record
   preserves whitespace, and the unified diff has separate old/new line columns
   with addition/deletion colors.
3. The Tangent surface reference loads a retained read-only projection without
   creating a room or WebSocket. Closing and reopening the drawer does not
   present, resolve, cancel, disconnect, or reorder either queue item.
4. Artifact authority, ID, digest, type, size, and sensitivity remain visible.
   **Request safe preview** reports an unsupported adapter inline on a stock
   Tangent host; it performs no file or network read. Only a host-registered
   `torque` + `pdf-preview-v1` adapter may provide content. Any adapter-provided
   external fallback must be an explicit HTTPS link.
5. Stop and restart Tangent with the same database, reopen the item, and confirm
   all inline evidence plus the Tangent reference and artifact metadata remain
   readable. A missing, expired, unauthorized, oversized, or unsupported
   preview must leave the drawer open and show a useful local state.

Do not substitute a filesystem path, `file:` URI, shell/process action, export
action, or clipboard action for an artifact ID. Those values confer no
authority and must never produce a preview.

## Resolve and recover

1. Open the first item's `/hitl/items/<itemID>` deep link in two tabs.
2. Refresh one tab. It must retain the selected item and show enabled decision
   controls after resynchronizing its durable presentation revision.
3. In tab A choose **Approve with note**, enter a non-empty note, and submit.
   The disposition commits the item durably to Resolved, while the surface stays
   on Pending and selects and focuses the next item with a higher
   `queue_sequence`; here, the second row becomes FIFO position 1. If no higher
   pending item exists but earlier items remain, selection wraps to the FIFO
   head. Only when no pending items remain does the surface switch to Resolved
   and select and focus the item just resolved.
4. In tab B try **Deny** from its stale view. The page must report that another
   client already committed the winning outcome. The approval and note must be
   unchanged.
5. Stop and restart Tangent with the same database. The pending row and resolved
   history must reappear, and the resolved item's deep link must still work.
6. Open the attention item. Exercise **Mark seen** once, then repeat with fresh
   attention items for **Log context** and **Respond to worker**. The latter two
   reject whitespace, persist trimmed text, and display Note and Reply as
   separate fields in resolved history. Every variant resolves to
   `acknowledged`; it must not accept work or trigger an external action.
7. For one fresh attention item, submit different variants concurrently in two
   tabs. Exactly one immutable acknowledgement wins; the stale tab reports the
   winner after resynchronizing. Repeat a pending-item restart and confirm the
   attention row remains pending until acknowledged or withdrawn.

Use `tangent.hitl_get` and a zero-wait `tangent.hitl_await` from the originating
caller after acknowledgement. Both must return the exact durable attention
response (including `note` or `reply`) while the browser history shows the same
outcome.

Also verify the note variants cannot submit whitespace, plain Approve/Deny are
one-click actions, Up/Down/Home/End navigate the focused queue, all controls
have visible focus, and the committed outcome is announced by a screen reader.
At a mobile viewport, `/hitl` shows the ledger and a selected deep link shows a
single-column detail view with **Back to queue**.

Tangent records only the participant outcome. This recipe must not cause any
deployment, task transition, or other downstream business action.
