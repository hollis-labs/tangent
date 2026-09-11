# Writing a Tangent plugin

```bash
go run ./cmd/tangent-new-plugin -package almanac -app Almanac -hands-back-work
```

That writes a plugin that loads. The rest of this document is why it is shaped
the way it is, and which parts of it you still have to do yourself.

The scaffold is **extracted, not designed**. Three plugins exist —
`internal/plugins/appboard` contributes a domain-free kind, and
`internal/plugins/torqueboard` and `internal/plugins/tesseract` fill it with two
different applications' records — and everything in the template was measured
off those three. Nothing in it anticipates a fourth.

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
`GetConfig` is deliberately unimplemented; and a readiness probe. The bearer
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
- **A plugin that writes to its application declares the ADR 0007 §6 exception
  in its package doc**, in the terms the existing plugins use. The amendment
  makes "who is inside the exception" a `grep` rather than a memory, and that
  only holds if each plugin writes it down.

## What the scaffold cannot do for you

The generator prints these when it runs. They are two-place edits and
measurements, and a generator that stayed silent about them would produce a
plugin that builds and does not load.

1. **Register it.** `internal/plugins/shipped.go`, after the plugin that
   contributes the kind it fills — the host refuses a plugin whose stated
   dependency is not already loaded. A contributed kind also needs a row in
   `internal/envelope/extensions/register_all.go` with `contributedByPlugin:
   true`; a manifest the tree carries that nothing registers fails
   `TestPackageTreeMatchesRegistrations`, and so does the reverse.
2. **Document the tools**, per the gate above.
3. **Fill in the live check and run it** before writing the mapping.
4. **Measure the card bounds.**
5. **Replace `mechanical()`** with the application's answer, if you scaffolded
   with `-hands-back-work`.

Then `make verify-supported` and `make smoke`.

## Configuration, secrets, lifecycle and enable/disable

Four questions every plugin author asks. All four are answered, and three of the
answers are a refusal with a reason (`CW-20260910-0036`).

### Your plugin reads its own environment. The host holds no config.

`GetConfig`, `SetConfig` and `RegisterConfigSchema` are unimplemented, and that
is the ratified answer rather than a gap waiting to be filled. **The host holds
no plugin configuration, so it can never hold a plugin's secret.** ADR 0005
§3.1 keeps a secret boundary — Tangent does not store or rotate provider
secrets — and a config surface here would be the obvious place to put a
credential. Holding nothing keeps that boundary true by construction: there is
no store to leak, none to migrate, and none to redact out of a health report.

So read a base URL and, where one is needed, a token from your own process
environment. Both shipped plugins do; the scaffold writes it for you. Name the
variables after your plugin and document them where an operator will look.

What would reopen it is a plugin with no process environment to read — in
practice a subprocess plugin under `CW-20260910-0034` — and the secret question
gets answered before the surface gets built.

### Unload drops your state and unregisters nothing

That is the host's contract, in `internal/pluginhost/lifecycle.go`, and not a
decision each plugin makes. An envelope kind cannot be removed because
go-envelopes' registry is boot-time and has no removal at all; a contributed
tool or route cannot be removed because the SDK passes no caller identity to a
registration call, so the host does not know which plugin registered which
surface and has nothing to select.

Your `Unload` therefore drops what you hold — a cached host handle, a client, a
status — and nothing else. A tool whose plugin has unloaded still dispatches,
into a plugin that now answers "not loaded"; a readable refusal beats a surface
that answers nothing. The host calls `UnloadAll` on the way out of the process.

The first host shipped a load-failure cleanup path that could not have worked —
it looked like a rollback and removed nothing. Refusing honestly is what
replaced it.

### There is no enable/disable flag, deliberately

For a compiled-in plugin the enable set is `internal/plugins/shipped.go`:
in the slice is enabled, out of it is disabled, and changing that is a rebuild.
Nothing has a caller for a runtime toggle. Tether's catalog has the flag and the
failure mode that came with it — an entry marked enabled but unreachable stalls
its proxy for 120 seconds — and the way not to inherit that is not to build the
flag until something needs it.

### A defect in your plugin is contained, not tolerated

Every contributed tool and route is wrapped at registration
(`internal/pluginhost/isolation.go`). A panic comes back as a named refusal
rather than taking the process down, and a handler that does not return within
its dispatch budget is abandoned — the *caller* is released, which is the only
promise a compiled-in host can honestly make, because Go cannot interrupt a
goroutine that ignores its context. Shutdown releases every in-flight dispatch
at once rather than waiting out the budget.

None of that makes a hang cheap. The leaked goroutine is real, it lasts as long
as the process does, and `tangent.health_report` will not tell you about it.
Honor your context.

### Being legible

`tangent.health_report` carries a plugin inventory: which plugins loaded, which
refused and why, and what was contributed between them. The contributed lists
are host-wide and not attributed to a plugin, for the same reason `Unload`
cannot unregister — the host does not know who registered what.

## Stop and report rather than working around

If the work needs a change to `internal/pluginhost/`, `internal/room/`,
`internal/mcp/` or `internal/interaction/`, stop. The last two plugins each
found real host defects that way, and both became their own tasks — filing one
is a good outcome, not a delay. A plugin drives Tangent through
`pluginhost.ToolCaller`, an in-process MCP client against Tangent's own tool
surface with the same authority any local caller has and no more. Reach for a
new typed host method only when a tool genuinely cannot express the need.

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

They are not in `internal/plugins/shipped.go`, so this build serves none of
their tools.
