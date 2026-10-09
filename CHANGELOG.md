# Changelog

All notable changes to Tangent are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [v0.17.0] - 2026-10-09

### Changed

- Adopt published monorepo modules: `libs/plugin-mcp` v0.1.1 for the SDK,
  lifecycle driver and MCP transports, `libs/ui-go` v0.1.0 for envelopes, and
  `substrate/mesh` v0.1.0 for the Tether client. Plugin scaffolds use the same
  package paths. Go 1.26.8 is required; the toolchain pin is 1.26.9.
  Generated envelope provenance reports the containing `ui-go` module.
  Uncooperative plugin teardown retains its graceful-unload failure even when
  the child is killed and reaped. CI builds compatible first-party sources
  from `3efe7d72b70b23e0fe8d377d6c5e44fd617ba8f1`.
  No installed plugins or daemon are changed.

- Subprocess plugins use plugin-host Lifecycle for protocol-2 handshake,
  process groups, bounded teardown, crash recovery and health gating. Tangent
  keeps manifest registration, authorization and dispatch policy. Unexpected
  runtime exits retain bounded recovery; handshake mismatches are terminal.
- **Breaking:** protocol-1 plugin binaries are refused. Plugins must rebuild
  against the pinned protocol-2 SDK and acknowledge capability contract 1.
  Init sends a fresh incarnation, empty grants and empty config, with no offers.
  The SDK and driver use the published plugin-mcp module. Plugin installation
  and daemon activation remain separate; nothing is deployed by this change.

- **`tangent-plugins.version` pins the protocol-2 rebuild** at
  `v0.1.2-0.20261004010850-eac2e08457ba` for runner, tesseract and torque.
  This older operator pin is retained; its declarations do not satisfy the
  current manifest-v2 loader. The separate CI source fixture rebuilds all four
  plugins with the matching SDK. No installation pin is advanced here.

- The empty plugin registry proof endpoint uses SDK registry v2 and shares the
  lifecycle host epoch. The current browser registry-v1 loader refuses this
  empty snapshot; loader/catalog adoption is deferred and app boot is unaffected.

- Preserve the earlier v0.1.1 first-party update: the runner validates turns
  against `plugin.TurnsEnqueueInputSchema`; tesseract and torque changed only
  their version. The protocol-2 source pin above supersedes that install pin.
- **Activation order:** select a compatible first-party release, rebuild
  protocol-2 plugins from that tag, then install those plugins before or
  together with the new daemon. Until then protocol-1 binaries are refused,
  `/readyz` reports `plugins: fail`, and the installer exits non-zero after its
  30-second readiness wait. Runner tools remain unavailable until reinstalled.
- Requests and replies are limited to 8 MiB, including the frame newline. An
  over-cap reply is currently dropped; the caller waits for its 30-second budget.

### Fixed

- Plugin diagnostic scrubbing normalizes common credential encodings while
  preserving unrelated text, physical newlines and following URL/log fields.
  Credential names use their last underscore/hyphen/camelCase segment; compound
  `key` names need a credential prefix. Bare `key`/`auth`/`pwd`/`pass` values are
  scrubbed without consuming following diagnostics; absolute `PWD` paths remain
  readable. Quoted names must be identifier-shaped, preserving causes after URLs.
  The pinned tail drops a possibly partial leading line only when non-blank text
  follows the first LF, and repeats the rule after a redaction-growth trim.
  CR-only separators are not line boundaries; exact-boundary cuts may drop a
  complete line. Windows without a qualifying LF can retain a partial key.
  Redaction remains best effort; plugins must not log secrets. Bare-colon failure
  prose stays readable; controls are sanitized only for display. Runtime failures and initial refusals are reported
  separately, and failed restarts warn once on status/inventory sampling.
- Relative plugin roots and spawn paths resolve to absolute directories.

- **A LaunchAgent reinstall no longer drops the daemon's extra environment**
  (`CW-20260930-0104`). `tangent-launchagent install` and `tangent-install
  install` keep every variable the installed plist already carries beyond
  port, database and plugin directory. Those are how a launchd-started plugin
  gets its settings, e.g. `TANGENT_TESSERACT_NAMESPACES`. `--env KEY=VALUE`
  (repeatable) adds or overrides one, and `tangent-launchagent install
  --replace-env` writes exactly what is given. `tangent-launchagent` also gains
  `--plugin-dir`, which `Config` already supported.

## [v0.16.0] - 2026-09-30

