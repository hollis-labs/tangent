# MCP smoke test

One command checks that Tangent's MCP surface is reachable and correct over
every hop a consumer uses: direct `/mcp`, the Tether gateway that fronts it,
and the Cerberus resource that owns the process. (The legacy `/sse` transport
no longer exists: the Substrate `mcp` module serves Streamable HTTP only.)

```bash
make smoke
```

That is the whole recipe for the CI-safe layer. It builds `./cmd/tangent`,
boots it on a port it reserved with a data directory in a temp directory
(`TANGENT_DATA_DIR`; the port and a cheap password cost come from a
`SUBSTRATE_CONFIG` override file), makes a throwaway owner and an API key for
the probes (first-run setup on a throwaway directory only), and checks:

- `/healthz` liveness (open), `/api/health/ready` readiness and
  `/api/health/capability` per-kind health (both need `tangent.view`);
- direct `/mcp` `tools/list`, which lists the tools the probe key's scopes allow;
- one read-only tool call, `tangent_health_get`, whose readiness must agree
  with `/api/health/ready`.

A build with no frontend embedded fails the renderer-host readiness check by
design (the SPA is built and embedded for a release); the check tolerates
exactly that and nothing else.

It touches no live instance, no Cerberus resource, and no Tether catalog, so it
is safe in CI — and because the package carries no build tag, `go test ./...`
already runs it.

## The tool count is never written down

The expected surface is derived by running the binary and asking it. There is
no literal anywhere in the check, and there is none here either: the number
moved six times inside a single day of work, and every document that pinned it
has been wrong within a week.

To see the current count, ask the build:

```bash
make smoke 2>&1 | grep 'shipped build advertises'
# shipped build advertises N tools (digest <short-sha256>) over /mcp
```

The output above is deliberately written with placeholders. A worked example
with real numbers in it would be a literal, and it would be wrong by the next
task that adds a tool — which is exactly the failure this whole section
exists to prevent.

The digest is a short sha256 over the sorted tool names. Two hops with the same
digest are serving the same surface; that is the only comparison the check
makes. (The published names themselves are recorded in
`internal/mcp/testdata/tool_names.txt` and compared byte for byte by
`cmd/tangent/host_test.go`; see [`mcp-tool-names.md`](./mcp-tool-names.md).)

## The four failure modes

An operator reading this at 2am needs to know *which* thing to go fix. Every
finding leads with one of four modes, so the first token of the line is the
answer:

| Mode | What it means | What to do |
|---|---|---|
| `FAIL [PROCESS_DOWN]` | Nothing is serving MCP at the address checked. | Restart Tangent (`cerberus resource deploy tangent-dev` when managed). |
| `FAIL [UPSTREAM_ABSENT]` | The gateway answers, but Tangent is not among what it serves: no catalog entry, a disabled one, or a discovery returning no Tangent tools. | Restore or enable the catalog entry, then refresh the gateway. |
| `FAIL [CATALOG_STALE]` | Something answered, with a surface that is not the one this build ships. | Redeploy so the serving process is the build on disk, then refresh the gateway's catalog. |
| `FAIL [CAPABILITY_UNHEALTHY]` | The process serves and the surface matches, but readiness or a per-kind capability report says it cannot do the work. | Follow the operator action the health report itself carries. |

Each line is `FAIL [MODE] check: detail`, with the operator action indented
beneath it. Example, from a real run against a deployment two commits behind
the working tree:

```
FAIL [CATALOG_STALE] deployed /mcp tool surface: shipped build advertises N tools
  (digest <a>), deployed /mcp advertises M tools (digest <b>);
  absent there: tangent_retention_get, tangent_telemetry_query
  operator action: Redeploy Tangent so the serving process is the build on disk
  then refresh the gateway's catalog so it re-lists tools. (The database
  module applies a schema change when the process starts.)
2 finding(s); modes: CATALOG_STALE
```

The counts and digests are placeholders for the same reason. What matters in a
real finding is the *names* it lists under `absent there`: those are the tools
the deployment is missing, and they say how far behind it is. **This shape is
not hypothetical — the live deployment has been observed advertising a smaller
surface than the build on disk while this document was being written.**

