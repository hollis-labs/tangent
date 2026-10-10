# Writing a Tangent plugin

```bash
go run ./cmd/tangent-new-plugin -package almanac -app Almanac -hands-back-work
```

That writes the adapter and a manifest emitter. A native subprocess wrapper and a
fresh distribution bundle complete the installed plugin. The rest of this document is why it is shaped
the way it is, and which parts of it you still have to do yourself.

The scaffold is **extracted, not designed**. Two of the first-party plugins —
Torque and Tesseract, which fill the host's `tangent.app-board` kind with two
different applications' records, and which now live in
[`hollis-labs/tangent-plugins`](https://github.com/hollis-labs/tangent-plugins) — are what everything in the `application` preset was measured off. Nothing in
it anticipates a third.

The `kind` preset is the other half, and it has no shipped instance: nothing
this build ships contributes a kind through the plugin host. That is the
correction `CW-20260911-0036` made rather than a gap. `appboard` looked like
that instance and was not one — it named `tangent.app-board`, which the host
publishes, owns, versions with the repository and compiles into `ui_dist`, so
the kind went back to `extensions.RegisterAll` and the plugin was deleted.
Reach for the `kind` preset when your surface genuinely cannot fit a kind the
host already ships; the host's own board shape is not yours to re-contribute.

## Two presets, kept apart

| Preset | Ships | Knows about |
|---|---|---|
| `application` (default) | tools, a route, a mapping, a client | one application |
| `kind` | a manifest, a request schema, a renderer | no application, ever |

A plugin that did both would be a domain-free kind with one application's
concepts in it, which is the specific way this boundary rots. The generator
refuses the combination rather than trusting a reviewer to catch it.

The `application` preset is the shape you almost certainly want. It is what both
working application plugins are, and it is what the decision record
`app_plugins_are_self_contained` says the next one should be: reuse a
domain-free kind, map in userland, contribute no new kind unless a surface
genuinely cannot fit one.

## The boundary the second plugin corrected

> A plugin applies what is mechanical **in the owning application's own terms**,
> and hands back what that application makes an authored act. Which side a
> disposition falls on is the application's answer, not the plugin's.

The earlier wording — "a sync applies what is mechanical" — described Torque,
where every disposition a board offers completes inside Torque's own API. It did
not describe application plugins in general. Tesseract's promotion is a new
immutable revision carrying `supersedes`, which is the same act as a reword, so
the same board shape has to hand that one back to an agent.

`-hands-back-work` is that question asked at scaffold time. It is the one
question the two working plugins answered differently, and answering it later
means discovering it in production.

**The test is not "does the API have an endpoint for it."** Tesseract has a write
endpoint for promotion and promotion is still authored.

## What the eight-item delta decided

`CW-20260910-0054` measured eight differences between the two application
plugins. Each is either **essential** (every plugin needs it), **incidental**
(that application happened to need it), or a **trap** (something the template
should prevent). The classification is the template's design.

| # | Delta | Verdict | How it is encoded |
|---|---|---|---|
| 1 | Route names in a brief were wrong | essential | an opt-in `live_test.go`, and "fill this in first" at the top of it |
| 2 | An HTTP door's request shape ≠ its MCP tool's | essential | request-byte assertions against a wire-level fake |
| 3 | One write, not a write surface | essential | one client write method; the boundary quoted at the call site |
| 4 | Sorting was the caller's ranking choice | incidental | one sentence, no mechanism |
| 5 | The domain-free kind needed extending | **trap** | two presets that never mix; the pressure named where it is felt |
| 6 | State carried across a sync on envelope `meta` | essential, conditional | `-hands-back-work`, with the condition that makes it safe |
| 7 | The client is not shareable as-is | **trap** | generated per plugin; "do not extract a shared base" |
| 8 | No `touch` | **trap** | "a board reads; reading is not using" |

### 1 and 2 — verify against the running service

Both existing plugins were handed route names that were wrong, and both found
out by calling. An HTTP door may also take a different shape than the same
application's MCP tool: nested objects, keys that are Go field names rather than
wire names, unknown top-level fields rejected rather than ignored.

Two artifacts, because they catch different things and neither substitutes:
`live_test.go` **discovers** the truth and cannot run in CI; the request-byte
assertions in `plugin_test.go` **keep** what it found and run every build.

### 3 — one write

The scaffolded client has exactly one method that changes anything. Adding a
second is a decision about the application, not about the plugin, and the
comment at that method says so. The sync result carries `applied`, `failed` and
— when work is handed back — `requests`, so handing work back is the default
shape rather than something retrofitted once you discover you need it.

### 4 — sorting

Incidental. Tesseract's recall has ranking modes, `tangent.app-board` has no
client-side sort, and the cheap answer was to let the caller's ranking be the
sort. Torque's list has no ranking and the question never came up. What
generalizes is a preference — reach for the application's own query surface
before widening a shared kind — not a mechanism.

### 5 — the kind grows, but not here

A trap, because the shortest path is the wrong one. When a board cannot express
something, the consuming plugin is where you are already typing, so that is
where the field gets added — and a domain-free kind quietly acquires one
application's concept. `tangent.app-board` needed two additive field additions
for the second plugin, and both of them landed in the kind's own plugin.

Widening a shared kind is also not free. Every one of those additions moved
`contract_digest`, which ADR 0003 §3 makes a **version** bump rather than a
revision bump — `tangent.app-board` is at `0.2` because CW-20260911-0008
reconciled three of them at once — and a version bump takes pending interactions
of the kind out of service under §8 C1. `docs/developing.md`'s *Changing a kind
that already ships* is the procedure and the gate that enforces it.

The template keeps the presets apart, and the generated `EnvelopeType` constant
carries the rule at the point where the temptation appears.

### 6 — state on `meta`, and the condition that makes it safe

The second plugin's one architectural novelty, and the template asks about it
rather than copying it.

Outstanding work rides on the envelope because a sync **replaces** the board, so
what the participant asked for would vanish from the screen the moment they
pressed the button that submitted it. It is not draft material: ADR 0007 §5 puts
view state in the draft — what the participant is looking at and has not decided
— and the press of Sync *is* the decision, so a request is what a decision
produced.

The condition is not "meta is a good place for state". It is that every carried
item is **derived and self-clearing** against fresh application data: each one
names a record, and every way of servicing it makes that record stop coming
back, so it drops out of the carry set exactly when the work is done.

**If your carried item can only be cleared by something remembering to clear
it, it is not this.** That is plugin-owned durable state, this host has no owner
for it, and the right move is to stop and file a task rather than grow one.

A sync whose writes all complete carries nothing at all. That is the simpler
case and it is what `-hands-back-work=false` scaffolds.

### 7 — do not extract a shared client

The delta says the client "is not shareable as-is", and the trap is the
conclusion someone draws from that: two HTTP clients that look alike, so factor
out a base. That would put a second application's name in a file that is not the
one file, and "does Tangent know about X" stops being a `grep`. **Duplication
here is the boundary.**

Three properties are essential and the template emits all three: a
distinguishable unavailability error, so an operator restarts a service instead
of reading Tangent's logs; reading the plugin's own environment, because
global `GetConfig` is deliberately refused; and a readiness probe. The bearer
token is incidental — Tesseract takes one and Torque does not — so it is
`-auth`.

### 8 — a board reads; reading is not using

Tesseract has `touch` and Torque has nothing like it, so literally there is
nothing to copy. The generalization is sharp, and it is a trap because the
instinct is exactly backwards: any signal derived from access — a hit counter, a
"last viewed", a recency rank, an LRU — must not be fed by rendering a card. The
records on a board are the ones the application's own query chose, so
reinforcing them teaches that query its own guess.

## The traps the scaffold writes down

Each of these has cost someone a build or a bug already:

- **Reserved manifest properties are refused by name.** The host derives the
  digests, the granted capabilities and the effective trust assurance, and
  refuses a manifest that tries to author any of them.
- **The documentation gate fails a build for an undocumented tool.** A
  plugin-contributed tool is a shipped tool. Every one must appear in a file
  `internal/smoke/docs_test.go` names in `documentedToolFiles`, and removing the
  last mention of one fails the build too.
- **Never write a tool, kind or workflow count into prose.** That number has
  been wrong in this repository far more often than right, and a template is the
  one place a wrong number gets copied into everything that follows.
- **Markdown goes through the shared component and never a second path** — and
  which fields render as markdown is documented on the MCP tool schema, never in
  the kind's request schema. Those bytes are hashed into `contract_digest` and
  `binding_digest`, so a description there moves the pin and takes pending
  interactions of that kind out of service.
- **A bounded card set says it was bounded.** Measure your card cost against the
  kind's `inline_payload_limit_bytes`; the scaffold's numbers are another
  application's. A cut set that reports only a count reads as the whole set.
- **One file holds the application client** and nothing else in the repository
  knows that application exists.
- **Application writes run in the plugin subprocess.** The adapter package
  stays outside Tangent core and has a native wrapper in its own module.
  Application credentials remain the application's own authorization boundary.

## What the scaffold cannot do for you

The generator prints these when it runs. They are two-place edits and
measurements, and a generator that stayed silent about them would produce a
plugin that builds and does not load.

1. **Give it a home, a process, and install it.** A plugin is its own module
   and its own program, and first-party plugins live in
   [`hollis-labs/tangent-plugins`](https://github.com/hollis-labs/tangent-plugins): a `<name>/` module with
   `cmd/tangent-plugin-<name>/main.go` (modeled on
   `tesseract/cmd/tangent-plugin-tesseract`; it serves the plugin over the
   protocol-2 plugin-sdk subprocess wire and answers `--manifest`) and the plugin under
   `internal/<name>/`, added to that repository's `PLUGINS`. The generator still
   writes into this repository and does not write the program
   so move what it renders. Build the wrapper into `bin/tangent-plugin-<name>` in a fresh distribution
   directory; its `--manifest` path reads that final executable and calls the
   generated `WriteManifest` method. Write the output to `plugin.yaml` beside
   `bin/`. Source, tests and previous binaries must stay outside that inventory.
   Installation and restart require a separately approved rollout; `tangent
   plugin list` reads the installed inventory. A contributed kind
   also needs a row in `internal/envelope/extensions/register_all.go` with
   `contributedByPlugin: true`; a manifest the tree carries that nothing
   registers fails `TestPackageTreeMatchesRegistrations`, and so does the
   reverse.
2. **Document the tools**, per the gate above.
3. **Fill in the live check and run it** before writing the mapping.
4. **Measure the card bounds.**
5. **Replace `mechanical()`** with the application's answer, if you scaffolded
   with `-hands-back-work`.

The entrypoint's Init result must advertise `subprocess.ProtocolVersion` (2)
and `capability.ContractVersion` (1). The SDK validates the host's incarnation
and explicit grant array before invoking Init. Tangent currently supplies empty
grants and offers no optional profiles; a plugin cannot treat either its opaque
identity courier or its environment as an authorization grant. Protocol-1
binaries are refused before load.

Then `make verify-supported` and `make smoke`.

## Process manifest v2 and the Tangent extension

`plugin.yaml` is the SDK's manifest-v2 JSON format (JSON is a subset of YAML).
Both install and discovery use `manifest.Decode` followed by strict decoding of
`TangentExtension`; broader YAML and legacy top-level `entrypoint`, `args` or
`routes` have no fallback. Unknown, duplicate, null and trailing fields are
refused. Plugin `version` is strict SemVer at both doors, before copying an
installation or starting a child. First-party protocol-2 development builds
report `0.2.0-dev` until their first tag; Init must report the exact same version.

The common declaration carries identity, schema version 2, protocol 2,
`runtime: subprocess`, inline tool schemas and effects, configuration/secret
names, requested capabilities and the complete artifact inventory. Tangent
accepts native binary entries under `bin/`, with a SHA-256 and executable flag
for every payload file and the SDK tree digest. Scripts, symlinks, extra files,
changed bytes and changed executable flags are refused. `plugin.yaml` is the
sole excluded inventory file. Browser assets and hooks await separate host
adoption and are refused here rather than silently ignored.

`hosts.tangent` ranges over the public plugin declaration contract **1.0.0**;
`server.engines.binary` ranges over the native runner contract **1.0.0**.
These are independent of the Tangent application release and interaction
protocol. Missing Tangent ranges or incompatible host/engine ranges fail at
install and discovery. Prerelease plugin versions remain valid identities;
contract-range prereleases follow the SDK's compatibility rules.

The host block is shaped as follows (this is the extension, not a complete
process manifest):

```json
{
  "schema_version": 1,
  "kinds": [{"kind": "tangent.app-board", "package": "tangent.appboard", "version": "0.3"}],
  "routes": [{"method": "POST", "path": "/api/plugins/torque-board/sync", "capability": "draft"}],
  "mcp_tools": ["tangent.torque_open_board", "tangent.torque_sync_board"]
}
```

Kinds name exact approved definition package versions; they cannot supply
renderer or trust bytes. Install/discovery resolves the host's definition tree,
and loading requires a materialized, available definition. A contributed kind
uses the existing approved host-package registration door. A process reference
does not replace its ADR 0003 definition manifest.

Routes use literal canonical GET/POST paths owned by the plugin's final id
segment (for example `almanac` or `almanac-board`) under `/api/plugins/` and name
a participant capability: `view`, `draft`, `resolve` or `cancel`. Existing
same-origin, participant-session and capability checks run before dispatch;
cookies stay out of the child wire. MCP bindings name each common tool exactly
once, with no duplicated schemas in the extension. Tool effects are `read`,
`write` or `destructive`. Explicit MCP annotations are advertised as hints;
omitted hints use the host's conservative defaults (mutating, destructive,
non-idempotent, open-world). Effects and hints grant no authority.

The loader copies an accepted bundle to a private, verified read-only snapshot,
uses that as `PluginDir` and the working directory, and verifies it again before
each spawn, including crash recovery. `DataDir` and `CacheDir` are separate
private directories under the installation root's `.state/<id>/data` and
`.state/<id>/cache`; snapshots live under `.runtime/`. Unload removes only the
snapshot after the child stops. It preserves writable state and the source
bundle. These modes prevent accidental mutation; they are not an OS sandbox
against a process running as the same user. Existing data is never moved by
discovery or loading. A legacy bundle with writable files inside it is refused;
operator data migration is a separate decision.

`EncodeNativeManifest` inventories one final native executable from its bytes;
`EncodeManifest` accepts an explicit SDK inventory for a larger payload. The
scaffold's `WriteManifest` uses the former and emits the Tangent block and
strict development version. Its application adapter still needs the protocol-2
subprocess wrapper described above. A kind scaffold additionally requires host
approval of its definition package and renderer before it can load.

Configuration and capability requests are declarations only. Tangent keeps the
existing environment and local-MCP callback path, sends a detached reviewed
Init.Config snapshot and empty grants, and does not offer reverse host callbacks
or broker-secret capabilities. The SDK comes from the released
`github.com/hollis-labs/libs/plugin-mcp` module at v0.1.1. Plugin entrypoints
import `plugin-sdk/subprocess` and `plugin-sdk/manifest` under that module.
The SDK accepts finite forward-call `context` budgets, including Init; strict
older protocol-2 decoders that reject that field need rebuilding. This source
change does not activate or migrate any installed plugin.

CI builds first-party sources from the published immutable commit
`3efe7d72b70b23e0fe8d377d6c5e44fd617ba8f1` via `TANGENT_PLUGINS_SRC`, verifies
that checkout identity, and stages test-owned native bundles. The smoke children
use a private HOME, database, port and install root. This compatibility fixture
is separate from `tangent-plugins.version`: the held release pin still names
legacy declarations, which strict v2 intentionally refuses. Fixture success
is not rollout readiness; approved release pins and an operator rollout remain
required.

## Configuration, secrets, lifecycle and enable/disable

The host owns lifecycle intent, registration custody and reviewed flat settings.
The plugin owns the domain interpretation of those settings.

### Reviewed settings and secrets are scoped to each load

Declare scalar fields and secrets in manifest-v2 `config`. The host projects
reviewed fields through published kit-settings controls; it never parses a
plugin's domain JSON. Scalars have a private SQLite store and secrets have OS
keychain references. A browser receives presence only, never a saved secret.
Each process attempt receives a detached declared-key snapshot in Init.Config;
no ambient environment value is substituted into that snapshot. Empty Config
may retain a plugin's documented standalone environment mode, as messaging does.

Native in-process plugins receive a current owner-scoped Host. `GetConfig` reads
that incarnation's snapshot; `SetConfig` saves declared overrides without
changing the running snapshot or restarting. `RegisterConfigSchema` must agree
with the reviewed manifest. Global Host calls and stale owner calls are refused.
Subprocesses have no reverse config callback. Saving settings is revision-aware;
explicit apply/restart uses the selected revision and obtains a fresh owner.
See [plugin configuration](plugin-configuration.md) for scope precedence, typed
defaults, file/settings consumer modes and keychain failure semantics.

### Unload withdraws exactly your load owner's registrations

Load receives a scoped host handle. Registrations through it belong to that
load, not to a guessed identity derived from a tool or component name. Unload
fences the handle and dispatches, sweeps owned tools/routes/contributed kinds,
and stops the child. A late registration from the old handle is refused.
Versioned material for pinned interactions remains; core kinds and other
plugins' registrations are untouched. Release your own clients and join your
workers in `Unload` so the shared driver's bounded graceful teardown succeeds.

### Enable intent survives restart; reload obtains a fresh owner

The host's `.state/enabled.json` stores enabled booleans only. The participant
management API can enable, disable and reload an installed plugin. Reload first
stops and sweeps the old owner, then re-verifies and snapshots the installed
artifact. A fresh process receives a fresh incarnation and generation. Failure
does not leave the old registration live or masquerade as readiness.

### A defect in your plugin is contained

Tools and routes remain bounded and panic-contained. Exhausting the dispatch
budget trips the owner's circuit, fences new dispatch and tears down its child.
Caller cancellation alone does not trip the circuit. Honor cancellation and
join workers: graceful drain errors stay visible even when the driver contains
and reaps an uncooperative process. Crash recovery retains its bounded policy;
intentional disable does not restart the child.

### Being legible

`tangent.health_report` and the lifecycle management API report desired enable
intent separately from actual loaded/failed state. Failed startup and quarantined
owners remain visible. Contributed surfaces are attributed to their load owner;
direct host registrations remain host-owned.

## Stop and report rather than working around

If the work needs a change to `internal/pluginhost/`, `internal/room/`,
`internal/mcp/` or `internal/interaction/`, stop. The last two plugins each
found real host defects that way, and both became their own tasks — filing one
is a good outcome, not a delay. A plugin drives Tangent as an ordinary local
MCP client against its own tool surface (`pkg/plugin/hostclient`), with
the same authority any local caller has and no more. Reach for a
new typed host method only when a tool genuinely cannot express the need.

**A plugin imports `pkg/plugin`, never `internal/`.** `pkg/plugin` is the public
plugin surface — `ToolCaller`, `ToolResult`, `MCPTool`, `HTTPRoute`,
`Capability`, the `plugin.yaml` types (`Manifest`, `ToolDecl`, `RouteDecl`) and
`AgentTurnContractVersion` — and `pkg/plugin/hostclient` is the caller over MCP.
Everything else is the host's. The host aliases these types, so they are the
same types it registers. `internal/smoke`'s `TestPublicPluginSurfaceIsALeaf`
keeps `pkg/plugin` free of host internals, which is what lets a plugin in
another module import it.

`RegisterCRUDHandler` and `GetService` are deliberately unimplemented.
Implementing a host surface because the SDK offers it, rather than because a
consumer needs it, is the specific way ADR 0007 says this boundary rots.

## Where the examples live

`internal/plugintemplate/example/` holds two committed renders — one that hands
authored work back to the agent, one that applies everything — and a drift gate
asserts they are exactly what a fresh render produces. They are ordinary Go
packages, so `go build ./...` compiles them and their own generated tests load
them onto the real plugin host. That is what makes "the template produces a
plugin that loads" a checked claim rather than an assertion.

They have no `cmd/` program and are not a tangent-plugins module, so nothing
installs them and no build serves their tools.

### Subprocess compatibility and limits

The host requires protocol 2 and capability contract 1. Init sends a detached reviewed
configuration and an empty grant set, a fresh host-owned incarnation, and no optional
profiles. Identity must exactly match the installed manifest. Both the manifest
version and the reported version must be strict SemVer (`major.minor.patch`,
with valid optional prerelease/build metadata); `v` prefixes, missing components,
and leading zeros are refused. Reported version must exactly match the manifest,
including prerelease and build metadata. Refusals show bounded printable expected
and actual metadata, and the plugin is stopped before load or registration.

Both request and reply frames are limited to 8 MiB including the newline. An
oversized request fails locally; this pinned host currently drops an oversized
reply, so its caller waits up to the 30-second call budget. Keep replies below
the cap and paginate large results.

Activation requires approved release pins and protocol-2 binaries rebuilt from
the first tag. Install those binaries before or together with the new daemon.
With older protocol-1 binaries still installed, `/readyz` fails its plugin check,
and the installer exits non-zero after its 30-second readiness wait. Runner
capabilities return only once its compatible binary is installed. The browser
registry loader must also adopt registry v2 before this host change ships.

Plugin stderr is untrusted diagnostic text. Tangent retains a bounded tail and
removes the possibly partial leading line after truncation only when non-blank
text follows the first LF. The library repeats that rule if configured-secret
redaction expands the output and a final byte trim is needed. Windows without
such an LF remain whole within the byte cap, and can retain a partial key.
CR-only separators are not line boundaries; a cut exactly at a line boundary
can conservatively remove a complete leading line.

Credential names use their last underscore, hyphen or camelCase segment:
`token`, `secret`, `password`, `passwd`, `pass`, `passphrase`, `credential`, `jwt`
and `dsn` (including token/secret/credential plurals). A final `key` in a compound
name needs a credential prefix such as `api`, `private`, `secret`, `access`, `db`, `client`,
`session`, `refresh`, `signing` or `encryption`. Thus `OPENAI_API_KEY` and
`AWS_SECRET_ACCESS_KEY` are scrubbed while `project_key` remains diagnostic
metadata. Compact `apikey`, `MYTOKEN`, `PGPASSWORD` and `SECRET_KEY_BASE` are
recognized conventional names. Bare `key`, `auth`, `pwd` and `pass` are also
scrubbed, regardless of case; their unquoted values end at whitespace. A `PWD`
value starting with `/` remains readable as an absolute working directory.

Matching normalizes common escaped/Unicode spellings without rewriting
unrelated percent-encoded text. Values never consume a physical newline;
quoted, URL-query/form and logfmt values preserve following fields. Quoted keys
must consist of letters, digits, underscores, dots or hyphens; a quoted URL is
not a credential name. Authorization headers of any scheme, cookies, URL
userinfo, scheme-less tcp/unix DSNs, complete
private-key PEM blocks, bare JWTs and common provider token prefixes are scrubbed.
Physical line boundaries survive redaction and wrapping; display text sanitizes
control characters afterwards. Plugin directories and executables resolve to
absolute paths.

This is **best effort**, not a guarantee against arbitrary secret disclosure;
plugins must keep secrets out of their logs. Look-alike letters, combining marks,
mathematical alphabets, arbitrary mid-word wrapping, `-p` flags, passwords with
an unescaped `@` in a URL, and YAML/bare-colon credentials stay outside the rule.
Bare-colon failure prose remains readable.

Inventory keeps initial load refusals in `error`, and failures after a successful
load in `failed_after_load` and `runtime_error`. Registration history remains
recorded even when a plugin stops serving. Readiness names refusals and runtime
failures separately, using names and bounded lists with overflow counts. A plugin
that fails after loading makes `/readyz` return HTTP 503, including restart
exhaustion; an authored unhealthy answer remains a warning. A failed restart
emits a deduplicated warning when status or inventory is sampled; the shared library continues to own process supervision.
