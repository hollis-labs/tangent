# Interview-question e2e

Manual recipe for exercising three `tangent.interview-question` turns in
one persistent room and confirming `session_get` exposes structured Q/A
history for agent resume.

## Prerequisites

- `make build` produced `./tangent`
- `jq` is installed
- Port `7842` is free, or all commands below are updated consistently

## 1. Boot Tangent

```bash
cd ~/dev/hollis-labs/apps/tangent
./tangent
```

## 2. Ask the first interview question

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.interview_question","arguments":{"envelope":{"v":1,"id":"iq-1","type":"tangent.interview-question","title":"Question 1","data":{"prompt":"What is the main goal for this workflow?","helper_text":"Answer in full sentences.","thread_id":"goals","topic_label":"Workflow goals","choices":[{"id":"quality","label":"Quality"},{"id":"speed","label":"Speed"}],"output_shape":{"label":"Preferred output shape","help":"If you want a specific format, say it explicitly."}}}}}}'
```

Read the room URL from the tool response and open it in the browser. (The
server log carries only the room id — per ADR 0002 §8 it never assembles a
URL — so `http://127.0.0.1:7842/r/<roomID>` is the form to construct if you
are watching stderr instead.)
Submit a long-form answer.

## 3. Ask two follow-up questions in the same room

Reuse the `roomID` from the first room URL:

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.interview_question","arguments":{"envelope":{"v":1,"id":"iq-2","type":"tangent.interview-question","data":{"prompt":"What constraints matter most?","thread_id":"constraints","topic_label":"Constraints","choices":[{"id":"time","label":"Time"},{"id":"scope","label":"Scope"}]},"meta":{"roomID":"<roomID>"}}}}}'
```

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.interview_question","arguments":{"envelope":{"v":1,"id":"iq-3","type":"tangent.interview-question","data":{"prompt":"Give one concrete example the agent should remember.","thread_id":"examples","topic_label":"Examples","output_shape":{"label":"How should the final synthesis be formatted?"}},"meta":{"roomID":"<roomID>"}}}}}'
```

Resolve each question in the same browser tab.

## 4. Verify structured history

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.session_get","arguments":{"roomID":"<roomID>"}}}' \
  | jq -r '.result.content[0].text | fromjson'
```

Expect:

- `envelopes_history` length is `3`
- every entry has `type: "tangent.interview-question"`
- every entry includes `interview_question.thread_id`
- answered entries include `interview_question.answer_text`
- selected quick picks appear as `interview_question.selected_choice_id`
- explicit formatting preferences appear as `interview_question.output_shape_signal`

## 5. Mock automation option

For a no-browser smoke pass, run:

```bash
node scripts/interview-question-mock-call.mjs
```
