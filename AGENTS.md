# Tangent

Tangent is an agent-summoned interactive surface: one Go binary on `:7842`
serving an MCP tool surface, per-room browser workflows, and the durable
`/hitl` operator inbox, with the React SPA embedded in the binary. An agent
summons it when chat is the wrong shape for the work. It is the
separate-window app surface only — inline modals, fast-triage popovers and
spatial canvases are deliberately somewhere else, and Tangent is not a task
tracker, agent launcher or session manager.
[ADR 0005](docs/adr/0005-product-boundary-and-portfolio-composition.md) is the
boundary it refuses to cross; read it before proposing that Tangent own
something new.

## Start Here

- `docs/architecture.md` — system shape and layers, and the canonical
  **Current limitations** list. Read it before designing anything.
- `docs/adr/` — accepted decision records. `0005` owns the product boundary,
  `0007` the collaboration surface, the plugin host and where view state
  lives. `0008` carries the plugin model — what the host is becoming, and what
  is still open — after `0007` §4 was narrowed to the boundary rule alone.
  `0009` reduces the renderer trust model to isolation. `0010` corrects `0007`
  §6: a plugin writing to the application it adapts is the pattern working,
  not an exception — the boundary `0007` §6 and this file protect is that
  Tangent's own binary takes no application dependency, not which direction a
  write travels. `0006` is superseded by `0007`; read `0007` instead.
- `cmd/tangent/main.go` — server entry point, flags and signal handling.
  `cmd/tangent-app/main.go` is the Wails desktop shell.
- `internal/server/static.go` — the `//go:embed all:ui_dist` directive. Vite
  writes to `internal/server/ui_dist/` so the embed resolves relative to the
  package.
- `internal/envelope/extensions/register_all.go` — the single table binding
  every envelope wire name; authored manifests live beside it under
  `packages/<package-id>/<kind>/`. One table, **two doors**: a row marked
  `contributedByPlugin` is installed by the plugin host, not by `RegisterAll`.
- `docs/writing-a-plugin.md` + `internal/plugintemplate/` — the scaffold every
  plugin after the second one starts from, and the eight-item classification it
  encodes. `go run ./cmd/tangent-new-plugin -package <name>` writes a plugin
  package that loads onto the host; it does not write the
  `cmd/tangent-plugin-<name>` program an installed plugin needs, and the guide
  says how. `internal/plugintemplate/example/` holds two committed renders,
  compiled and load-tested here but installed by nothing.
- `internal/pluginhost/` + `internal/plugins/installed.go` — the ADR 0007 §4
  plugin host and the loader for the plugins this Tangent has **installed**.
  There is no compiled-in roster: each plugin is its own
  program (`cmd/tangent-plugin-*` in tangent-plugins), installed into
  `~/.tangent/plugins/` with `tangent plugin install` (`make install-plugins`
  does the first-party ones), spawned through plugin-host Lifecycle over the
  protocol-2 plugin-sdk wire,
  restarted with backoff if it crashes, and gated on its health. `go list -deps ./cmd/tangent` names no
  plugin: the binary is domain-free by its dependency graph. The host resolves
  an ADR 0003 manifest for the kind a plugin names and refuses the registration
  without one; a plugin authors nothing about what its kind may do. ADR 0008 is
  the model — read a not-yet here against `0008` and a refusal against `0007`
  §4. Two surfaces extend the SDK's base contract, and both refuse by name
  rather than accommodate: `RegisterMCPTool` (`mcp.go`) contributes an
  agent-callable tool, and `RegisterHTTPRoute` (`http.go`) contributes a
  browser route under `/api/plugins/`. Both are recorded at load and installed
  later — plugins load before the MCP and HTTP servers exist.
- `pkg/plugin/` — the public surface a plugin is written against, and the only
  Tangent package a plugin imports. The host aliases its types; it must never
  import `internal/` (`TestPublicPluginSurfaceIsALeaf`).
