# Developing Tangent

Onboarding for contributors and future-you. For the system shape see
[`architecture.md`](./architecture.md). For the user-facing MCP setup
see [`mcp-integration.md`](./mcp-integration.md). To add a plugin, start from
the scaffold — [`writing-a-plugin.md`](./writing-a-plugin.md).

## Prerequisites

- **Go** at the version `go.mod`'s `go` line names, or newer.
- **Node 22.12.0**. The repo pins this via [`mise.toml`](../mise.toml);
  run `mise install` after cloning.
- **`lefthook`** for the pre-commit / pre-push hooks:
  `brew install lefthook`.
- Optional: `golangci-lint` (lint target skips it cleanly if missing),
  `jq` (for the curl probes in `mcp-smoketest.md`).

## First-time setup

```bash
git clone https://github.com/hollis-labs/tangent.git
cd tangent
mise install
make install-hooks      # lefthook install — sets up pre-commit/pre-push
(cd ui && npm ci)
```

A `go install …/cmd/tangent@<version>` builds a binary with no web UI —
`internal/server/ui_dist/` holds only a placeholder in git, and the SPA is
embedded by `make build` — so it is not an install path.

## Dev loop

```bash
make dev
```

Runs Vite (`:5173`) and the Go **dev** server (`:7843`) in parallel. The Go
server proxies non-API requests to Vite, so HMR works as if the SPA
were standalone, and the same routes you'll hit in production are
reachable on `:7843` during dev.

### Two instances: stable and dev

The daemon's own defaults (`:7842`, `~/.tangent/tangent.db`) belong to the
**stable** install the operator uses every day. Nothing agent-facing rewires
for the split: agents configured for `http://127.0.0.1:7842/mcp` keep reaching
stable. Point an agent at dev explicitly (`http://127.0.0.1:7843/mcp`) rather
than registering both under the same server name, since identical tool names
from two servers collide in most MCP clients.

Every dev-facing `make` target (`dev`, `dev-go`, `db-migrate`, `db-rollback`,
and the environment-coupled arm of `smoke`) runs the **dev** instance instead,
by exporting the two existing overrides:

| Variable | Dev value | Set by |
|---|---|---|
| `TANGENT_HTTP_PORT` | `DEV_PORT`, default `7843` | `make` |
| `TANGENT_DB_PATH` | `DEV_DB_PATH`, default `<repo>/.tangent/dev.db` | `make` |

`.tangent/` is gitignored and removed by `make clean`. A different database
path is a different `.owner` lock file, so dev and stable can never collide on
the single-writer flock or on the port; two binaries at different migration
levels never share a database, which is the failure this layout prevents
(`bab0b89` exists because it happened once).