### Why `PROCESS_DOWN` is not the same question as "is the resource running"

`cerberus resource status tangent-dev` reports supervisor bookkeeping. It does
not dial the port. A process that exited without the supervisor noticing still
reads `running`, and an operator who trusts that answer spends the first twenty
minutes of an incident looking in the wrong place. The smoke check queries both
and reports the disagreement:

```
FAIL [PROCESS_DOWN] supervisor vs listener: supervisor reports tangent-dev
  status="running" but nothing answers MCP at http://127.0.0.1:7842; supervisor
  status is bookkeeping, not a live probe, so the two can disagree
```

## Environment-coupled checks (operator path)

The live deployment, its supervisor, and the Tether gateway are behind one
explicit gate. Nothing probes a running instance by accident:

```bash
TANGENT_SMOKE_ENV=1 make smoke
```

That adds two checks to the same command:

1. **`TestDeployedTangentMatchesShippedBuild`** — supervisor status vs. an
   actual listener, liveness/readiness/capability on the live instance, its
   `/mcp` surface compared against the build this checkout
   produces, one read-only `tangent_health_get` call, and the Tether catalog
   entry checked against the address that is actually serving.
2. **`TestTetherGatewayStillPublishesTangent`** — runs the real `mux` gateway
   in `mcp --proxy --only tangent` mode, confirms Tangent is still discoverable
   through it, compares the surface it advertises against the shipped build,
   and forwards one read-only tool call. A gateway can list a tool it can no
   longer forward, so discovery alone is not enough.

Knobs, all optional:

| Variable | Default | Purpose |
|---|---|---|
| `TANGENT_SMOKE_ENV` | unset | `1` enables the environment-coupled checks. |
| `TANGENT_SMOKE_URL` | `http://127.0.0.1:7842` | The deployment to probe. |
| `TANGENT_SMOKE_KEY` | unset | An API key for the deployment holding `tangent.mcp` and `tangent.view`. Without it the probes are refused with 401. |
| `TANGENT_SMOKE_RESOURCE` | `tangent-dev` | The Cerberus resource id. |
| `TANGENT_SMOKE_CATALOG` | `~/.tether/catalog` | The Tether catalog root to read. |
| `TANGENT_SMOKE_GATEWAY` | `mux` | The gateway binary to drive. |

### What the gated layer will not do

It is read-only by construction, because a check that can change the thing it
measures is a check nobody runs during an incident:

- It never deploys, reloads, stops, or removes a Cerberus resource. It runs
  `cerberus resource status` and nothing else.
- It never writes to `~/.tether`. The gateway arm copies the operator's own
  catalog entry into a temp directory and points the gateway at that, so the
  process under test is the real one while the state it touches is disposable.
- It never touches the live data directory. The reference build always boots
  with `TANGENT_DATA_DIR` inside the test's temp directory, on a port it
  reserved — the harness refuses to boot on 7842.
- It signals only pids it started.

### Verifying a restart or a catalog refresh

Run the gated command on both sides of the operation:

```bash
TANGENT_SMOKE_ENV=1 make smoke          # before: expect CATALOG_STALE if the deployment is behind
cerberus resource deploy tangent-dev    # operator action — not something the check performs
TANGENT_SMOKE_ENV=1 make smoke          # after: the surfaces should agree
```

The surface comparison is what proves the restart actually picked up the binary
on disk. A supervisor's own `running` cannot tell you that, and neither can an
uptime: a restart that re-launched the same stale artifact looks identical from
the outside and identical to the gateway, right up until a caller asks for a
tool that is not there.

When the schema moved as well as the surface, a backup comes first; the new
process migrates the database as it starts.

## Manual curl probes

The gated command covers all of this; these remain for a machine with no Go
toolchain, or for probing something the harness cannot reach.

`/mcp` is served by the Substrate `mcp` module and needs a credential: an API
key that holds `tangent.mcp` plus the scope of each tool you call. The owner
mints one in the identity API (`POST /api/identity/keys`, body
`{"name": "...", "scopes": [...]}`, signed in) and it is shown once. Put it in
a variable without echoing it, and pass it in a header file rather than in the
command line, so it never reaches the process list or shell history:

