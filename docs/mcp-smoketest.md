# MCP smoke test

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

The streamable handler supports **stateless + JSONResponse** calls, which lets
these one-shot curl probes succeed without first sending an `initialize`
request. Room and interaction state is durable application state, not an MCP
transport-session requirement.

## Probe 1 — tool catalog

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools | length'
# Expected: 39
```

The build-derived grouping is 25 room/workflow compatibility tools, 10 generic
durable interaction tools, and 4 strict HITL inbox operations. See the exact
name list in [`mcp-integration.md`](./mcp-integration.md#verification-no-agent-required).

## Probe 2 — list_workflows

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.list_workflows","arguments":{}}}' \
  | jq '.result'
```

The result contains the registered envelope workflow definitions. The durable
HITL operations are discovered through `tools/list`; `tangent.hitl-item` is an
interaction definition rather than a room workflow launcher.

## Probe 3 — strict schema rejection

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.triage","arguments":{"envelope":{"v":1,"id":"smoke-1","type":"triage","data":{}}}}}' \
  | jq '.result'
```

Expected response:

- `isError: true`
- the error identifies the invalid `type`/payload before any room is created.

The valid tool pins `type: "tangent.triage"` and requires the complete triage
shape. This deliberately invalid probe confirms schema enforcement without
opening a browser workflow.

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
