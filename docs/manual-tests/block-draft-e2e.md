# Block-draft e2e

Manual recipe for exercising `tangent.block_draft` through both the
outline-first and skip-outline drafting paths, while confirming accepted
blocks persist and `session_get` reconstructs the current draft.

## Prerequisites

- `make build` produced `./tangent`
- `jq` is installed
- Port `7842` is free, or all commands below are updated consistently

## 1. Boot Tangent

```bash
cd ~/dev/hollis-labs/apps/tangent
./tangent
```

## 2. Create a room and open the tab

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"Block draft outline path"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Open the returned `/r/<roomID>` URL in the browser.

## 3. Seed synthesis and advance to drafting

Send either the outline-first synthesis payload:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.synthesis_notes","arguments":{"envelope":{"v":1,"id":"synth-1","type":"tangent.synthesis-notes","title":"Synthesis handoff","data":{"private_notes":"Keep the draft grounded in constraints before examples.","summary":"Start with constraints, then add one concrete example.","outline_state":"present","outline":{"title":"Draft outline","items":[{"label":"Goal","description":"Frame the writing target."},{"label":"Constraints","description":"Name the limits first."}]}},"meta":{"roomID":"<roomID>"}}}}}'
```

or the skip-outline payload:

```json
{
  "private_notes": "Keep the draft grounded in constraints before examples.",
  "summary": "Start with constraints, then add one concrete example.",
  "outline_state": "skipped"
}
```

Then advance the room:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"drafting","reason":"move into drafting"}}}'
```

## 4. Send a draft block

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.block_draft","arguments":{"envelope":{"v":1,"id":"draft-1","type":"tangent.block-draft","title":"Intro block","data":{"block_id":"intro","mode":"section","label":"Intro","content":"First candidate section.","rationale":"Lead with the strongest point.","outline_hint":"Draft toward the active section."},"meta":{"roomID":"<roomID>"}}}}}'
```

Expect the room UI to show:

- the proposed draft block
- action choices for `accept`, `request revision`, `inline edit`, and `different direction`
- the reconstructed current draft after at least one accepted block exists

## 5. Verify accepted-block persistence via `session_get`

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Expect:

- `accepted_draft_blocks` grows only after `accept` or `inline_edit`
- `accepted_draft_blocks` preserves append order
- `current_draft.blocks` reconstructs the latest accepted version per `block_id`
- `current_draft.markdown` joins the accepted blocks into the current piece

## 6. Exercise both workflow variants

- Outline-first path: accept one block, then inline-edit a second block.
- Skip-outline path: request a revision first, then submit the revised block with `inline_edit`.

In the skip-outline path, verify the revision-only response does **not**
append to `accepted_draft_blocks`.

## 7. Mock automation option

For a no-browser smoke pass, run:

```bash
node scripts/block-draft-mock-call.mjs
```