- [`hollis-labs/tangent-plugins`](https://github.com/hollis-labs/tangent-plugins)
  — the first-party plugins (torque, tesseract, runner), one module each,
  pinned by `tangent-plugins.version`; `make install-plugins` installs that
  pinned version and `make smoke` measures it. Nothing in this repository knows Torque
  or Tesseract exists. The Torque plugin is the ADR 0007 §6 pattern working: a
  domain-free kind, a mechanical mapping in userland, one agent call in, and a
  sync button that costs no agent turn. It writes to Torque through its own
  client, with Torque's own authority — that is a plugin doing what a plugin is
  for, per [ADR 0010](docs/adr/0010-the-boundary-is-coupling-not-write-direction.md),
  not an exception to anything. Tesseract's plugin does the same for
  Tesseract, and the runner supervises agent processes for the agent-turns
  inbox. `plugin.TurnsEnqueueInputSchema` is the contract the runner is tested
  against, and `make smoke` boots the installed runner against the real tools.
- `internal/interaction/` + `internal/roomflow/` — the durable substrate and
  the compatibility adapter every room workflow routes through. The
  interaction is the canonical record; `rooms`/`envelopes` are a projection.
- `internal/room/connection.go` — multi-connection room lifecycle and the
  resolver lease. Nothing binds a room to one agent, caller or browser tab.
- `internal/authz/` — the capability matrix behind every route and caller.
- `internal/smoke/` — derives the MCP surface from the shipped binary rather
  than from a number written down; `docs_test.go` is the documentation gate.

## Commands

```bash
make build             # codegen → Vite build → embed → ./tangent
make test              # go test -race + vitest
make lint              # gofmt + go vet + golangci-lint + biome
make smoke             # boot the shipped binary; check the MCP surface and the docs
make verify-supported  # test + lint + check-envelopes under the pinned Node
```

`make verify-supported` is the gate to trust over a bare `make`: it pins the
Node runtime instead of trusting whatever the shell offers. Run `make smoke`
whenever the MCP surface, a bundled workflow, or a document that names tools
changes — it is what runs the documentation gate. `make generate-envelopes`
rewrites the committed `ui/src/generated/envelope-types.ts`, and
`make check-envelopes` is the CI staleness gate.

## Boundaries

**Never write a tool, envelope-kind or workflow count into prose.** That number
has been wrong in this repository far more often than right.
`internal/smoke/docs_test.go` is the documentation gate that enforces it, plus
two more rules over the document set its `documentedToolFiles` names: every
`tangent.<tool>` those documents mention must exist in the shipped surface, and
every shipped tool must appear in at least one of them. Adding a tool without
documenting it fails the build, and so does removing the last mention of one.
This file is in that set, and `TestNoDocumentPinsTheToolCount` reads
`CHANGELOG.md` as well, so both must stay readable.

`docs/architecture.md#current-limitations` is the canonical limitations list —
this file, `README.md` and `docs/developing.md` point there rather than keeping
copies, because the copy is what rots. One entry is load-bearing enough to
repeat: a `standalone-local` partition is advisory, not a security boundary.
Any local caller can assert any partition, enforcement exists only *across*
authorities, and nothing downstream may present a partition as isolation.

`.agents/skills/` is product surface, not this repo's agent configuration: it
holds the consumer-facing launcher skills for Tangent's own workflows, and
`README.md` and four documents link into it. Do not delete it as stray agent
configuration.

Nothing a plugin contributes is privileged. A kind goes through the same
manifest, trust classification and renderer isolation as every other; a
plugin-contributed **tool** is a shipped tool, so the documentation gate covers
it and a name it already serves is refused rather than shadowed; a plugin
**route** goes through `registerParticipantRoute` like every other browser API
route, so it carries the origin guard, the participant session and the ADR 0004
§7 capability check, and it appears in `ParticipantRoutes()` where the
capability check can see it. The participant's session cookie does not cross
into a plugin and `Set-Cookie` does not cross back out. Both kind doors must
stay inside the drift tests — `TestPackageTreeMatchesRegistrations`
covering only `RegisterAll` would narrow ADR 0003 §6's ownership guarantee to
half the registry without failing. `RegisterCRUDHandler` is deliberately
unimplemented: the owning application's agent or plugin is its client, and a
*host* surface that wrote to applications generically would mean Tangent
learning their schemas — the domain-free-binary property ADR 0005 protects,
not a rule against writes ([ADR 0010](docs/adr/0010-the-boundary-is-coupling-not-write-direction.md)).
Implementing a host surface because the SDK offers it, rather than because a
consumer needs it, is the specific way ADR 0007 says this boundary rots.

`tangent.app-board` is the first kind whose drafts are Tangent's own records
(`draft_custody: tangent-custodied`) rather than `localStorage`. Its view state
goes through the draft path — never the response path, which takes the resolver
lease and would let a second tab steal the right to answer from the first. A
staged card move is view state as well: it lives in the draft and changes
nothing in the owning application until the participant presses Sync, so a
board abandoned with staged changes has changed nothing. The press is the
decision; do not make a draft into one.

A plugin reaches Tangent as an ordinary local MCP client against `/mcp`
(`pkg/plugin/hostclient`), with the same authority any local MCP caller
has and no more: Tangent offers no reverse RPC on its base-profile wire, and
the plugin resolves to the same host-assigned identity — see
[ADR 0010](docs/adr/0010-the-boundary-is-coupling-not-write-direction.md) §5.
`pluginhost.ToolCaller` remains the in-process form of the same thing.
Reach for a new typed host method only when a tool genuinely cannot
express the need; `GetService` stays unimplemented.

Reviewed plugin configuration is host-owned under CW-20261003-0063. Scalars
are persisted in a private settings database; declared secrets live in the OS
keychain and appear in browser snapshots only as presence. Each load receives
a detached scoped snapshot through Init.Config. Global `GetConfig`, `SetConfig`
and `RegisterConfigSchema` remain refused; current owner handles may access only
their reviewed keys, and schema registration cannot widen the manifest. Save is
revision-CAS protected; explicit apply/restart checks that revision before
replacing the current owner. See [plugin configuration](docs/plugin-configuration.md).
`Unload` fences the exact load owner's handle and dispatches, withdraws its
registrations, and tears down its subprocess. Core definitions and other owners
remain; retained material for pinned interactions is not erased. Enable intent
is a separate boolean-only store, not plugin configuration. The live MCP and
HTTP registries resolve the current owner, and budget exhaustion quarantines
that owner before teardown. Caller cancellation does not quarantine it.

A change that moves a manifest's `contract_digest` is a **version** bump, never
a `revision` bump — ADR 0003 §3, and `revision` is a non-semantic edit counter
within a version. `contractLock` in
`internal/envelope/extensions/contract_lock_test.go` is the committed record
that makes it a gate rather than a rule nobody checks; do not edit it to silence
a §3 finding. `docs/developing.md` carries the procedure, and
`docs/architecture.md#current-limitations` carries the §8 C1 blast radius a bump
still has.

Keep the HTTP layer separate from app logic, and never import `cmd/tangent`
from `internal/...` — the dependency is one-way. The transports
(`internal/server`, `internal/mcp`, `internal/ws`) call services, never
`internal/db` or raw SQL: `.golangci.transport.yml` is that gate, at hard block
in `make lint` and CI, and a new transport directory has to be added to it.
Wails is a real dependency
today, confined to `cmd/tangent-app` and `internal/appshell`; that binary
carries no `go:embed` of its own because the server already holds `ui_dist`.

There is no plugin sandbox, importmap or shared-chunk pattern here, and adding
one needs an explicit decision rather than drift. Renderer trust classes and
presentation sandboxing (`docs/renderer-trust-classes.md`) are the shipped
isolation model; they are not a plugin system.

The `hollis-labs` Go modules are public and resolve from a clean clone with no
`replace` directives; CI still sets `GOPRIVATE`/`GONOSUMDB` and a `GH_PAT` URL
rewrite from when `go-envelopes` was private. Frontend dependencies stay
minimal — editor, chart and DnD libraries get added when a real workflow needs
one, not preemptively. The top-level `LICENSE` is sufficient; do not add
per-file license headers.

Open a pull request for changes; a maintainer will review it. See
`CONTRIBUTING.md`.
