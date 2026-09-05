# Tangent writing flow e2e

Manual recipe for the full writing workflow in one room, including one
explicit jump-back from `revision` to `drafting`.

## Setup

```bash
cd ~/dev/hollis-labs/apps/tangent
make build
./tangent --port=7842
```

In another terminal:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"writing-flow-manual"}}}'
```

Open `http://127.0.0.1:7842/r/<roomID>` in the browser.

## Canonical phase order

Default sequence:

1. `interview`
2. `synthesis`
3. `drafting`
4. `revision`
5. `output`

Jump-back rule:

- When a revision pass exposes a structural issue, explicitly call
  `tangent.session_advance_phase` back to `drafting`, resolve the new
  `tangent.block_draft` envelope, then advance forward again.

## 1. Interview

Advance the room:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"interview"}}}'
```

Send one `tangent.interview_question` envelope:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.interview_question","arguments":{"envelope":{"v":1,"id":"iq-1","type":"tangent.interview-question","title":"Writing scope","data":{"prompt":"What are we writing, and how big should it be?","topic_label":"scope"},"meta":{"roomID":"<roomID>"}}}}}'
```

In the browser, answer with something like:

- `A short article, around 800 words, with one clear example.`

## 2. Synthesis

Advance and send the synthesis envelope:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"synthesis"}}}'

curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tangent.synthesis_notes","arguments":{"envelope":{"v":1,"id":"synth-1","type":"tangent.synthesis-notes","title":"Synthesis handoff","data":{"private_notes":"Keep the article direct and constraint-led.","summary":"Lead with the claim, then illustrate it with one example.","outline_state":"present","outline":{"title":"Draft outline","items":[{"label":"Claim"},{"label":"Example"}]}},"meta":{"roomID":"<roomID>"}}}}}'
```

Click `Continue`.

## 3. Drafting

Advance to `drafting`, then send a `tangent.block_draft` envelope:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"drafting"}}}'

curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"tangent.block_draft","arguments":{"envelope":{"v":1,"id":"draft-1","type":"tangent.block-draft","title":"Opening block","data":{"block_id":"intro","mode":"section","label":"Intro","content":"Original opening paragraph with too much setup and two examples."},"meta":{"roomID":"<roomID>"}}}}}'
```

Click `Accept`.

## 4. Revision and jump-back

Advance to `revision`, then send a `tangent.prose_revision` envelope:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"revision"}}}'

curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"tangent.prose_revision","arguments":{"envelope":{"v":1,"id":"review-1","type":"tangent.prose-revision","title":"Review pass","data":{"lens":"review","revision_id":"review-1","block_id":"intro","source_text":"Original opening paragraph with too much setup and two examples.","suggestions":[{"id":"s1","label":"Lead with the claim","suggested_text":"Lead with the main claim before the setup."}]},"meta":{"roomID":"<roomID>"}}}}}'
```

Mark the suggestion `Accept`, submit, then jump back:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"drafting","reason":"jump back after review to tighten the opening"}}}'
```

Now send a second draft envelope for the same `block_id` and choose
`Inline edit` in the browser:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"tangent.block_draft","arguments":{"envelope":{"v":1,"id":"draft-2","type":"tangent.block-draft","title":"Opening revision","data":{"block_id":"intro","mode":"section","label":"Intro","content":"Original opening paragraph with too much setup and two examples."},"meta":{"roomID":"<roomID>"}}}}}'
```

Use edited text:

- `Lead with the claim, then keep one concrete example.`

Advance to `revision` again if you want one last copy/style pass before output.

## 5. Output

Advance to `output`, then send the final renderer:

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"tangent.session_advance_phase","arguments":{"roomID":"<roomID>","to_phase":"output"}}}'

curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"tangent.output_render","arguments":{"envelope":{"v":1,"id":"output-1","type":"tangent.output-render","title":"Final output","data":{"title":"Final draft","markdown":"# Final draft\n\nLead with the claim, then keep one concrete example.","filename":"final-draft.md","format":"markdown","summary":"Final polished article."},"meta":{"roomID":"<roomID>"}}}}}'
```

Verify in the browser:

- final markdown is rendered clearly
- `Copy markdown` works
- `Download .md` downloads the artifact
- clicking `Done` resolves the envelope

## 6. Verify room state

```bash
curl -s http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}'
```

Confirm:

- `final_output.markdown` matches the final artifact
- `final_output.filename` is `final-draft.md`
- `phases_visited` includes `drafting` twice because of the jump-back
- `accepted_draft_blocks` still preserves the accepted drafting history

## Mock flow

```bash
node scripts/output-render-mock-call.mjs
```
