# `tangent.prose-revision` e2e

Manual recipe for exercising one `tangent.prose-revision` pass for each lens:
`review`, `copy`, and `style`.

## Setup

```bash
cd ~/Projects-apps/tangent
make build
./tangent --port=7842
```

In another terminal:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"prose-revision-manual"}}}'
```

Open `http://127.0.0.1:7842/r/<roomID>` in the browser.

## Seed one accepted draft block

1. Send a `tangent.synthesis_notes` envelope for the room and click `Continue`.
2. Advance the room to `drafting` with `tangent.session_advance_phase`.
3. Send a `tangent.block_draft` envelope and click `Accept`.

At this point, `tangent.session_get` should show one accepted draft block and a non-empty `current_draft`.

## Run the three lenses

Send one `tangent.prose_revision` envelope per lens. Example `review` call:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.prose_revision","arguments":{"envelope":{"v":1,"id":"review-1","type":"tangent.prose-revision","title":"Review pass","data":{"lens":"review","revision_id":"opening-review","block_id":"intro","source_text":"Original opening paragraph with too much setup and two examples.","suggestions":[{"id":"s1","label":"Lead with the claim","suggested_text":"Start with the main claim before the setup."},{"id":"s2","label":"Trim the example load","suggested_text":"Keep one concrete example instead of two."}]},"meta":{"roomID":"<roomID>"}}}}}'
```

Repeat with `lens: "copy"` and `lens: "style"`.

For each envelope, verify:

- the active lens badge changes
- the accepted draft context is shown above the source text
- every suggestion must be marked `Accept`, `Reject`, or `Comment` before submit
- `Comment` requires text

After all three passes:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}'
```

Confirm:

- `prose_revision_outcomes` has three entries
- each entry carries `lens`, `revision_id`, `suggestions`, and explicit per-suggestion `outcomes`
- accepted draft state remains under `accepted_draft_blocks` and `current_draft`

## Mock flow

```bash
node scripts/prose-revision-mock-call.mjs
```
