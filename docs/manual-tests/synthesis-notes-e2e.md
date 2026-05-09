# Synthesis-notes e2e

Manual recipe for exercising `tangent.synthesis-notes` through both the
outline-preview and explicit skip paths, with server-side visibility
gating checked via `session_get`.

## Prerequisites

- `make build` produced `./tangent`
- `jq` is installed
- Port `7842` is free, or all commands below are updated consistently

## 1. Boot Tangent

```bash
cd ~/Projects-apps/tangent
./tangent
```

## 2. Create a room and open the tab

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"Synthesis outline path"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Open the returned `/r/<roomID>` URL in the browser.

## 3. Send hidden synthesis notes

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.synthesis_notes","arguments":{"envelope":{"v":1,"id":"synth-1","type":"tangent.synthesis-notes","title":"Synthesis handoff","data":{"private_notes":"Keep the draft grounded in constraints before examples.","summary":"Start with constraints, then show one concrete example.","outline_state":"present","outline":{"title":"Draft outline","items":[{"label":"Goal","description":"Frame the writing target."},{"label":"Constraints","description":"Name the key limits first."}]}},"meta":{"roomID":"<roomID>"}}}}}'
```

Expect the room UI to show the hidden placeholder, not the outline.

## 4. Verify hidden state via `session_get`

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Expect:

- `synthesis_notes.visibility` is `hidden`
- `synthesis_notes.summary` is absent
- `synthesis_notes.outline` is absent
- `phase_outputs.synthesis.data.private_notes` is still persisted

## 5. Advance to drafting and resend the handoff

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"drafting","reason":"move into drafting"}}}'
```

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.synthesis_notes","arguments":{"envelope":{"v":1,"id":"synth-2","type":"tangent.synthesis-notes","title":"Draft orientation","data":{"private_notes":"Keep the draft grounded in constraints before examples.","summary":"Start with constraints, then show one concrete example.","outline_state":"present","outline":{"title":"Draft outline","items":[{"label":"Goal","description":"Frame the writing target."},{"label":"Constraints","description":"Name the key limits first."}]}},"meta":{"roomID":"<roomID>"}}}}}'
```

Expect the room UI to show the summary plus the outline preview.

## 6. Verify the explicit skip path

Repeat steps 2-5 in a fresh room, but send:

```json
{
  "private_notes": "Same synthesis, but skip the outline preview.",
  "summary": "Start with constraints, then show one concrete example.",
  "outline_state": "skipped"
}
```

After advancing to `drafting`, expect:

- `synthesis_notes.visibility` is `visible`
- `synthesis_notes.outline_state` is `skipped`
- the UI shows the skipped-outline state instead of an outline preview

## 7. Mock automation option

For a no-browser smoke pass, run:

```bash
node scripts/synthesis-notes-mock-call.mjs
```
