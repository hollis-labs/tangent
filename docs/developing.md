# Developing Tangent

Onboarding for contributors and future-you. For the system shape see
[`architecture.md`](./architecture.md). For the user-facing MCP setup
see [`mcp-integration.md`](./mcp-integration.md).

## Prerequisites

- **Go 1.26.1** (matches `go-envelopes`; see `go.mod`).
- **Node 22.12.0**. The repo pins this via [`mise.toml`](../mise.toml);
  run `mise install` after cloning.
- **`lefthook`** for the pre-commit / pre-push hooks:
  `brew install lefthook`.
- Optional: `golangci-lint` (lint target skips it cleanly if missing),
  `jq` (for the curl probes in `mcp-smoketest.md`).

## First-time setup

```bash
git clone git@github.com:hollis-labs/tangent.git
cd tangent
mise install
make install-hooks      # lefthook install — sets up pre-commit/pre-push
cd ui && npm install && cd ..
```

## Dev loop

```bash
make dev
```

Runs Vite (`:5173`) and the Go server (`:7842`) in parallel. The Go
server proxies non-API requests to Vite, so HMR works as if the SPA
were standalone, and the same routes you'll hit in production are
reachable on `:7842` during dev.

For a production-shaped run (no Vite proxy, embedded build):

```bash
make build              # frontend build → embedded into Go binary → ./tangent
./tangent
```

## Tests

```bash
make test               # Go (-race) + vitest
make test-go            # Go only
make test-frontend      # vitest only
```

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

## Lint

```bash
make lint               # Go (gofmt + vet + golangci-lint) + frontend (biome)
make lint-go
make lint-frontend
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
go-envelopes registry) and the Wails-deferral note, see
[`architecture.md`](./architecture.md).

For how a room workflow states what it still needs, and how it reports a
submission the server refused, see
[`room-validation-affordances.md`](./room-validation-affordances.md).

## Known limitations (v0.6)

See [`CHANGELOG.md`](../CHANGELOG.md) Security section. Headlines:

- Localhost only, single-user. Object access is scoped (ADR 0004), but loopback
  admission is not authentication: a hostile local process running as the same
  user can still mint a browser participant session.
- `standalone-local` caller partitions are advisory, not a security boundary.
  Any local caller can assert any partition; isolation is enforced only across
  authorities. See
  [`architecture.md`](./architecture.md#room-access-and-caller-scope).
- One active pending envelope per room.
- The production server's tool surface is room/workflow compatibility tools,
  generic durable interaction tools, 4 strict HITL inbox operations, the
  definition-registry diagnostics, and the operability probes. The count is
  deliberately not recorded here — it has been wrong in this repository's docs
  more often than right. `make smoke` derives it from the build and reports any
  hop that disagrees; see [`mcp-smoketest.md`](./mcp-smoketest.md).

## Persistence layer

Local state now lives in SQLite at `~/.tangent/tangent.db`
(`TANGENT_DB_PATH` overrides).

Useful commands:

```bash
make db-migrate
make db-rollback
sqlite3 ~/.tangent/tangent.db
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

## Adding a new workflow

A workflow is one envelope kind plus an MCP tool that creates a room
and dispatches it. Follow the steps above; `tangent.triage`,
`tangent.feedback`, `tangent.form-collect`, `tangent.design-iteration`,
tangent.whiteboard`, `tangent.spreadsheet-review`,
`tangent.approval-queue`, and the writing
workflow kinds are the worked examples. If the workflow
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