```bash
read -rs TANGENT_KEY                                  # paste the key, press enter
printf 'Authorization: Bearer %s\n' "$TANGENT_KEY" > /tmp/tangent.hdr && chmod 600 /tmp/tangent.hdr
```

The MCP surface is mounted on the same port as the SPA, Streamable HTTP only:
`http://127.0.0.1:7842/mcp`.

**The reply is SSE-framed.** The module's handler answers a POST that accepts
`text/event-stream` (which is what MCP clients send: `Accept: application/json,
text/event-stream`) with one server-sent event, `event: message` then
`data: {json-rpc}`, not a bare JSON body; a POST that does not accept
`text/event-stream` is refused with 400. Unwrap it with `sed`, as below
(`TestG6bWhatTheModuleAnswersToTheMainClientsPost` records the behavior
against the real host, so a framework change shows up there).

The handler needs no `initialize` for a single call, so one-shot probes work.

### Probe 1 — tool catalog

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp -H @/tmp/tangent.hdr \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | sed -n 's/^data: //p' | jq '.result.tools | length'
```

It lists the tools your key may call, not every tool: a key with only
`tangent.view` sees the reads. To see all of them, use a key that holds every
tool scope (see [`mcp-tool-names.md`](./mcp-tool-names.md)). Compare the answer
with what the build on disk advertises rather than with a number from a
document: boot a throwaway copy (`TANGENT_DATA_DIR=$(mktemp -d)` and a
`SUBSTRATE_CONFIG` file with `httphost: {addr: "127.0.0.1:17842"}`), make its
owner and a key, and list its tools the same way.

A difference between the two is `CATALOG_STALE`: the deployment is behind the
artifact.

### Probe 2 — the read-only tool call

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp -H @/tmp/tangent.hdr \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent_health_get","arguments":{}}}' \
  | sed -n 's/^data: //p' | jq '.result.structuredContent.readiness.status'
```

`tangent_health_get` is the smoke check's one read-only call, and the choice
is deliberate: every candidate proves the transport carried a call, and only
this one also answers whether the host can do the work. It writes nothing,
creates no room, and returns the same pass/warn/fail vocabulary
`/api/health/ready` and `cerberus resource doctor` print. It is also the only
one of the three health probes an agent behind the gateway can reach — it
speaks JSON-RPC and holds no HTTP client for `/api/health/ready`.

### Probe 3 — strict schema rejection

```bash
curl -fsS -X POST http://127.0.0.1:7842/mcp -H @/tmp/tangent.hdr \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent_triage","arguments":{"envelope":{"v":1,"id":"smoke-1","type":"triage","data":{}}}}}' \
  | sed -n 's/^data: //p' | jq '.result'
```

Expected with a key that holds `tangent.submit`: `isError: true`, with the
error identifying the invalid `type` before any room is created. The valid tool
pins `type: "tangent.triage"`. With a key that does not hold it the answer is
`isError: true` with `forbidden`: the call is refused before the tool runs.

These probes send no browser headers. A probe that attaches to `/ws` needs a
signed-in session; see
[`mcp-integration.md`](./mcp-integration.md#room-access-and-caller-scope).

### Optional — MCP Inspector

```bash
npx @modelcontextprotocol/inspector
# Transport=Streamable HTTP, URL=http://127.0.0.1:7842/mcp,
# header Authorization: Bearer <the key>.
```

### Shutdown

```bash
kill %1
```

Send SIGINT/SIGTERM to a pid you started. Never a pattern-matched kill: a
managed `tangent-dev` may be running against the real database on the same
machine.

## Related

- The cross-repository gateway smokes (`-tags tether_smoke`) drove Tangent
  through its SSE endpoint and are gone with it. A replacement belongs to the
  Tether side once its catalog entry for Tangent is a Streamable HTTP entry with
  a key (see `docs/client-table.md`).
- [`mcp-integration.md`](./mcp-integration.md) — client configuration and the
  cold-start recipe.