The first-party plugins leave the repository. Tangent is now the host,
`pkg/plugin` and the scaffold; the plugins ship from
[hollis-labs/tangent-plugins](https://github.com/hollis-labs/tangent-plugins),
and this release publishes the `tangent.turns_enqueue` schema the runner tests
against.

### Changed

- **The first-party plugins live in
  [hollis-labs/tangent-plugins](https://github.com/hollis-labs/tangent-plugins)**
  (`CW-20260930-0102`). `runner`, `tesseract` and `torque` are one Go module
  each there, released as `runner/v0.1.0`, `tesseract/v0.1.0` and
  `torque/v0.1.0` — extracted from this repository at v0.15.0 with behavior
  unchanged, so an installed plugin upgrades in place. This repository keeps the
  host, `pkg/plugin`, and the scaffold.
- **`tangent-plugins.version` pins the plugin release** this Tangent documents
  and tests against. `make install-plugins` installs exactly that release
  (`go install …@<version>`, then `--manifest` and `tangent plugin install`),
  and `make smoke` installs it the same way, so the documentation gate still
  measures Tangent plus its first-party plugins. `TANGENT_PLUGINS_SRC=<checkout>`
  builds from a local tangent-plugins checkout instead. `make build-plugins` is
  removed; there is no plugin source here to build.

### Added

- **`plugin.TurnsEnqueueInputSchema`**, the schema `tangent.turns_enqueue`
  accepts, published so a plugin that enqueues turns can test against it.
  `TestTurnsEnqueueAdvertisesThePublishedSchema` holds it equal to the packaged
  agent-turn request schema and to what the tool advertises.
- **`TestInstalledRunnerCarriesATurnThroughTheInbox`** (`make smoke`): the
  installed runner, with `cat` as the agent, carries one turn through the real
  tools and the browser API and back. It replaces the in-process runner tests
  that left with the runner.

### Removed

- `cmd/tangent-plugin-*`, `internal/plugins/{runner,tesseract,torque}`, and
  `TestInTreePluginsImportOnlyThePublicSurface`, which only applied while the
  plugins were in-tree; Go's internal rule now enforces it.

## [v0.15.0] - 2026-09-30

The public plugin surface. Application plugins can now live outside this
repository: everything a plugin needs from Tangent is in `pkg/plugin`, the
first step of moving the first-party plugins to
[hollis-labs/tangent-plugins](https://github.com/hollis-labs/tangent-plugins).

### Added

- **`pkg/plugin` and `pkg/plugin/hostclient`.** The plugin-facing API, as a leaf
  package (standard library plus `plugin-sdk/subprocess` only): `ToolCaller`,
  `ToolResult`, `ErrToolCallerUnavailable`, `MCPTool`, `HTTPRoute`,
  `RoutePrefix`, `ToolNamespace`, `Capability` with `CapabilityView` and
  `CapabilityDraft`, `Manifest` with `Validate`, `ToolDecl`, `RouteDecl`,
  `ManifestName`, and `AgentTurnContractVersion`. `hostclient` is the MCP client
  a subprocess plugin uses to reach Tangent. The definitions moved here, and
  `internal/` aliases them, so host code is unchanged. It follows the Cerberus
  pattern: the wire protocol comes from `plugin-sdk`, and host-specific types
  come from the host's own `pkg/`. `plugin-sdk` stays host-neutral.
- **`TestPublicPluginSurfaceIsALeaf`** fails if `pkg/plugin` ever reaches a
  `tangent/internal` package. Go does not enforce that: a `pkg/` file may import
  `internal/`, and an external plugin would then silently compile host
  internals.

### Changed

- The in-tree plugins (runner, tesseract, torque) and the plugin scaffold
  import `pkg/plugin` instead of `internal/*`. `pluginpkg`'s compatibility check
  is now the function `pluginpkg.CheckCompatible(m)`.

## [v0.14.0] - 2026-09-30

The plugin cycle, and the public repository. Tangent gains a plugin host whose
application plugins are installed subprocesses rather than code linked into the
binary, the two application plugins that shaped it (Torque and Tesseract), the
cooperative relay and channel substrate, and an agent-turns inbox. It closes
with the preparation for the repository going public on 2026-09-30:
clone-and-build install, no local `replace` directives, CI green end to end,
and the transport-boundary gate from the Hollis Labs service-layer standard.

### Added

- **Plugin host (`internal/pluginhost`, `internal/plugins`).** Tangent
  implements the portfolio plugin framework's `Host` contract
  (`github.com/hollis-labs/plugin-sdk`), so a new interaction kind can arrive
  without editing Tangent's own registration table. The rule, from
  [ADR 0007](docs/adr/0007-collaboration-surface-plugin-host-and-view-state.md)
  §4: the SDK says what a plugin may offer, the ADR 0003 manifest says what the
  host will let it do, and **a registration without a manifest is refused**. A
  plugin names a kind; it cannot author that kind's trust class, capabilities,
  assurance or digests, and a component that tries is refused by name rather
  than silently downgraded. `RegisterMCPTool` and `RegisterHTTPRoute` let a
  plugin contribute an agent-callable tool and a browser route under
  `/api/plugins/`; `RegisterCRUDHandler` is deliberately unimplemented, and
  `register_all.go` keeps one row per kind with a column saying which door it
  comes through, so the drift tests walk both.

- **Plugins are installed subprocesses (`CW-20260911-0070`).** Application
  plugins are separate programs (`cmd/tangent-plugin-*`) speaking the
  plugin-sdk JSON-RPC wire over stdin/stdout, installed into
  `~/.tangent/plugins/` with `tangent plugin install <dir>` / `list` / `remove`,
  and spawned by the host at startup; `make install-plugins` builds and installs
  the first-party ones. There is no compiled-in roster: `go list -deps ./cmd/tangent`
  names no plugin, so the binary is domain-free by its dependency graph rather
  than by review. A crashed plugin is restarted with exponential backoff (1 s to
  30 s, at most three attempts), and its calls are gated on its health; a plugin
  reaches Tangent back as an ordinary local MCP client
  (`internal/pluginpkg/hostclient`). [ADR 0008](docs/adr/0008-the-plugin-model.md)
  records the model.

- **Torque board plugin (`tangent.plugin.torque`).** `tangent.torque_open_board`
  queries Torque with its own list filters, shapes the tasks into a
  `tangent.app-board` and opens a room; a staged card move is view state until
  the participant presses Sync, which applies the moves through Torque's API with
  no agent turn (`tangent.torque_sync_board` is the agent-side equivalent). A
  bounded card set says it was bounded.

- **Tesseract review plugin (`tangent.plugin.tesseract`).**
  `tangent.tesseract_review` recalls Tesseract records into a board whose
  columns are the lifecycle statuses; Sync deprecates what the participant
  retired and hands promotions, rewords and supersedes back to the agent as a
  work list (`tangent.tesseract_review_sync`), because a Tesseract revision is
  immutable except for deprecation. See the Changed entry below for its
  namespace default.

- **Agent runner plugin (`tangent.plugin.runner`).** Runs an agent either as an
  embedded subprocess (streaming stdio, JSON-RPC, PTY, ACP) or through a Tether
  daemon session, sieves the stream down to conversational turns, enqueues them
  to the agent-turns inbox, and carries the operator's answer back to the agent.

- **Plugin scaffold.** `go run ./cmd/tangent-new-plugin -package <name>` writes
  a plugin that loads, from two presets (`application` fills an existing kind;
  `kind` contributes one) that the generator refuses to combine.
  [`docs/writing-a-plugin.md`](docs/writing-a-plugin.md) carries the
  classification it encodes.

- **Plugin registry endpoint.** `/api/plugins/registry` serves a
  `registry.Response` and the SPA loads it through
  `@hollis-labs/plugin-registry`. A minimal proof: the registry is empty and no
  plugin ships browser contributions through it yet.

- **`tangent.app-board`.** A domain-free board: caller-supplied cards in
  columns, a filter bar, and an optional detail pane composed inside the one
  envelope rather than opening a second. Host plumbing — the shape both
  application plugins fill and neither owns — and the first kind whose
  `draft_custody` is `tangent-custodied`, so its view state is a durable
  revisioned draft record rather than `localStorage`.
  Filters are a **view over the cards the caller supplied**, never a query the
  host re-runs; the manifest, the tool description and the board itself all say
  so. The owning application supplies the records and applies every
  consequence: nothing in Tangent's process writes to it.

- **`tangent.app-board` per-card notes and drag staging.** A participant can
  write a note on a card as well as move it; both are draft view state until
  Sync, and a note already on record comes back with the refreshed board
  (`cards[].note`).

- **Participant view state is readable by the caller.** `Service.SaveDraft` had
  no callers; it now has one. A long-lived surface opened with
  `completion: async` stays pending while the participant works in it, and what
  they are looking at — filters, selection, whether a detail pane is open —
  reaches the caller through `tangent.surface_get`'s `Drafts[]`. A draft never
  takes the resolver lease, so opening a board in a second tab cannot steal the
  right to answer from the first; a stale revision is refused rather than
  merged; and reading view state is a pull, never a push. A caller must not
  present a draft as a decision.

- **A versioning gate (`CW-20260911-0008`).** ADR 0003 §3 has said since it was
  accepted that a `revision` may advance within a `version` only while
  `contract_digest`, `renderer.class`, `renderer.trust_class` and
  `required_capabilities` all hold. Nothing compared them, so a year of
  divergence surfaced in one sitting. `definition.CheckRevisionAdvance` is the
  rule as code, honoring the one granted exception — the once-per-kind
  response-schema backfill — rather than hard-failing it, and `contractLock` in
  `internal/envelope/extensions` is the committed record that turns it into a
  gate. **`tangent.app-board` is `0.2`, `revision: 1`**: one minor bump covering
  the `sync` block, `sync.note_label` and `cards[].note`, which were each added
  under a revision bump and each moved the contract. Semver versions mark
  releases and none of the three were released separately, so the compressed
  history is deliberate. §3 was not amended — it was right.

- **Plugin lifecycle, failure isolation and legibility (`CW-20260910-0036`).**
  `Unload` is now the host's stated contract instead of a decision each plugin
  made in a comment: a plugin drops its own state and comes off the roster, and
  **nothing it registered is removed** — the envelope registry has no removal,
  and a tool or route cannot be attributed to the plugin that registered it. The
  host refuses honestly rather than performing a rollback it cannot do, and
  `UnloadAll` runs on the shutdown path. Every contributed tool and route is
  bounded and panic-contained at registration, so one plugin's defect cannot
  take down the tool surface or hold graceful shutdown past its deadline the way
  an unbounded `/sse` stream did (`CW-20260909-0045`). `tangent.health_report`
  carries a plugin inventory — which loaded, which refused and why — because a
  tool list cannot answer it: a plugin can load, refuse, or load and register
  nothing a caller can see.

- **Channels, threads and participant bindings (`internal/channel`,
  migration 0013).** The storage for ADR 0006's channel / subject / participant /
  runtime-binding vocabulary. Partitions and project references are advisory
  navigation metadata, not enforcement.

- **The relay (`internal/relay`, migration 0014) and `tangent.relay_*`.** An
  immutable exchange journal with a transactional outbox and per-recipient
  replay cursors, exposed as the cooperative MCP inbox: open a channel, attach
  and detach a participant, send, receive (`unacked_only` gives a relaunched
  session a durable check-in), acknowledge, and read capabilities.

- **Operator channel pane.** A minimal channel view in the SPA over the same
  stores, the operator's own send/read path rather than the MCP surface.

- **Relay content is erasable.** `tangent --erase-exchange` and
  `--erase-channel` redact exchange bodies and delivery evidence, with the same
  `--confirm` requirement and single-writer-lock exclusivity as the existing
  erase flags, recorded in `retention_operations`.

- **Agent-turns inbox (`internal/turns`, `/turns`).** A strict-FIFO inbox for
  turns an agent is waiting on: the `tangent.agent-turn` kind, `/api/turns` REST
  and SSE, an operator UI with keyboard driving and canned replies, a Tether
  bridge (`internal/turns/tetherbridge`) that delivers a reply immediately to an
  idle session or at its next stop, and MCP tools — `tangent.turns_enqueue`,
  `tangent.turn_await` (at-least-once, a timeout is a result) and
  `tangent.turn_ack`.

- **Docs inbox, layout shell and theme toggle.** A Docs tab beside Approvals
  (renamed from "Human input") and Agent turns; a `PageShell` layout that ended
  the tab-to-tab layout shift; a system / light / dark toggle.

- **Agent-authored markdown renders.** One shared renderer
  (`components/markdown/Markdown.tsx`, react-markdown + remark-gfm) for approval
  bodies, evidence panes, triage, turns and agent prose. It builds a React tree
  rather than HTML, and raw HTML in the source stays visible as inert text.

- **A route's render crash is contained.** A `RouteErrorBoundary` around the
  route outlet keeps the shell and navigation mounted when one route throws,
  with "Try again" and "Go home".

- **Design-kit adoption.** The HITL inbox is on `@hollis-labs/design-kit`
  tokens instead of 150 hardcoded hex values; the design packages come from npm.

- **Transport-boundary gate (`.golangci.transport.yml`, `CW-20260930-0091`).**
  The Hollis Labs service-layer standard's lint at hard block, in `make lint`
  and CI: `internal/server`, `internal/mcp` and `internal/ws` must not import
  `internal/db` or run raw SQL. `tangent.retention_status` now reads through a
  new `internal/retention` service, which was the one finding.

### Changed

- **`tangent.tesseract_review` has no built-in namespace default (operator-facing).**
  It used to recall from a hardcoded maintainer namespace when the caller named
  none. The default now comes from the plugin's own environment,
  `TANGENT_TESSERACT_NAMESPACES` (comma-separated, e.g. `user/<name>/memory`).
  **With it unset, a call that names no namespaces is refused** rather than
  aimed at a namespace nobody chose. To keep the old behavior, set it in the
  environment of the process that runs the Tesseract plugin. The unused
  `RecallFilters.Origins` field, which Tesseract's recall route would have
  rejected, is removed.

- **Install is clone-and-build.** `go install …/cmd/tangent@<version>` builds a
  binary with no web UI (`internal/server/ui_dist` is a placeholder in git), so
  the README installs by clone, `(cd ui && npm ci)`, `make build`. The UI takes
  `@hollis-labs/plugin-registry` from npm (`^0.1.0`) instead of a sibling
  `plugin-sdk` checkout, so a fresh clone needs nothing beside it. Documentation
  URLs name `127.0.0.1`, which is what the daemon binds.

- **Go 1.26.6, no `replace` directives, dependencies current.** The `go` line is
  the portfolio floor, and CI reads it from `go.mod`. The local `replace` for
  `go-tether-client` (which broke a clean clone's runner-plugin build) and the
  dead one for `agentkit` are gone.

  | Module | v0.13.0 | v0.14.0 |
  |---|---|---|
  | go-envelopes | v0.1.0 | v0.4.0 |
  | go-mcp | — | v0.13.0 |
  | go-tether-client | — | v0.4.0 |
  | plugin-sdk | — | v0.5.0 |
  | modelcontextprotocol/go-sdk | v1.6.1 | v1.8.0 |
  | wails/v3 | v3.0.0-beta.16 | v3.0.0-beta.26 |
  | modernc.org/sqlite | v1.44.3 | v1.60.1 |

  The UI moved to Vite 8, Vitest 5, TypeScript 7, lucide-react 1.x and
  @testing-library/jest-dom 7, plus minor and patch updates.

- **Cancellation vocabulary is US spelling.** Tangent emits `canceled` /
  `user-canceled`, reads the legacy British values as their canonical form, and
  migration 0017 rewrites stored envelope rows. go-envelopes v0.5.0 removes the
  British values, so this closes the compatibility window.

- **MCP server and client roles converge onto `go-mcp` (`CW-20260917-0018`).**
  Tangent drops its direct dependency on the official
  `modelcontextprotocol/go-sdk` typed `AddTool` generic in favor of
  `github.com/hollis-labs/go-mcp`'s own tool-registration surface (adopted at v0.5.0; v0.13.0 as released), per
  the portfolio-wide consolidation (`CW-20260917-0011`) every other app in
  this initiative already completed. `/mcp` and `/sse` are unaffected on the
  wire — same transports, same stateless streamable-HTTP handling, still
  built directly against the official SDK's server underneath.

  Two things changed for real, not just mechanically:

  - **Every tool now carries an explicit, reviewed annotation** —
    `readOnlyHint`/`destructiveHint`/`idempotentHint`/`openWorldHint` — via a
    single table in `internal/mcp/tool_registry.go`. Nothing set these before;
    go-mcp makes them mandatory rather than optional or name-inferred.
    Plugin-contributed tools carry a fixed conservative default (mutating,
    destructive, non-idempotent, open-world) — the host cannot know a
    plugin's actual shape, and inferring "safe" from a name is exactly the
    failure mode this contract exists to rule out.
  - **Input-schema validation, restored.** go-mcp registers tools through the
    official SDK's raw, untyped `AddTool` — which does not validate a call's
    arguments against its declared schema; that responsibility moved to the
    app. Several of this host's schemas rely on `additionalProperties: false`
    to reject malformed input (see `gateway_metadata.go`), so a new receiving
    middleware (`internal/mcp/schema_validation.go`) restores exactly that
    enforcement — same validation semantics, same
    `validating "arguments": ...` error text — for every registered tool,
    host and plugin alike.

  The subprocess plugin callback path (`internal/pluginpkg/hostclient`) and
  the tool-error wire contract (`{v, kind: "error", error: {code, message}}`,
  and each surface's own richer shape) are unchanged; a plugin's outbound
  call now goes through `go-mcp/client`'s pool instead of a hand-rolled
  session, picking up reconnect and leak-prevention for free.

- **The plugin inventory, corrected (`CW-20260911-0036`).** Two names described
  things that were not what they said.

  `appboard` was not a plugin. It was a struct with a status field and a `Load`
  that named one kind — `tangent.app-board`, which the host publishes, declares
  `ownership_class: host-package`, versions with the repository and compiles
  into `ui_dist` with the release. Every property that would justify plugin-hood
  was absent: no dependency to isolate, no independent distribution, no
  independent versioning, no domain knowledge to keep out of core. It was never
  *moved* to a plugin either — the kind and the plugin were born in the same
  commit, so the plugin existed to give the new host a customer, and the two
  consumers that made the shape look shared arrived afterwards. The kind now
  registers through `extensions.RegisterAll` like every other host-package kind
  and `internal/plugins/appboard/` is deleted. **The ADR 0007 §4 door stays**,
  fully implemented and tested with a fixture, on the same terms as the host
  surfaces that are deliberately unimplemented: it is waiting for a kind this
  host does not own. Every plugin this build ships now holds a real application
  dependency.

  `torqueboard` is now `torque`, because it will not just be a board — the
  plugin carries the application, the way `tesseract` does. Its two tools are
  renamed with it, to `tangent.torque_<verb>_<surface>` so a second surface
  slots in without a collision or a breaking rename:

  | Was | Is |
  |---|---|
  | `tangent.torque_board` | `tangent.torque_open_board` |
  | `tangent.torque_board_sync` | `tangent.torque_sync_board` |

  Both tool names are new in this unreleased cycle, so nothing published moves.

- **ADR 0007 §4 is split, not replaced (`CW-20260911-0040`).** §4 was amended
  twice in two days, and both times the same half moved: its *decisions* held
  while its *instance stamps* and its *exclusions* rotted. Chrispian's call is
  that the boundary rule and the plugin model have different lifetimes and
  should not share a section.

  §4 keeps what never moved — the SDK says what a plugin may offer, the ADR 0003
  manifest says what the host will let it do, a registration without a manifest
  is refused — plus the reserved-to-host list and the surfaces this host declines
  to grow. It carries a note pointing at the rest.

  [**ADR 0008**](docs/adr/0008-the-plugin-model.md) carries the model:
  Nanite's manifest-authoritative registry adopted, runtime bundle loading,
  first-party kinds with Tangent keeping a small core set, compiled-in as the
  dogfood concession rather than the target, the extracted browser loader landing
  as a TypeScript companion inside `libs/plugin-sdk` so the registry wire contract
  is defined once, and four questions left explicitly open. **It reverses two of
  §4's exclusions** — runtime asset loading and subprocess-as-a-separate-decision
  — which were an agent's framing rather than a decision, and it corrects the
  claim that `appboard` was the first plugin.

  ADR 0007 stays Accepted and stays the collaboration-surface record. This is an
  extraction, not a supersession.

  ADR 0008 was drafted `Proposed` and approved the same day — read end-to-end by
  Chrispian and ruled through a Tangent approval queue, the first ADR in this set
  approved through Tangent rather than in chat. **An agent-drafted ADR landing
  `Proposed` and promoting only on his approval is now the standing convention**;
  an agent cannot write an approval line for a document he has not seen.

- **[ADR 0010](docs/adr/0010-the-boundary-is-coupling-not-write-direction.md):
  the boundary is coupling, not write direction.** Supersedes ADR 0007 §6's
  no-write test. A plugin writing to the application it adapts is the pattern
  working; what Tangent's own binary must not do is take an application
  dependency.

- **The renderer trust model is reduced to isolation (`CW-20260911-0060`).**
  Five trust classes became four isolations, and `renderer.trust_class` is now
  `renderer.isolation`. [**ADR 0009**](docs/adr/0009-renderer-trust-reduced-to-isolation.md)
  carries the decision and the evidence.

  The class did two jobs under one name: **isolation**, which a browser
  enforces, and **provenance** — which publisher, verified how, shipping its
  bundle where — which a table enforced. Read against the distribution that
  exists, the provenance half sorted seventeen first-party React components from
  one first-party React component that imports tldraw, and the two buckets
  differed by `process.exec`: a capability with no executor, gated on an
  authority nothing in the shipped binary holds. What blocked an out-of-tree
  publisher from `portfolio-trusted` was `signed-package` having no verifier —
  **an unbuilt feature reading as a security boundary**, not a decision that
  plugins must be signed.

  Removed: assurance grantability as a class gate, the publisher reservation,
  the empty-`asset_digest` rule, the `core-trusted` / `portfolio-trusted` split,
  and the class-based capability ceiling. The ceiling was removed rather than
  demoted to documentation — a table that reads like a gate and is not one is
  the defect being corrected, and keeping it one layer down would rebuild it.

  **The sandbox is untouched.** `tangent.design-iteration` renders markup an
  agent produced, in the participant's browser, at Tangent's origin. The agent
  is not the adversary there — it is the conduit, for a web page it summarized
  or a file it read. All three layers, and the four-check `postMessage` rule,
  stand exactly as they were.

  Cost, paid once and deliberately: **every shipped kind takes a version bump**,
  because the renderer identity moved and ADR 0003 §3 makes that a version
  rather than a revision. Under §8 C1 that takes pending interactions out of
  service for new submissions on upgrade. `CW-20260911-0045` rides along — two
  knowingly false comments in the `tangent.app-board` manifest have been waiting
  since `adca3ff` for a change that moved those bytes anyway.

- **The repository is public.** Internal prompts and working plans are no
  longer tracked, the README follows the portfolio template, and personal paths
  and private references are scrubbed from user-facing docs. `AGENTS.md` (the
  agent contract, with `CLAUDE.md` pointing at it) stays tracked and is covered
  by the documentation gate.

### Fixed

- A draft-bearing board no longer breaks after a reload or reconnect: the
  `stale_draft` frame carries the revision the record expects, so a client that
  lost its draft sequence resynchronizes instead of guessing.
- `tangent` returns from `main` through one `run() int`, so deferred cleanup
  (plugin unload, database close, ownership release) runs on every exit path.
- `TestUSEnglishCanceledErrorCodeMigrationRewritesAndRollsBack` pins its target
  to migration 17; after 0018 landed it had been rolling back the wrong one.
- The channel pane serializes an empty HITL list as `[]`, not `null`.

### Known limitations

- **The host holds no plugin configuration, and that is the answer**
  (`CW-20260910-0036`). `GetConfig`, `SetConfig` and `RegisterConfigSchema` stay
  unimplemented; a plugin reads its own environment. It keeps ADR 0005 §3.1's
  secret boundary true by construction rather than by policy — there is no store
  to leak, migrate or redact.
- **There is no runtime plugin enable/disable.** The installed set is read at
  startup; `tangent plugin install` / `remove` and a restart is how it changes.
- **A dispatch budget releases the caller, not the plugin.** A contributed tool
  or route call that overruns its budget returns to the caller, but the plugin
  process may keep working on it; the host cannot interrupt it.
- **The plugin registry is a proof.** `/api/plugins/registry` is served and
  loaded, but it is empty: no plugin ships browser contributions through it.
- **The LaunchAgent plist carries only `TANGENT_HTTP_PORT` and
  `TANGENT_DB_PATH`.** Any other environment an installed plugin reads, such as
  `TANGENT_TESSERACT_NAMESPACES`, has to be added by hand and is dropped by a
  reinstall.
- **An additive version bump takes pending interactions of that kind out of
  service** (ADR 0003 §8 C1). The old payloads are still valid by construction,
  but the pinned definition goes `unavailable` for new submissions anyway.
  Narrowing C1 for an `additive` compatibility class is the honest fix and is
  deliberately not in this change; proceed and fix the breakage as it is felt.
- ADR 0002 uses "custody" for the retention axis and `draft_custody` uses it
  for a location axis — two meanings, one word, in adjacent documents. Not
  renamed; recorded so a reader who trips on it knows it is known.

## [v0.13.0] - 2026-09-07

The first **stable cut for personal use** (plan `CW-20260907-0014`): everything
the foundation phase shipped, the durable `/hitl` inbox, and the desktop shell
as an unsigned, locally built `Tangent.app`. What this release deliberately does
**not** contain is listed under *Not in this release* at the end of the
section. The formal desktop-shell acceptance (`CW-20260905-0050`) and the
shell's own decision record (`CW-20260905-0051`) are not gates for this cut and
remain open.

### Added

- **Desktop shell (`cmd/tangent-app`, `internal/appshell`).** A Wails v3 webview
  pointed at the same loopback server `tangent` serves; no Wails asset server
  and no generated bindings, so the SPA and its participant-cookie / CSP model
  are unchanged. On launch the app probes `/healthz` and **adopts** a running
  daemon or **boots** one in-process; a database held by something that is not
  serving is reported distinctly, never guessed through. Window geometry,
  always-on-top, and a stable client id persist in
  `os.UserConfigDir()/tangent/shell.json` with the measured guards (readback
  epsilon, sanity bounds, creation-time gate, title-bar height correction, and
  atomic writes). The persisted client id reaches the SPA as `?clientId=`, so
  quitting and relaunching the app is a reconnect that inherits the resolver
  lease rather than a second tab. `internal/boot` is the server assembly both
  binaries share. (`CW-20260905-0026`, `0027`, `0029`; PR #22)
- **macOS packaging.** `make build-app` assembles `Tangent.app` from
  `packaging/macos/Info.plist` and an icon, verified by
  `internal/packagecheck` before promotion. The bundle is **not code-signed or
  notarized** by design. CI runs the desktop shell in its own macOS job so a
  broken shell build never blocks the server build. (`CW-20260905-0032`; PR #22)
- **Launch at login.** `internal/launchagent` and `cmd/tangent-launchagent`
  (with `make launch-agent-*`) write, validate, install, remove, and inspect a
  user LaunchAgent that starts the **headless daemon** at login: `RunAtLoad`
  true, `KeepAlive` false, `launchctl bootstrap`/`bootout`. Install refuses
  when the binary is not where the plist would say; `status` re-validates the
  path so a moved binary is reported. Nothing installs it automatically; the
  stable install script (`CW-20260907-0020`) does. (`CW-20260905-0031`; PR #26)
- **Gateway trace metadata is accepted at the MCP boundary.** Tether's `mux`
  proxy writes the caller's W3C trace context into tool arguments as
  `_traceparent` (and `_tracestate`), which every strict schema refused, so a
  Claude Code session on the default mux configuration could not call a single
  HITL tool. A receiving middleware now strips exactly those two keys before
  validation and records the trace as an **upstream link** beside the derived
  telemetry identity (record-only attributes, an OpenTelemetry span link; never
  the trace id, never a metric dimension). Schemas stay strict; unknown keys are
  still rejected. (`CW-20260907-0022`; PR #24)
- **Dev and stable are separate instances.** Every dev-facing `make` target
  runs on `DEV_PORT` (7843) against a workspace-local database; the daemon's
  own defaults (7842, `~/.tangent/tangent.db`) are the stable install's. The
  Cerberus resource change is committed as `packaging/cerberus/` and applied by
  the install, not by the repo. (`CW-20260907-0018`; PR #27)
- **macOS installer.** `cmd/tangent-install` (wrapped by
  `scripts/install-macos.sh` and `make install-macos` / `install-macos-dry-run`
  / `uninstall-macos`) places the headless daemon and `Tangent.app`, writes the
  LaunchAgent, and waits for `/readyz`; install and upgrade are the same path.
  It refuses, touching nothing, when the two artifacts are not a pair, when the
  daemon on the port is not ready or is not the installed stable daemon, when
  the artifact would downgrade, when the artifact's `--db-check` rejects the
  existing database, or when nothing healthy answers afterwards. Uninstall
  never removes the database. `tangent --version` prints the gate-checked
  release so the installer and an operator can ask a binary what it is.
  (`CW-20260907-0020`; PR #29)
- **ADR 0006, the collaboration-surface and relay boundary.** Supersedes four
  named places in ADR 0005 and keeps every other exclusion; records the
  channel / thread / view / runtime-binding vocabulary, operational-history
  custody, and the conductor scenario; states that none of that capability
  exists in this build. (`CW-20260906-0056`; PR #23)
- **One version source.** `ui/package.json` is the release version;
  `internal/smoke/version_test.go` fails when the CHANGELOG, the README, the
  Go `HostVersion`, or the bundle template disagree with it. Previously the
  tree said 0.1.0, v0.11.0, and v0.12.0 about itself at once.
  (`CW-20260907-0019`)
- **Database operations: single-writer ownership, backup, restore, repair, and
  six distinct deletion kinds.** Implements
  [ADR 0002](docs/adr/0002-retention-and-draft-custody.md) §6 and §7, and clears
  the obstruction it recorded: there was no deletion path at all for canonical
  records, because twelve `*_immutable_delete` triggers abort `DELETE` and
  SQLite fires them on foreign-key cascades too, while the legacy
  `rooms`/`envelopes` pair holding the same payloads deleted cleanly.
  Deletability was exactly backwards.
  - **Single-writer is enforced, not hoped for.** Every mode that writes takes a
    non-blocking exclusive `flock` on `<database>.owner` before opening the
    database and refuses — naming the holding process — rather than queuing. The
    kernel lock is the authority and is released on process death, so a crash
    needs no manual cleanup. Commands needing exclusive ownership additionally
    probe `/readyz` and refuse if anything is serving, which catches a Tangent
    deployed before the lock existed.
  - **Backup is opt-in and uses `VACUUM INTO`**, in two modes with two different
    guarantees: online (a point-in-time snapshot taken while the service runs,
    verified against 200 concurrent writes) and drained (`wal_checkpoint(TRUNCATE)`
    first, requiring that nothing else holds the database). A `cp` of a WAL-mode
    file is not a backup. Each backup writes a sidecar manifest carrying its
    fingerprint and no filesystem path.
  - **Restore verifies before it touches anything**, moves the existing database
    and its WAL companions to `<database>.superseded-<timestamp>` rather than
    deleting them, restores them on any failure, and asserts five preservation
    properties individually: definition digests, interaction identities
    (including idempotency keys), resolutions (including integrity digests and
    participant binding), audit history, and delivery obligations. Pending
    deliveries and held leases survive and are reconciled by `RecoverAfterRestart`
    at the next boot — proven end to end, with the retryable/manual distinction
    intact and reconciliation idempotent across two boots.
  - **`--db-check` and `--db-repair`.** The expected immutability-guard inventory
    is derived by migrating a throwaway in-memory database with this binary's own
    embedded migrations, so it cannot drift; a missing guard is recreated from
    that reference's exact text. Repair fixes exactly two things — a missing guard
    and an unbounded WAL — and reports everything else with the action that fixes
    it, which is a restore.
  - **The custody maintenance path.** One narrowly scoped function in
    `internal/db` drops only the guards an operation needs, inside one
    transaction, recreates them from `sqlite_master`'s own text, and verifies
    them back before committing. A rollback restores them too, because SQLite DDL
    is transactional; the single connection and the single-writer lock mean the
    window is unobservable to any other statement or process.
  - **Six deletion kinds, kept distinct**: capability expiry (`effect_handles`,
    no guard suspension needed), draft deletion (tombstones `draft_revisions.payload`
    and finally gives `draft_revision_tombstones` a writer), payload redaction
    (ADR 0002 §2's typed tombstone, preserving identity and digests, idempotent),
    surface close (removes nothing, and is logged saying so), surface purge (the
    cascade delete, never automatic), and external-source deletion (a recorded
    refusal — Tangent never held the bytes).
  - **`retention_operations`** (migration `0012`): append-only *and* undeletable,
    with no foreign keys so an audit row outlives the surface it records.
    Carries digests, identifiers, counts, and states — never content — plus
    `guards_restored` and a backup survey that says `not-surveyed` when nobody
    looked.
  - **`tangent.retention_status`** reports the custody posture over MCP,
    read-only. The operations themselves are
    CLI commands, because erasure authority belongs to the local user and MCP has
    no authenticated caller identity to hold it.
  - See [docs/database-operations.md](docs/database-operations.md), including
    what deletion cannot reach and what this work did not implement.

- **Separate liveness, readiness, and capability health.** `/healthz` proved
  only that the process was responding while reporting `{"status":"ok"}` for a
  host whose database, migrations, registry, renderer host, or requested kind
  was unavailable. Three probes now answer three questions: `GET /healthz`
  (liveness — touches no dependency, so a supervisor never restarts a
  repairable process in a loop), `GET /readyz` (readiness — database, schema
  version against the version this binary embeds, definition registry, renderer
  host, delivery-worker authorization; `ok`/`degraded`/`unavailable`, 503 only
  on the last), and `GET /healthz/capability[/{kind}]` (per-kind, reported in
  `internal/definition`'s existing `available`/`incompatible`/`quarantined`/
  `unavailable` vocabulary rather than a second one). `tangent.health_report`
  exposes all three over MCP for a Tether-connected client with no HTTP path to
  the host. Every non-passing check carries
  an operator action, and no report carries a payload, participant text,
  filesystem path, session, or capability material.
- **Versioned interaction-definition registry.** Every Tangent-owned kind now
  ships as a package under `internal/envelope/extensions/packages/` with an
  authored `manifest.yaml`: identity, request/response/error schemas, renderer
  binding and trust class, host-mediated effect capabilities, draft custody and
  sensitivity, trust evidence, telemetry, and compatibility ranges. See
  [ADR 0003](docs/adr/0003-definition-and-package-ownership.md).
- **Durable definition material.** Migration `0007` adds the immutable
  `definition_manifests` table and extends `definition_bindings` with the full
  manifest identity. A submitted interaction retains the exact material behind
  its pinned digest, so it can still be validated and replayed after a restart,
  after the installed catalog changes, and after the current version moves on.
- **`tangent.definition_registry_list`, `tangent.definition_get`,
  `tangent.definition_registry_diagnostics`.** Payload-bounded registry
  diagnostics reporting materialization state, ownership, renderer binding,
  digests, and retained-material coverage.
- **Response-schema validation.** `envelope.Service.RegisterDefinition` carries
  a response schema into `TypeSpec.PayloadSchema` and into the binding digest.
  `tangent.hitl-item` is the first kind to use it; the other seventeen declare
  `compatibility_response_schema: absent` and keep today's response-kind-only
  check until they are backfilled.
- **Definition source digests on generated artifacts.** Every generated file
  carries a `@definition-source` stamp, `make check-envelopes` reports which
  kind drifted rather than which byte, and `ui/src/generated/renderer-bindings.ts`
  makes the manifest's renderer binding checkable against `main.tsx`.
- **Authenticated browser participant sessions.** A same-origin loopback
  document navigation with no session cookie mints one: an `HttpOnly`,
  `SameSite=Lax` cookie naming a durable row (migration `0008`) that stores
  only the SHA-256 of the cookie value. No idle expiry, absolute 30-day
  lifetime, rotation on assurance change, and revocation via
  `tangent --revoke-participant-sessions`. See
  [ADR 0004](docs/adr/0004-caller-participant-and-room-access-authority.md).
- **Object-access authorization.** Seven capabilities (`view`, `submit`,
  `draft`, `resolve`, `cancel`, `close`, `administer`) evaluated against a
  named surface or interaction through one decision function in
  `internal/authz`. Caller scope is `<authority>:<partition>`, with the
  authority host-assigned from admission facts and the partition
  caller-declared.
- **`/api/rooms`.** A participant-authenticated, origin-guarded browser room
  API mirroring `/api/hitl`, replacing the SPA's direct `/mcp` JSON-RPC POSTs
  for `session_list`, `session_get`, and `session_close`.
- **Process-wide `Referrer-Policy: no-referrer` and
  `X-Content-Type-Options: nosniff`,** applied in the middleware that wraps the
  mux so no route can forget them.

### Changed

- **`surface_open_requests.surface_id` moves from `ON DELETE RESTRICT` to
  `ON DELETE CASCADE`** (migration `0012`, ADR 0002 §Q9). Its `request_snapshot`
  was a permanent second copy of the caller payload that no operation could
  reach and that made every surface opened through the async path undeletable.
  The immutability trigger stays: the table still refuses `UPDATE` and direct
  `DELETE`.
- **New CLI commands**: `--db-check`, `--db-backup` (with `--db-drain`),
  `--db-restore`, `--db-compact`, `--db-repair`, `--retention-plan`,
  `--retention-apply`, `--retention-history`, `--erase-interaction`,
  `--erase-surface`, `--erase-drafts`, `--purge-surface`, `--close-surface`,
  `--expire-capabilities`, `--external-deletion-report`, with `--actor`,
  `--policy-ref`, `--backup-dir`, `--dry-run`, and a mandatory `--confirm` for
  anything that removes content or replaces the database.

- **`definition_bindings.revision` is a real field.** It previously held a copy
  of `version`. Migration `0007` normalizes existing rows to `1`; new bindings
  carry the manifest's own monotonic revision.
- **Definitions that cannot be served are distinguishable.** `incompatible`,
  `quarantined`, and `unavailable` surface as `definition_incompatible`,
  `definition_quarantined`, and `definition_unavailable`, each carrying a reused
  go-envelopes error code. No shipped kind is in any of those states.
- **A room UUID is no longer an answer credential.** The `/ws` upgrade requires
  a valid participant session immediately, with no grace period. A raw
  WebSocket client that dials `/ws?roomID=…` with no cookie now receives a 403.
  Opening the room URL in a browser mints the session and is unchanged for a
  human; scripted attachments need the cookie recipe in
  [`docs/mcp-integration.md`](docs/mcp-integration.md#room-access-and-caller-scope).
- **`tangent.session_close` and `tangent.surface_close` are
  partition-enforcing.** Closing dispositions another caller's pending human
  work. `session_list` and `session_get` stay authority-wide, so "show me all
  my rooms" is unchanged. The seventeen workflow tools and
  `tangent.session_advance` can no longer push into a room another partition
  owns. Refusals are 403-shaped within an authority and 404-shaped across one.
- **`caller.scope`, `requester_scope`, and `owner_scope` are no longer the
  authorization value.** The arguments are still accepted so shipped schemas do
  not break, but only their partition half survives; the authority is
  host-assigned. `tangent.surface_open` derives `owner_scope` from the caller
  and keeps the supplied value as an attribution label.
- **`tangent.interaction_submit` checks the target surface.** A caller can no
  longer create an interaction on a surface it neither owns nor opened.
- **Caller scope spelling.** `direct-loopback:<app>` becomes
  `standalone-local:<app>`. `hitl_*` request shapes are unchanged, existing rows
  are not rewritten, and the old spelling reads as the same caller through a
  fixed alias.
- **`/mcp`, `/sse`, and `/ws` carry the same-origin guard** `/api/hitl/*`
  already had. It permits header-less non-browser clients, so MCP clients and
  the documented `curl` recipes are unaffected.

No wire name, version, request schema, response payload, MCP tool name, room
id, or phase projection changed. Two routes were added (`/api/rooms`,
`/api/rooms/{roomID}`); none was removed.

### Fixed

- **Backing up and restoring a database older than the binary.** The
  pre-upgrade backup, the one case the tooling exists for, failed its own
  integrity check because every fixture had been migrated to current first.
  Backup and restore now work against a source schema behind the binary.
  (`bab0b89`)
- **An identical room-workflow retry returns the bound room.** Six identical
  `tangent.approval-queue` calls produced one interaction but six rooms; the
  retry now returns the same handle and room. (`5c7f536`)
- **A refused submission no longer empties the form.** Submit cleared the
  pending envelope optimistically, unmounting the renderer and every field the
  operator had typed before the server refused it. (`da21f2d`)
- **The room-workflow parity test no longer races a 250 ms window.** Tests that
  answer the room while the caller waits use a rig whose window cannot expire
  inside their own deadlines; the tests that want expiry keep the compressed
  window. (`CW-20260907-0024`; PR #25)

### Not in this release

- **No system tray, close-to-hide, or single-instance UX** (`CW-20260905-0030`,
  stable 1.1). Closing the last window quits the app.
- **No code signing or notarization.** `Tangent.app` is unsigned and locally
  built, for a single-user machine.
- **The desktop-shell acceptance matrix has not been run** (`CW-20260905-0050`),
  and the shell's decision record is unwritten (`CW-20260905-0051`, ADR 0007).
  Documents that say "nothing in the tree is Wails" are corrected there, not
  here.
- **No relay, channel, or addressed conversation.** ADR 0006 is a decision
  about direction; the durable `/hitl` inbox is the only attention surface.
- **The stable install has not been performed.** The installer ships; running
  it (LaunchAgent written, dev redeployed to 7843, `tangent-dev` catalog entry
  enabled) is the rest of `CW-20260907-0020`, after this tag.

### Security

- **`standalone-local` partitions are advisory, not a security boundary.** Any
  local caller can assert any partition; isolation is enforced only across
  authorities, where the prefix is host-assigned. Loopback admission is not
  authentication: a hostile local process running as the same user can still
  mint a participant session. Nothing downstream may present a partition as
  isolation.
- **`clipboard.write` and `export.download` are enforced only inside a
  sandboxed frame.** On the main origin they remain declared-not-enforced.
  `network.fetch` is genuinely enforced by the document CSP's `connect-src`.
- **No definition declares a host-mediated effect capability**, so every
  request through `POST /api/effects` refuses `effect_capability_undeclared`.
  The broker has zero production traffic (`CW-20260905-0010`).
- **There is no browser in CI.** CSP and sandboxing are proven by construction
  and by unit tests over the emitted policy, never by observing a browser
  refuse anything; `docs/manual-tests/renderer-sandbox-e2e.md` is the real
  verification and it is manual (`CW-20260904-0171`).
- **The OpenTelemetry bridge has never been observed against a collector**
  (`CW-20260905-0011`).
- **`SaveDraft` has no production caller**; browser `localStorage` is the only
  running draft custody, so server-side draft tables are empty in production
  (`CW-20260905-0001`).
- **`renderer.entry` loads nothing**; `ui/src/main.tsx` registers renderers by
  string literal (`CW-20260905-0004`).
- **The ADR 0002 §3 custody-precedence engine is not implemented**; retention
  uses host windows only (`CW-20260905-0008`).

The full, canonical list is
[docs/architecture.md](docs/architecture.md#current-limitations).

## [v0.12.0] - 2026-05-10 — documented, not tagged

Wizard. Tangent now ships a persistent room-backed guided wizard
workflow with explicit partial updates, canonical branch-aware step
progress, browser-local draft recovery, and final completion through
the existing room/tool contract.

### Added

- **`tangent.wizard`.** A bundled room-backed wizard workflow with
  canonical step definitions, explicit partial updates, and final
  completion through the existing room/tool contract.
- **Wizard room substrate.** `phase_outputs["wizard"]` now persists
  `wizard_id`, canonical `steps`, `current_step_id`, accepted
  `progress`, deterministic `branch_selections`, summary fields, and
  `updated_at`, all surfaced through `tangent.session_get.wizard`.
- **Wizard browser host.** Tangent now ships a step-based wizard host
  with reopen hydration, browser-local draft recovery, review summary,
  back/next navigation, attachment-style response capture, and action
  output affordances.
- **Wizard manual test coverage.** Added
  `docs/manual-tests/wizard-e2e.md`.
- **Repo-local Tangent workflow skills.** Added tight copy/paste
  launcher skills under `.agents/skills/` for the bundled workflows and
  the multi-phase writing flow so operators can reuse known-good
  prompts and payload shapes during manual runs.

### Changed

- **`tangent.session_get` now projects wizard state.** Agents can read
  canonical wizard room state directly without scraping envelope
  history.
- **Wizard submit semantics are explicit.** Partial room-backed saves
  stay `status: "partial"` while final completion resolves with
  `status: "submitted"` and a completed summary payload.
- **Manual smoke docs are stricter about launch discipline.** The smoke
  runbook now calls out payload-sensitive workflows, shared-room
  writing-flow sequencing, and the one-tab-per-room rule while a submit
  is pending.

### Fixed

- **Wizard manual docs now include a minimal valid seed payload.**
  Operators no longer need to infer the required `steps`,
  `current_step_id`, and branch target shape from tests or handlers.

## [v0.11.0] - 2026-05-10

Dashboard. Tangent now ships a persistent room-backed dashboard
workflow for explicit refresh/update turns, saved layout reuse,
browser-local layout draft recovery, room/artifact drill-down, and
concise accepted snapshot export metadata.

### Added

- **`tangent.dashboard`.** A bundled room-backed dashboard workflow
  with canonical tiles, explicit refresh/update submits, and room reuse
  via `meta.roomID`.
- **Dashboard room substrate.** `phase_outputs["dashboard"]` now
  persists `dashboard_id`, canonical `tiles`, normalized `layout`,
  room-backed `saved_layouts`, `active_layout_id`, normalized
  `query_state`, summary fields, append-only `snapshot_history`, and
  accepted `export_state`.
- **Saved layouts and drill-down flows.** The shipped host now supports
  reusable saved layouts, browser-local draft recovery for in-progress
  layout edits, room-route drill-down, and artifact-ref handoff actions.
- **Accepted export/share metadata.** Accepted dashboard submits persist
  a deterministic `export_state` payload with snapshot id, active
  layout, tile count, and stable room/artifact refs for downstream
  inspection.
- **Dashboard e2e coverage.** Added
  `docs/manual-tests/dashboard-e2e.md`.

### Changed

- **`tangent.session_get` now projects dashboard state.** It exposes a
  dedicated `dashboard` payload with canonical tiles, normalized
  layout/query state, saved layouts, accepted snapshot history, and the
  latest export/share metadata.
- **Docs now describe dashboard as the newest bundled workflow.**
  README, MCP integration, and manual test docs now reflect the new
  tool surface and the localhost-only single-user posture of the
  dashboard workflow.

### Fixed

- **Rejected dashboard submits no longer risk mutating accepted export
  metadata.** Invalid payloads still return explicit rejected response
  shapes while preserving the last accepted room-backed snapshot and
  export state for reopen.

## [v0.10.0] - 2026-05-10

Progress panel. Tangent now ships a persistent room-backed
progress-panel workflow for explicit update/reopen turns, append-only
status history, checkpoint summaries, operator recovery, and concise
export snapshots.

### Added

- **`tangent.progress-panel`.** A bundled room-backed progress workflow
  with canonical tracked items, explicit update submits, and room reuse
  via `meta.roomID`.
- **Progress-panel room substrate.** `phase_outputs["progress_panel"]`
  now persists `panel_id`, canonical `items`, append-only `updates`,
  derived `checkpoints`, and room-backed summary fields surfaced
  through `tangent.session_get.progress_panel`.
- **Timeline/log/checkpoint inspection.** The shipped host includes
  practical timeline, checkpoint, and structured log views with
  browser-local view-state recovery across refresh.
- **Operator controls and recovery.** The host now exposes explicit
  pause/resume/cancel affordances, preserves unsent operator context in
  browser storage, and restores that context on reopen without
  overwriting canonical room state.
- **Summary/export surface.** Progress summaries now carry latest
  checkpoint label and completion-result metadata, and the browser host
  can copy a concise export snapshot for downstream inspection.
- **Progress-panel e2e coverage.** Added
  `docs/manual-tests/progress-panel-e2e.md`.

### Changed

- **`tangent.session_get` now projects progress-panel state.** It
  exposes a dedicated `progress_panel` payload with canonical items,
  append-only updates, derived checkpoints, and concise summary fields
  for agent reasoning.
- **Docs now describe progress panel as the newest bundled workflow.**
  README, MCP integration, and manual docs now reflect the new tool
  surface and the fact that updates still ride the existing Tangent
  room/tool contract.

### Fixed

- **Rejected progress updates no longer corrupt room history.** Invalid
  update payloads return an explicit rejected response shape while
  preserving the last accepted room-backed progress snapshot for reopen.

## [v0.9.0] - 2026-05-09

File picker. Tangent now ships a persistent room-backed file-picker
workflow for explicit local artifact selection, persisted browse/query
state, browser-local draft recovery, accepted selection revisions, and
reusable handoff payloads.

### Added

- **`tangent.file-picker`.** A bundled room-backed file-picker workflow
  with allowed browse roots, explicit submit/cancel, and room reuse via
  `meta.roomID`.
- **File-picker room substrate.** `phase_outputs["file_picker"]` now
  persists `picker_id`, canonical `browse_roots`, durable
  `selected_refs`, normalized `query_state`, append-only
  `selection_revisions`, and accepted submission metadata.
- **Browser-local draft recovery and preview.** File-picker turns keep
  unsent staged selection/query state in browser storage keyed by room +
  picker and recover it on refresh without overwriting canonical room
  state.
- **Artifact handoff metadata.** Accepted file-picker submits now
  persist a stable `handoff` payload plus `submission_summary` so later
  workflows can consume selected artifact refs without reparsing the UI
  state.
- **File-picker e2e coverage.** Added
  `docs/manual-tests/file-picker-e2e.md`.

### Changed

- **`tangent.session_get` now projects file-picker state.** It exposes
  a dedicated `file_picker` payload with canonical roots, durable
  selected refs, normalized query state, accepted selection revisions,
  and the latest handoff summary.
- **Docs now describe file picker as the newest bundled workflow.**
  README, MCP integration, and manual test docs now reflect the new
  tool surface and localhost-only artifact-ref selection model.

### Fixed

- **Invalid file-picker submits no longer create fake accepted room
  state.** Rejected payloads return an explicit non-accepted response
  shape and preserve the last accepted selection snapshot for reopen.

## [v0.6.0] - 2026-05-09

Form collect. Tangent now ships a persistent room-backed generalized
form workflow for schema-driven field collection, conditional sections,
repeatable groups, saved drafts/templates, attachment refs, and durable
submission summaries.

### Added

- **`tangent.form-collect`.** A bundled room-backed structured form
  workflow with canonical agent-provided schema, explicit submit/cancel,
  and room reuse via `meta.roomID`.
- **Form room substrate.** `phase_outputs["form-collect"]` now
  persists `form_id`, canonical `schema`, normalized `answers`,
  `notes`, `updated_at`, room-backed `saved_drafts`, `templates`,
  lightweight `attachment_refs`, and `submission_summary`.
- **Conditional and repeatable sections.** The shipped form host
  supports deterministic show/hide logic plus repeatable groups backed
  by the persisted answer state.
- **Local refresh recovery.** In-progress form state autosaves in the
  browser for the active room + form and recovers across refresh until
  submit/cancel.
- **Form submit summary/export.** Submitted forms now persist a durable
  summary/export payload so agents can reopen the room and inspect the
  last canonical submission without parsing UI-local state.
- **Form-collect e2e coverage.** Added
  `docs/manual-tests/form-collect-e2e.md` and
  `scripts/form-collect-mock-call.mjs`.

### Changed

- **`tangent.session_get` now projects form-collect state.** It exposes
  a dedicated `form_collect` payload with schema, answers, room-backed
  drafts/templates, attachments, and the last submission summary.
- **Docs now describe form collect as the newest bundled workflow.**
  README, MCP integration, architecture, and developer docs now reflect
  the shipped v0.6 tool surface.

### Fixed

- **Refresh no longer cancels active form work by default.** The room
  keeps the pending form alive while the browser restores its local
  unsent draft state.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. Form collect adds richer persisted room state and browser
  draft recovery, but no auth, TLS, remote sync, or live multi-user
  collaboration.

## [v0.5.0] - 2026-05-09

Spreadsheet review. Tangent now ships a persistent room-backed
spreadsheet-review workflow for dense tabular review, explicit submit,
local refresh recovery, CSV export metadata, and saved query views.

### Added

- **`tangent.spreadsheet-review`.** A bundled room-backed table review
  workflow with canonical agent-provided rows, row selection, bulk
  actions, and explicit submit/cancel.
- **Spreadsheet-review room substrate.** `phase_outputs["spreadsheet-review"]`
  now persists canonical `columns` and `rows`, normalized `query_state`,
  `notes`, selected rows, bulk `action_id`, saved views, and export
  metadata.
- **Saved views and local draft recovery.** Spreadsheet-review turns
  support room-backed named query views plus host-local refresh recovery
  for unsent browser state.
- **CSV export metadata.** Exported CSVs stay browser-local as files,
  while lightweight export refs persist on the room for later agent
  reasoning.
- **Spreadsheet-review e2e coverage.** Added
  `docs/manual-tests/spreadsheet-review-e2e.md` and
  `scripts/spreadsheet-review-mock-call.mjs` for submit -> reopen ->
  cancel verification in one room.

### Changed

- **`tangent.session_get` now projects spreadsheet-review state.** It
  exposes a dedicated `spreadsheet_review` payload with canonical table
  state, normalized query metadata, saved views, selected rows, and
  export refs.
- **Docs now describe spreadsheet review as the current workflow
  expansion surface.** README, MCP integration, architecture, and
  developer docs now reflect the shipped v0.5 workflow set.
- **The roadmap advances past the initial spreadsheet-review expansion.**
  v0.5 is now the current shipped release rather than a future intent
  section.

### Deprecated

_None._

### Removed

_None._

### Fixed

- **Saved-view persistence now reuses the same normalization and
  validation path as full spreadsheet snapshots.** Invalid names and
  stray `query_state` keys are rejected before room state is updated.
- **Spreadsheet-review writes now reject unsupported persisted blob
  versions before overwriting phase state.** This matches the room-state
  guards used by other persisted workflow projections.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. Spreadsheet review adds richer persisted room state and
  browser draft recovery, but no auth, TLS, remote sync, or live
  multi-user collaboration.

## [v0.4.0] - 2026-05-09

Shared Whiteboard. Tangent now supports a persistent MCP-driven
whiteboard workflow in one room, with explicit submit/reopen turns,
local refresh recovery, PNG export, artifact-backed reference images,
and revision browsing for agent handoff from revision N.

### Added

- **`tangent.whiteboard`.** A bundled tldraw-backed whiteboard workflow
  with room reuse via `meta.roomID`, blank/seeded board load, and a
  full-scene submit contract.
- **Whiteboard room substrate.** `phase_outputs["whiteboard"]` now
  persists canonical scene JSON, lightweight asset refs, export refs,
  notes, updated-at metadata, and append-only revision history.
- **Local draft recovery.** Whiteboard edits autosave in the browser for
  the active room + board and recover after refresh until submit/cancel.
- **PNG export and artifact-backed refs.** Export metadata now persists
  on the room, and reference-image assets survive reload through durable
  artifact-style URIs instead of browser-only state.
- **Revision browser.** The room UI now lists prior whiteboard
  revisions, supports reopening a snapshot for inspection, and supports
  explicit continuation from an older revision with recorded lineage.
- **Whiteboard e2e coverage.** Added
  `docs/manual-tests/whiteboard-e2e.md` and
  `scripts/whiteboard-mock-call.mjs` for submit -> reopen -> continue
  verification in one room.

### Changed

- **`tangent.session_get` now projects whiteboard state.** It exposes
  the latest persisted board plus lightweight revision metadata for
  agent reasoning, while full historical snapshots stay room-local to
  the whiteboard browser UI.
- **Docs now describe Tangent as a multi-workflow surface that includes
  whiteboard collaboration.** README, MCP integration, architecture,
  and developer docs now reflect the shipped v0.4 tool surface and
  single-user localhost scope.
- **The roadmap advances past the planned whiteboard phase.** v0.4 is
  now the current shipped release rather than a future intent section.

### Deprecated

_None._

### Removed

_None._

### Fixed

- **Whiteboard submit normalization now rejects fake persistence on
  invalid payloads.** Invalid board submits do not create bogus
  revisions in room history.
- **Refresh no longer cancels active whiteboard work by default.**
  `beforeunload` preserves local draft recovery rather than treating a
  refresh like an explicit cancel.

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. The whiteboard adds richer persisted local state and
  browser draft recovery, but no auth, TLS, remote sync, or live
  multi-user presence.

## [v0.3.0] - 2026-05-09

Interview Protocol — Writing. Tangent now supports the first full multi-phase
workflow on top of persistent rooms and the session substrate: interview,
synthesis, drafting, revision, and final output in one room, including
explicit jump-back to earlier phases.

### Added

- **Phase-state substrate.** Rooms now persist `current_phase`,
  append-only `phases_visited`, and versioned `phase_outputs`, with MCP
  tools `tangent.session_advance_phase` and
  `tangent.session_set_phase_output`.
- **`tangent.interview_question`.** Long-form question/answer turns with
  optional quick picks, topic/thread metadata, and explicit output-shape
  prompting.
- **`tangent.synthesis_notes`.** Private synthesis state with
  server-enforced hidden/visible projection and optional outline preview.
- **`tangent.block_draft`.** Section-first drafting loop with accept,
  revise, inline-edit, and redirect actions. Accepted blocks now persist
  in order on the room.
- **`tangent.prose_revision`.** Unified review/copy/style workflow with
  explicit per-suggestion accept/reject/comment outcomes.
- **`tangent.output_render`.** Final markdown renderer with copy and
  download affordances, backed by persisted final-output room state.
- **Writing workflow manual recipe.**
  `docs/manual-tests/writing-flow-e2e.md` now covers interview -> output
  in one room, including one jump-back.

### Changed

- **`tangent.session_get` is now the writing-room checkpoint surface.**
  It exposes structured interview history, synthesis projection, accepted
  draft blocks, reconstructed current draft, prose revision outcomes, and
  the final output artifact.
- **Local Tangent skill/command docs now describe the canonical writing
  choreography.** The Tangent Nanite skill/command point at the exact
  phase/tool sequence instead of forcing agents to reconstruct it ad hoc.
- **Architecture and MCP integration docs now describe the v0.3 workflow
  surface.** Docs now reflect sixteen advertised tools and the canonical
  phase progression with explicit jump-back.

### Deprecated

- The old `fast-triage` redirect stub remains legacy-only and should not
  be extended for new workflows.

### Removed

_None._

### Fixed

_None._

### Security

- **The trust model is unchanged.** Tangent remains localhost-only and
  single-user. The writing workflow adds richer persisted state, but no
  auth, TLS, or capability-gated distribution yet.

## [v0.2.0] - 2026-05-08

Persistence, multi-envelope rooms, and three bundled workflows. Tangent now
absorbs the Fast-Triage use case while remaining a single localhost binary
with MCP and browser transports.

### Added

- **SQLite-backed persistence layer.** Rooms and resolved envelope history
  now persist in `~/.tangent/tangent.db` and survive server restart.
- **Multi-envelope-per-room session model.** Added the `tangent.session_*`
  MCP tools: `tangent.session_create`, `tangent.session_advance`,
  `tangent.session_get`, `tangent.session_close`, and
  `tangent.session_list`.
- **`tangent.feedback` workflow.** Bundled structured-form workflow with the
  frontend `<Feedback>` component.
- **`tangent.design-iteration` workflow.** Sandboxed HTML preview with the
  bundled `<DesignIteration>` component and click-event capture.
- **Multi-room tab strip UX.** The SPA can switch active rooms inside one
  browser tab while preserving per-room state.
- **Explicit cancel flow.** Active envelopes expose cancel, and browser
  unload triggers a best-effort cancel back to the agent.
- **`internal/room.ErrUserCancelled` sentinel.** Cancellation handling now
  uses a typed sentinel instead of string matching.

### Changed

- **`tangent.triage` now routes through the session substrate.** It is a
  thin wrapper over `tangent.session_create` plus
  `tangent.session_advance`; the public contract for v0.1 callers is
  preserved.
- **Fast-Triage has been retired in favor of Tangent.** The archived
  predecessor repo is documented in
  `docs/migrating-from-fast-triage.md`, and the local Nanite skill name
  moves from `fast-triage` to `tangent`.

### Deprecated

- The old `fast-triage` skill/command name remains as a redirect stub for
  v0.2 compatibility and is planned for removal in v0.3.

### Removed

- `triage_handler.containsUserCancel` string-match cancel detection,
  replaced by `ErrUserCancelled`.

### Fixed

_None._

### Security

- **Localhost-only bind remains unchanged.** Tangent still assumes a
  single-user trusted machine with no auth, TLS, or capability gating.
- **Persistence is local-user scoped.** The new database lives at
  `~/.tangent/tangent.db`; room state is no longer purely in-memory, but it
  remains a local user-controlled file.

## [v0.1.0] - 2026-05-08

First release. "Prove the shape." Any MCP-speaking agent can launch a
bundled Tangent workflow, the user resolves it in the browser, and the
agent receives a structured response back.

Verified end-to-end against a real Claude Code session
(`claude mcp add --transport http tangent http://localhost:7842/mcp`).

### Added

- **Go HTTP server with embedded Vite SPA.** Single binary on `:7842`,
  override via `TANGENT_HTTP_PORT`. Per-session rooms at
  `http://localhost:7842/r/<roomID>`.
- **`go-envelopes` v0.1.0 integration.** Envelope catalog loaded at
  startup (26 core kinds), TS types code-generated into
  `ui/src/generated/envelope-types.ts`, staleness gated in CI via
  `make check-envelopes`.
- **MCP server (Streamable HTTP + SSE).** Mounted on the same port as
  the SPA at `/mcp` and `/sse`. Two tools advertised:
  `tangent.list_workflows` and `tangent.triage`. Stateless +
  JSONResponse mode for v0.1, so one-shot `tools/list` and
  `tools/call` curl probes work without an `initialize` handshake.
- **WebSocket bridge with per-room state.** Each MCP triage call
  creates a fresh room; the SPA connects over WS and receives the
  envelope; user decisions flow back to the agent through the same
  room. Multi-room concurrency from day one.
- **Bundled `triage` workflow.** Plugin extension registers the
  `tangent.triage` envelope kind; the React `<Triage>` component
  renders prompt + items with Accept / Backlog / Delete decisions and
  Submit / Cancel controls. Submit is gated on every item having a
  decision.
- **Envelope component registry on the FE side.** `envelope-registry.ts`
  + `EnvelopeRouter.tsx` map envelope `type` to component, mirroring the
  Go-side plugin extension pattern. Future kinds register through the
  same surface.
- **Tooling.** `Makefile` for `build` / `dev` / `test` / `lint` /
  `generate-envelopes` / `check-envelopes`. `lefthook` pre-commit /
  pre-push hooks for Go format, vet, lint, and frontend lint /
  type-check / test.
- **Manual test recipes.** `docs/manual-tests/triage-e2e.md` (real
  Claude Code) and `scripts/triage-mock-call.mjs` (Node-driven, no
  LLM) for the same loop.
- **Documentation.** `README`, `docs/architecture.md`,
  `docs/developing.md`, `docs/mcp-integration.md`,
  `docs/mcp-smoketest.md`.

### Deprecated

_None._

### Removed

_None._

### Fixed

_None — first release._

### Security

- **Localhost-only, single-user, no auth.** Tangent listens on
  `localhost` only and assumes a trusted single-user machine. There is
  no authentication, no capability gating, and no isolation between
  rooms beyond room IDs. Distribution / trust model is a v0.6+ topic.
- **Ephemeral state.** Rooms and their envelopes live in memory for
  the lifetime of the server process. No persistence, no recovery
  across restarts.

[Unreleased]: https://github.com/hollis-labs/tangent/compare/v0.17.0...HEAD
[v0.17.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.17.0
[v0.16.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.16.0
[v0.15.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.15.0
[v0.14.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.14.0
[v0.13.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.13.0
[v0.11.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.11.0
[v0.10.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.10.0
[v0.9.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.9.0
[v0.8.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.8.0
[v0.7.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.7.0
[v0.6.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.6.0
[v0.5.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.5.0
[v0.4.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.4.0
[v0.3.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.3.0
[v0.2.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.2.0
[v0.1.0]: https://github.com/hollis-labs/tangent/releases/tag/v0.1.0
