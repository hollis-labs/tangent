# Room workflow completion and recovery e2e

Use this recipe to verify by hand what the automated suite proves: a room
workflow whose caller gives up waiting still reaches the human, and the caller
can still come back for the answer.

The contract itself is documented in `docs/room-workflow-completion.md`.

## Automated gates

These are the tests to run first; the manual walk-through below is for
confirming the browser half with your own eyes.

```bash
mise --no-config exec node@22.12.0 -- make build-ui
go test -race ./internal/mcp -count=1 \
  -run '^(TestRoomWorkflows_|TestRoomWorkflow_|TestOperatorCompletionSurvivesCallerWriteTimeout|TestLostCallerTransportNeverTerminalizes)'
go test -race ./internal/interaction ./internal/room ./internal/db -count=1
```

`TestOperatorCompletionSurvivesCallerWriteTimeout` is the direct regression for
the live incident: it mounts the MCP handler behind a deliberately short HTTP
write timeout, lets the caller's request die, has the operator answer
afterwards, and then recovers the identical immutable result three ways.

## Start from an isolated database

```bash
make build
TANGENT_DB_PATH=/tmp/tangent-completion-e2e.db TANGENT_HTTP_PORT=7843 ./tangent
```

## 1. Async mode returns immediately with a durable handle

```bash
curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tangent.session_create","arguments":{"title":"completion e2e"}}}'
```

Note the `roomID`, then:

```bash
curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.triage","arguments":{"completion":{"mode":"async"},"envelope":{"v":1,"id":"completion-e2e-1","type":"tangent.triage","data":{"prompt":"Triage these","items":["one","two"]},"meta":{"roomID":"<ROOM_ID>"}}}}}'
```

Expect an immediate result with `"status":"pending"`, a `handle` carrying
`interaction_id` and `url`, and a `resume` block. Confirm it came back in well
under a second — async never waits.

## 2. The room shows the work, and closing the tab does not lose it

Open the `handle.url` from the receipt. Confirm the triage envelope renders.

Now close the tab without answering. Reopen the same URL. The envelope must
render again: a browser is a replaceable attachment, not the request.

## 3. Restart does not lose it either

Stop the server with Ctrl-C, start it again with the same
`TANGENT_DB_PATH`, and reload the room URL. The envelope must render again —
this time rebuilt entirely from canonical records, since every in-memory
structure was discarded. The startup log line `restored room presentations`
reports what came back.

## 4. Answer as the operator, then recover as the caller

Submit the triage response in the browser. Then, as the caller:

```bash
curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.interaction_get","arguments":{"interaction_id":"<INTERACTION_ID>","requester_scope":"standalone-local"}}}'
```

Then recover the same result by *retrying the original invocation* — the exact
same call as step 1, unchanged. It must return the operator's response rather
than re-asking, and it must match what `interaction_get` returned.

## 5. A changed payload is a conflict

Repeat the step-1 call with the same `id` but a different `data`. Expect an
error result with code `IDEMPOTENCY_CONFLICT`, and confirm in the browser that
nothing was re-asked.

## 6. Wait mode still returns the normal response quickly

Issue a second triage call with a new envelope id and no `completion` object,
answer it in the browser within a few seconds, and confirm the tool returns the
ordinary triage response — not a receipt. This is the inline fast path, and it
is the behaviour every pre-existing caller depends on.

## 6b. Wait mode converts to a pending receipt, it does not fail

This is the step that proves the resumable contract, and it is the one most
worth actually running.

Issue a third triage call with a new envelope id and no `completion` object,
and then **do not answer it**. Leave the browser alone.

`internal/roomflow.CompatibilityWindow` bounds the inline wait. Past that
window the call must return a **successful pending receipt** carrying the
durable handle and the room URL — not an error, not a timeout, not a
cancellation. Confirm:

- the result is a success, not an error result;
- it carries an `interaction_id` and a room URL;
- the browser still shows the envelope waiting, untouched;
- answering it afterwards still produces a terminal outcome, retrievable with
  `tangent.interaction_get`.

Then confirm the same interaction can be waited on again with
`tangent.interaction_await`, and that re-issuing the identical original
invocation returns the same durable handle rather than creating a second
interaction.

The window is a constant in the source rather than a number to memorize; read
it from `internal/roomflow/roomflow.go` if you need the exact value while
timing the step.

## 7. Acknowledgement is separate and idempotent

```bash
curl -sS http://127.0.0.1:7843/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tangent.interaction_acknowledge","arguments":{"interaction_id":"<INTERACTION_ID>","requester_scope":"standalone-local"}}}'
```

The first call returns `"created": true`. Repeat it: the same
`acknowledgement_id` comes back with `"created": false`. Confirm in SQLite that
retrieval, delivery, and acknowledgement are three separate records:

```bash
sqlite3 /tmp/tangent-completion-e2e.db \
  "SELECT (SELECT COUNT(*) FROM terminal_outcome_retrievals),
          (SELECT COUNT(*) FROM delivery_attempts WHERE status='delivered'),
          (SELECT COUNT(*) FROM terminal_outcome_acknowledgements);"
```

## 8. Legacy SSE behaves identically

Repeat steps 1 and 4 against `http://127.0.0.1:7843/sse` with an MCP client
that speaks the 2024-11-05 SSE transport. Confirm the session survives idling
well past the server-wide read timeout — the server-wide read deadline is cleared for MCP
routes, and a dropped SSE session is what used to make catalog refresh hang and
the next call answer "session not found".