`./tangent` with no environment still means the stable defaults. Do not run it
by hand while a supervisor owns that instance; see
[`mcp-integration.md`](./mcp-integration.md#managed-runtime-single-launch-authority)
for who supervises what on the reference machine.

For a production-shaped run (no Vite proxy, embedded build):

```bash
make build              # frontend build → embedded into Go binary → ./tangent
./tangent
```

## Frontend design rules

Biome remains the primary frontend linter. Install the separate, locked design
rule tooling with `npm ci --prefix ui/tools/design-lint`, then run
`npm --prefix ui run check:design` under the pinned Node runtime. The frontend CI
job runs this ratchet in addition to Biome. See
[`ui/tools/design-lint/README.md`](../ui/tools/design-lint/README.md) for the
parser isolation, exit codes, and deliberate baseline update procedure.

## Tests

```bash
make verify-supported   # test + lint + check-envelopes under the pinned Node runtime
make test               # Go (-race) + vitest
make test-go            # Go only
make test-frontend      # vitest only
make smoke              # boot the shipped binary; derive and check the MCP surface
```

`make verify-supported` is the gate to trust: it runs everything through
`mise --no-config exec node@22.12.0` rather than whatever Node the shell
happens to have.

`make smoke` builds `./cmd/tangent`, boots it on a reserved port against a
database in a temp directory, and checks direct `/mcp`, legacy `/sse`, one
read-only tool call, and the three health probes. It touches no live instance
and no shared catalog, and because `internal/smoke` carries no build tag,
`go test ./...` runs it too. `TANGENT_SMOKE_ENV=1 make smoke` also checks a live
deployment — see [`mcp-smoketest.md`](./mcp-smoketest.md).

The smoke suite fetches and builds the exact public first-party source commit
in `tangent-plugins.smoke-ref`, including its manifest-v2 declarations. CI uses
the same default. `TANGENT_PLUGINS_SRC=<checkout>` is an explicit development
override. This source fixture does not change the separate operator install
pin in `tangent-plugins.version`, which still names legacy declarations;
`make install-plugins` requires a compatible release pin before activation.

Notable suites:

- `internal/server/integration_test.go::TestIntegration_TriageRoundTrip`
  — in-process e2e using the MCP SDK's in-memory transports. Canonical
  proof the MCP → WS → response loop closes.
- `ui/src/components/envelopes/Triage.test.tsx` — frontend behavior for
  the bundled triage component.
- `scripts/triage-mock-call.mjs` — Node-from-the-outside variant. Not
  in CI; used to verify the wire shape with no LLM.
- `scripts/whiteboard-mock-call.mjs` — end-to-end whiteboard submit ->
  reopen -> continue verification, including revision lineage.
- `scripts/spreadsheet-review-mock-call.mjs` — end-to-end
  spreadsheet-review submit -> reopen -> cancel verification, including
  saved views and export metadata.
- `scripts/approval-queue-mock-call.mjs` — end-to-end approval-queue
  submit -> reopen -> cancel verification, including audit export refs.
- `scripts/form-collect-mock-call.mjs` — end-to-end form-collect submit
  verification, including attachment refs and explicit action capture.
- `internal/smoke/` — boots the *shipped binary* and derives its MCP surface,
  so no expected tool count is ever written down. `docs_test.go` in the same
  package is the documentation gate: it fails when a document names a tool the
  build does not serve, omits one it does, or writes a count that has drifted.

## Lint

```bash
make lint               # Go (gofmt + vet + golangci-lint) + frontend (biome)
make lint-go
make lint-frontend
```

`make lint-go` also runs `.golangci.transport.yml`, the transport-boundary gate
from the Hollis Labs service-layer standard. The transports —
`internal/server`, `internal/mcp` and `internal/ws` — call services and never
import `internal/db` or run raw SQL; a service such as `internal/retention` or
`internal/health` sits between them and the database, and `internal/boot` wires
it. The gate is at hard block, in CI as well: any finding fails. A new
transport directory has to be added to both rules in that file, or it goes
unchecked.

```bash
GOWORK=off golangci-lint run --config .golangci.transport.yml ./...
```

## Conventions

- **Branches.** `feat/...`, `fix/...`, `docs/...`, `chore/...`. The
  v0.1 cycle used `feat/v01-NN-<slug>` per PR; future cycles can drop
  the prefix or keep it as taste dictates.
- **Commits.** Conventional-commits-ish. Subject prefix matches the
  area (`feat(mcp): ...`, `fix(ws): ...`, `docs: ...`, `chore(ci): ...`).
  Keep subject ≤ 72 chars; body explains the *why*.
- **PRs.** One logical change per PR. Test plan in the description.
  Review fixes go in their own commit (`fix(prN): address Copilot review
  feedback` is the v0.1 pattern).

## Operating model

Tangent follows the portfolio's adapted twelve-factor direction. These are
engineering conventions, not aspirations — a change that breaks one of them is
a change that needs an argument. The boundary they serve is
[ADR 0005](./adr/0005-product-boundary-and-portfolio-composition.md).

- One version-controlled codebase produces versioned binaries and packages for
  many deployments.
- Go and frontend dependencies are explicitly declared and isolated. Optional
  renderer helpers are registered capabilities, not ambient assumptions about
  globally installed tools.
- Configuration is external and contains no secret values. Environment
  variables bind deployment-specific values and credential references.
- SQLite or another database, artifact stores, identity providers, credential
  brokers, Tether gateways, and external source systems are attached resources.
- Build creates an immutable Go binary, embedded frontend, and verified
  renderer assets; release binds them to configuration and registrations; run
  executes that release.
- Processes are disposable. Durable surfaces, interactions, drafts,
  resolutions, and delivery obligations live in an attached stateful store.
- Tangent embeds its own HTTP server and binds an explicitly configured port or
  local endpoint.
- Concurrency scales through durable claims and a coordinated store rather than
  larger process-local room maps or competing uncoordinated writers.
- Startup is deterministic, shutdown is graceful, and in-flight interactions
  and resolution deliveries have explicit recovery semantics.
- Development, test, and production exercise the same definition, renderer,
  identity, persistence, and transport boundaries.
- Logs are structured event streams to stdout and stderr; durable audit and
  interaction history use application stores.
- Migrations, backup, restore, definition validation, cache repair, export,
  retention, and reconciliation run as one-off processes from the same release
  — see [`database-operations.md`](./database-operations.md).

Go-specific conventions follow the broader Hollis Labs engineering direction:

- transports depend inward on application contracts
- domain and application packages do not depend on HTTP, MCP, WebSocket, Wails,
  SQLite, or a particular plugin transport
- constructors validate required dependencies and fail fast
- contexts carry cancellation and deadlines, not optional service dependencies
- interfaces are consumer-owned and intentionally narrow
- errors remain typed across adapters
- process-global mutable state is avoided
- generated code records its source and is reproducibly checked
- official Go clients, SDKs, and CLIs are preferred before bespoke protocol
  implementations

## Architecture pointer

For the system layers (HTTP, MCP, WS bridge, envelope dispatcher,
go-envelopes registry, definition registry, connection lifecycle,
authorization, renderer trust classes, health, and telemetry) and the
Wails-deferral note, see [`architecture.md`](./architecture.md). The six
accepted decision records are in [`adr/`](./adr/).

For how a room workflow states what it still needs, and how it reports a
submission the server refused, see
[`room-validation-affordances.md`](./room-validation-affordances.md).

## Known limitations

**The canonical list is
[`architecture.md`](./architecture.md#current-limitations).** It is kept in one
place on purpose: a limitation copied into four documents rots in three of
them, which is precisely how the direction document this repository retired
came to mislead its readers. What follows is the contributor-facing subset.

- Localhost only, single-user. Object access is scoped (ADR 0004), but loopback
  admission is not authentication: a hostile local process running as the same
  user can still mint a browser participant session.
- `standalone-local` caller partitions are advisory, not a security boundary.
  Any local caller can assert any partition; isolation is enforced only across
  authorities. See
  [`architecture.md`](./architecture.md#room-access-and-caller-scope). **Do not
  build a trust assumption on one, and do not let a downstream product present
  one as isolation.**
- One active pending envelope per room. An overlapping `session_advance` is
  refused with `SESSION_BUSY` rather than queued.
- **No definition declares a host-mediated effect capability.** Every request
  through `POST /api/effects` refuses `effect_capability_undeclared`, so
  `internal/effect` is covered by tests and has zero production traffic. If you
  are changing it, you are the first caller (`CW-20260905-0010`).
- **`clipboard.write` and `export.download` are enforced only inside a
  sandboxed frame**; on the main origin they remain declared-not-enforced.
  `network.fetch` is genuinely enforced by the document CSP.
- **There is no browser in CI.** The CSP and the frame sandbox are verified by
  unit tests over the emitted policy and by construction, never by observing a
  browser refuse anything. A green CI run is not evidence of enforcement —
  [`manual-tests/renderer-sandbox-e2e.md`](./manual-tests/renderer-sandbox-e2e.md)
  is (`CW-20260904-0171`).
- **The OpenTelemetry bridge has never been run against a collector**
  (`CW-20260905-0011`).
- **`SaveDraft` has no production caller**; browser `localStorage` is the only
  running draft custody (`CW-20260905-0001`).
- **`renderer.entry` loads nothing**; `ui/src/main.tsx` registers renderers by
  string literal (`CW-20260905-0004`).
- **The ADR 0002 §3 custody-precedence engine is not implemented**; retention
  uses host windows only (`CW-20260905-0008`).
- The production server's tool surface is room/workflow compatibility tools,
  generic durable interaction tools, the strict HITL inbox operations, the
  definition-registry diagnostics, and the operability probes. The count is
  deliberately not recorded here — it has been wrong in this repository's docs
  more often than right. `make smoke` derives it from the build and reports any
  hop that disagrees; see [`mcp-smoketest.md`](./mcp-smoketest.md).

## Persistence layer

Local state lives in SQLite. The stable install uses `~/.tangent/tangent.db`;
the dev instance uses `<repo>/.tangent/dev.db` (`TANGENT_DB_PATH` selects;
see "Two instances" above).

Useful commands (all against the **dev** database):

```bash
make db-migrate
make db-rollback
sqlite3 .tangent/dev.db
```

When you add a migration, create matching files in
`internal/db/migrations/`:

- `000N_name.up.sql`
- `000N_name.down.sql`

Migrations are embedded into the Go binary, so every schema change must
land with both directions present.

## Adding a new envelope kind

The current precedents are `triage`, `feedback`, `form-collect`,
`design-iteration`, `whiteboard`, `spreadsheet-review`, `approval-queue`, `interview-question`,
`synthesis-notes`, `block-draft`, `prose-revision`, and
`output-render`. Use at least one simple workflow, one room-backed
structured surface (`form-collect`, `whiteboard`, or
`spreadsheet-review` or `approval-queue`), and one
multi-phase writing workflow as references instead of assuming one
shape fits every kind.

**Go (registration + handler):**

1. Define the envelope type in `internal/envelope/extensions/<kind>.go`,
   following `triage.go`. Register it via the plugin extension API so
   `go-envelopes` validates it, then add one line to the
   `registrations` table in `internal/envelope/extensions/register_all.go`.
   That table is the only registration list: `extensions.RegisterAll`
   backs both `cmd/tangent` and `cmd/tangent-dump-types`, so the kind
   reaches the server and the TypeScript codegen from a single edit.
   The authored **definition manifest** and its schema files live beside the
   kind at `internal/envelope/extensions/packages/<package-id>/<kind>/`
   (`manifest.yaml` plus the schemas it names, embedded as one tree). The
   package id is the ADR 0003 §6 ownership assignment made mechanical; see
   [ADR 0003](./adr/0003-definition-and-package-ownership.md) before inventing
   a new package.
2. If the kind drives an MCP tool, add the tool wiring under
   `internal/mcp/` — see `triage_handler.go` and `triage_schema.go`
   for the hand-rolled JSON Schema pattern. The handler either creates a
   room directly or routes through the session substrate, sends the
   envelope through the WS bridge, and returns the user's response as
   the MCP tool result.

**Frontend (rendering):**

3. Add the React component at
   `ui/src/components/envelopes/<Kind>.tsx`. Match the `<Triage>`
   shape: take a typed envelope prop, render the UI, call the
   submit/cancel callbacks the registry provides.
4. Register the component in `ui/src/lib/envelope-registry.ts` so
   `EnvelopeRouter.tsx` knows to render your component when an envelope
   of that `type` arrives. The registry mirrors the Go-side plugin
   extension pattern.
5. Codegen TS types: `make generate-envelopes`. Commit the regenerated
   `ui/src/generated/envelope-types.ts`. CI fails the build if it's
   stale (`make check-envelopes`), and the gate now covers every
   Tangent-owned kind, not just the upstream core catalog.

## Changing a kind that already ships

A manifest carries two numbers and they are not interchangeable. ADR 0003 §3:
`revision` may advance within a `version` only while `contract_digest`,
`renderer.class`, `renderer.isolation` and `required_capabilities` all hold.
Anything else is a new `version`. Adding an optional field to a request schema
moves `contract_digest`, so it is a version bump — an optional field is still a
contract change, and the number that tells a reader the contract moved is the
one on the wire.

`TestNoRevisionAdvancedWhileTheContractMoved` in
`internal/envelope/extensions/contract_lock_test.go` is the gate, and
`contractLock` beside it is the committed record of what each kind last
published. A build has no history, so the previous identity has to be written
down for "the contract moved" to mean anything — the same reason the trust-class
table in `drift_test.go` is written out kind by kind. Two failures come out of
it and the message says which:

- **A §3 violation.** Bump `version` in the manifest. Do not edit `contractLock`
  to match — that is the gate reporting the finding it exists for. A new version
  starts its revision count back at 1.
- **A stale lock.** The kind moved legally and the table has not caught up. The
  failure prints the literal to paste.

There is deliberately no target that rewrites `contractLock`. A baseline a
command can refresh is a baseline that gets refreshed instead of read.

The one exception §3 grants is the once-per-kind
`compatibility_response_schema: absent` → `present` backfill, which
`CheckRevisionAdvance` honors from the marker rather than hard-failing. It
covers `contract_digest` and nothing else.

Budget for the blast radius before you bump. Under ADR 0003 §8 C1 a registry
change that alters the current binding makes the pinned definition `unavailable`
for **new submissions**, so a version bump takes pending interactions of that
kind out of service. Narrowing C1 for an additive-classed bump is a known open
question, not something this gate decided.

## Adding a new workflow

A workflow is one envelope kind plus an MCP tool that creates a room
and dispatches it. Follow the steps above; `tangent.triage`,
`tangent.feedback`, `tangent.form-collect`, `tangent.design-iteration`,
`tangent.whiteboard`, `tangent.spreadsheet-review`,
`tangent.approval-queue`, `tangent.wizard`, and the writing
workflow kinds are the worked examples. Every named room workflow routes
through the shared completion adapter in `internal/roomflow`; do not add a
parallel wait path. If the workflow
is multi-step,
prefer reusing the `tangent.session_*` substrate and the room phase
state rather than inventing a parallel room lifecycle.

Before writing the SPA component, read
[`room-validation-affordances.md`](./room-validation-affordances.md). It
carries the conventions every room follows for required fields, blocked
terminal CTAs, and the split between a *blocked* submission (the operator
still owes something; amber, `role="status"`, owned by the workflow) and a
*refused* one (the server declined; red, `role="alert"`, owned by
`ConnectionStatus`). `ApprovalQueue.tsx` is the worked exemplar.

## Migration note

If you are coming from the archived Fast-Triage repo or skill, start
with [`migrating-from-fast-triage.md`](./migrating-from-fast-triage.md).
