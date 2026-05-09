# Multi-envelope session e2e

Manual recipe for exercising one persistent room through several bundled
workflows without involving an LLM.

## Prerequisites

- `make build` produced `./tangent`
- `jq` and `sqlite3` are installed
- Port `7842` is free, or all commands below are updated consistently

## 1. Boot Tangent

```bash
cd ~/Projects-apps/tangent
./tangent
```

## 2. Create one room

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"manual-session"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Capture the returned `roomID`, then open:

```text
http://127.0.0.1:7842/r/<roomID>
```

The page should show the room shell waiting for the first envelope.

## 3. Advance a triage envelope

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.session_advance","arguments":{"roomID":"<roomID>","envelope":{"v":1,"id":"triage-1","type":"tangent.triage","title":"Triage pass","data":{"prompt":"Decide all three items.","items":["alpha","beta","gamma"]}}}}}'
```

Resolve the triage envelope in the browser and let the curl call return.

## 4. Advance a feedback envelope

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.session_advance","arguments":{"roomID":"<roomID>","envelope":{"v":1,"id":"feedback-1","type":"tangent.feedback","title":"Feedback pass","data":{"prompt":"Answer the release questions.","questions":[{"id":"headline","type":"text","label":"Headline","required":true},{"id":"launch","type":"radio","label":"Launch?","required":true,"options":[{"value":"yes","label":"Yes"},{"value":"no","label":"No"}]}]}}}}}'
```

Answer the form in the same browser room and wait for curl to return.

## 5. Advance a design-iteration envelope

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_advance","arguments":{"roomID":"<roomID>","envelope":{"v":1,"id":"design-1","type":"tangent.design-iteration","title":"Design pass","data":{"caption":"Pick a hero","variant_id":"variant-1","html":"<main><button id=\"hero-a\">Hero A</button><button id=\"hero-b\">Hero B</button></main>","prompts":[{"id":"hero-a","kind":"click-region","label":"Hero A","selector":"#hero-a"},{"id":"hero-b","kind":"click-region","label":"Hero B","selector":"#hero-b"}]}}}}}'
```

Click one region in the browser and wait for curl to return.

## 6. Verify persisted room state

Inspect the room snapshot:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Inspect the database directly:

```bash
sqlite3 ~/.tangent/tangent.db "select id,status from rooms order by created_at desc limit 5;"
sqlite3 ~/.tangent/tangent.db "select room_id,envelope_id,status from envelopes order by created_at desc limit 10;"
```

You should see the room row plus three resolved envelope rows.

## 7. Restart and confirm history survives

Stop Tangent, start it again, reopen `http://127.0.0.1:7842/r/<roomID>`,
and rerun `tangent.session_get`. The room and its envelope history should
still be present after restart.
