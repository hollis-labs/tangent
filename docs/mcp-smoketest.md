# MCP smoke test

One command checks that Tangent's MCP surface is reachable and correct over
every hop a consumer uses: direct `/mcp`, legacy `/sse`, the Tether gateway
that fronts them, and the Cerberus resource that owns the process.

```bash
make smoke
```

That is the whole recipe for the CI-safe layer. It builds `./cmd/tangent`,
boots it on a port it reserved against a database in a temp directory, and
checks:

- `/healthz` liveness, `/readyz` readiness, `/healthz/capability` per-kind health;
- direct `/mcp` `tools/list`;
- legacy `/sse` `tools/list` through a real MCP client session;
- that the two transports advertise **the same surface** — they are one
  registry behind two handlers, so a difference means one of them is serving
  something else, and `/sse` is the one the gateway dials and nobody probes by
  hand;
- one read-only tool call, `tangent.health_report`, over both transports, whose
  readiness must agree with `/readyz`.

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
#   and N tools (digest <short-sha256>) over /sse
```

The output above is deliberately written with placeholders. A worked example
with real numbers in it would be a literal, and it would be wrong by the next
task that adds a tool — which is exactly the failure this whole section
exists to prevent.

The digest is a short sha256 over the sorted tool names. Two hops with the same
digest are serving the same surface; that is the only comparison the check
makes.

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
  absent there: tangent.retention_status, tangent.telemetry_query
  operator action: Redeploy Tangent so the serving process is the build on disk
  (`tangent --migrate-only` first when the schema moved), then refresh the
  gateway's catalog so it re-lists tools.
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
   `/mcp` and `/sse` surfaces compared against the build this checkout
   produces, one read-only `tangent.health_report` call, and the Tether catalog
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
- It never touches `~/.tangent/tangent.db`. The reference build always boots
  with `TANGENT_DB_PATH` inside the test's temp directory, on a port it
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

When the schema moved as well as the surface, `tangent --migrate-only` comes
first, and a drained backup comes before that.

## Manual curl probes

The gated command covers all of this; these remain for a machine with no Go
toolchain, or for probing something the harness cannot reach.

```bash
# Boot (unmanaged) — or just point at the running instance.
./tangent &
```

The MCP surface is mounted on the same port as the SPA:

- Streamable HTTP: `http://localhost:7842/mcp` (modern clients).
- SSE legacy: `http://localhost:7842/sse` (older Claude Code; long-lived event
  stream, and the transport the Tether gateway dials).

The streamable handler is **stateless + JSONResponse**, which is why one-shot
curl probes succeed without an `initialize` first. `/sse` is not like that: it
hands back an `endpoint` event naming a session-scoped POST URL, so probing it
by hand needs a real client — `make smoke` drives one.

### Probe 1 — tool catalog

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq '.result.tools | length'
```

Compare the answer with what the build on disk advertises rather than with a
number from a document:

```bash
TANGENT_DB_PATH=$(mktemp -d)/t.db TANGENT_HTTP_PORT=17842 ./tangent &
curl -fsS -X POST http://localhost:17842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | jq -r '.result.tools[].name' | sort
```

A difference between the two is `CATALOG_STALE`: the deployment is behind the
artifact. The name list groups into room/workflow and session compatibility
tools, generic durable surface/interaction tools, the 4 HITL inbox operations,
the definition-registry diagnostics, and the operability probes
(`tangent.health_report`, `tangent.telemetry_query`, `tangent.retention_status`).

### Probe 2 — the read-only tool call

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tangent.health_report","arguments":{}}}' \
  | jq '.result.structuredContent.readiness.status'
```

`tangent.health_report` is the smoke check's one read-only call, and the choice
is deliberate: every candidate proves the transport carried a call, and only
this one also answers whether the host can do the work. It writes nothing,
creates no room, and returns the same pass/warn/fail vocabulary `/readyz` and
`cerberus resource doctor` print. It is also the only one of the three health
probes an agent behind the gateway can reach — it speaks JSON-RPC and holds no
HTTP client for `/readyz`.

### Probe 3 — strict schema rejection

```bash
curl -fsS -X POST http://localhost:7842/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tangent.triage","arguments":{"envelope":{"v":1,"id":"smoke-1","type":"triage","data":{}}}}}' \
  | jq '.result'
```

Expected: `isError: true`, with the error identifying the invalid `type` before
any room is created. The valid tool pins `type: "tangent.triage"`.

These probes send no browser headers, so the same-origin guard on `/mcp` and
`/sse` passes them through unchanged. A probe that attaches to `/ws` needs a
participant session; see
[`mcp-integration.md`](./mcp-integration.md#room-access-and-caller-scope).

### Optional — MCP Inspector

```bash
npx @modelcontextprotocol/inspector
# Transport=Streamable HTTP, URL=http://localhost:7842/mcp.
```

### Shutdown

```bash
kill %1
```

Send SIGINT/SIGTERM to a pid you started. Never a pattern-matched kill: a
managed `tangent-dev` may be running against the real database on the same
machine.

## Related

- Cross-repository gateway contract smokes, opt-in and separate from this one:
  `go test -tags tether_smoke ./internal/mcp -run '^TestTetherNativeFlat'`.
  They build a sibling Tether checkout and drive the HITL and room-workflow
  contracts through it against a purpose-built upstream. This document's gated
  layer instead points the *installed* gateway at the *live* deployment.
- [`mcp-integration.md`](./mcp-integration.md) — client configuration and the
  cold-start recipe.
