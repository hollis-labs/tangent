# MCP smoke test (v0.1)

A handful of curl probes against the Tangent MCP server. Used to verify a
fresh build can answer `tools/list` and `tools/call` without spinning up
a full MCP client.

## Prereqs

- `make build` produced `./tangent` (or `go run ./cmd/tangent` works too).
- `jq` for pretty output (optional).

## Boot

```bash
./tangent &
# Waits for "tangent listening" / "MCP server ready" log lines on stderr.
```

The MCP surface is mounted on the same port as the SPA:

- Streamable HTTP: `http://localhost:7842/mcp` (modern clients).
- SSE legacy: `http://localhost:7842/sse` (older Claude Code; long-lived
  event stream).

In v0.1 the streamable handler runs in **stateless + JSONResponse** mode,
which lets these one-shot curl probes succeed without first sending an
`initialize` request. Stateful behaviour returns once a session-bound
workflow needs it (post-PR 4).

## Probe 1 — tool catalog

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools | length'
# Expected: 2
```

Two tools today: `tangent.list_workflows` and `tangent.triage`.

## Probe 2 — list_workflows

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.list_workflows","arguments":{}}}' \
  | jq '.result'
```

PR 3 has no dispatcher handlers wired yet, so `workflows` is `[]`. PR 4
adds `triage`, after which the array is non-empty.

## Probe 3 — triage NOT_WIRED

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.triage","arguments":{"envelope":{"v":1,"id":"smoke-1","type":"triage","data":{}}}}}' \
  | jq '.result'
```

Expected response:

- `isError: true`
- `content[0].text` is a JSON envelope-error frame with
  `error.code == "NOT_WIRED"`.

This is the v0.1 contract: the MCP layer accepts the call, the envelope
service validates the input shape, the dispatcher reports "no handler",
and the tool surfaces it as `NOT_WIRED`. PR 4 registers a handler that
turns this path into a real triage flow.

## Optional — MCP Inspector

The official MCP Inspector can drive the streamable transport
interactively:

```bash
npx @modelcontextprotocol/inspector
# In the UI, set Transport=Streamable HTTP, URL=http://localhost:7842/mcp.
```

## Shutdown

```bash
kill %1
# Or send SIGINT/SIGTERM; the server logs "shutdown signal received" and
# drains in-flight requests for up to 10s.
```
