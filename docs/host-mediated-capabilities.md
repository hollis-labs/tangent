# Host-mediated capabilities

How a renderer asks Tangent to do something in the world, and what stops it
from doing so on its own authority.

Implements [ADR 0003 §2.5](adr/0003-definition-and-package-ownership.md) and
composes with [ADR 0004](adr/0004-caller-participant-and-room-access-authority.md).
Task: `CW-20260825-0077`.

## The two capability namespaces

Tangent has two capability namespaces that nearly collide. They were left
separate deliberately so this work would reconcile them consciously rather than
discover the overlap mid-implementation.

| | **Object access** | **Host-mediated effect** |
|---|---|---|
| Package | `internal/authz` | `internal/effect` |
| Go type | `authz.Capability` | `effect.Capability` |
| Members | `view`, `submit`, `draft`, `resolve`, `cancel`, `close`, `administer` | `file.read_scoped`, `file.write_scoped`, `evidence.preview`, `export.download`, `clipboard.write`, `network.fetch`, `process.exec` |
| Question | May this principal perform this operation on this Tangent object? | May this definition's renderer cause the host to act on the world? |
| Evaluated by | `authz.Authorize` | `effect.Broker.Request` |
| Source of truth | ADR 0004 §7 principal × capability matrix | The definition manifest's `required_capabilities`, intersected with host policy |

**The answer is two namespaces, two Go types, one conjunctive gate.**

They are orthogonal axes of one decision, not two points on one scale. A
participant's `resolve` says nothing about whether a renderer may read a file;
a granted `file.read_scoped` says nothing about whether this browser may answer
this interaction. Merging them would produce the confusing enum ADR 0003 §2.5
warned about; leaving them wholly independent would produce two systems that
each think they are the authority.

The single place they meet is `effect.ObjectPrecondition`. Every effect names
the object-access capability a principal must **already** hold before the
effect is considered, and `effect.Broker.Request` evaluates the conjunction:

```
object access (authz.Authorize on the precondition)
  AND the manifest declared the effect     (required_capabilities)
  AND host policy granted it               (granted_capabilities)
  AND a host-minted handle scopes it
  AND the participant expressed intent
  AND the idempotency key is unused
```

Denying any one denies the effect. `TestTheTwoCapabilityNamespacesAreDisjoint`
holds the identifier sets apart by name as well as by type, so a future
`effect.Capability("resolve")` fails the build.

The preconditions, in full:

| Effect | Object precondition | Why |
|---|---|---|
| `file.read_scoped` | `view` | Reading content a participant is already entitled to see. |
| `evidence.preview` | `view` | Same. |
| `file.write_scoped` | `draft` | Writing on the participant's behalf is authorship. It is deliberately **not** `resolve`: writing a file does not terminalize an interaction. |
| `export.download` | `view` | |
| `clipboard.write` | `view` | |
| `network.fetch` | `view` | |
| `process.exec` | `administer` | Nothing in the shipped binary holds `administer` (ADR 0004 §7), so it is refused before its (absent) executor is ever reached. |

### There was a third namespace

`hitl.ArtifactPreviewCapability{Authority, CapabilityID}` is an ad-hoc third
capability namespace that neither ADR names. Its *intent* was already correct —
its own comment says Tangent "never treats authority, artifact IDs, paths,
URIs, or action IDs in an evidence payload as permission to read or execute
anything" — but it is a separate registry with separate spelling. It is an
instance of the effect namespace, and `effect.EvidencePreview` is the
capability id it maps onto. Folding the adapter registry into the broker is
tracked as an ADR 0003 amendment below; nothing is broken today because no
preview adapter is registered in production, so `/preview` always answers
`evidence_unsupported`.

## Handles replace path strings

ADR 0003 §2.5 says "a path string is not a capability" and stops short of
saying what a renderer names instead. A **handle** is the answer.

`effect.Handle` binds a target to a class, a host-registered root, an
interaction, a participant realm, a pinned binding digest, a capability set, an
expiry, and a granted-use budget. Three properties are the whole point:

1. **A handle is minted, never asserted.** Only the host creates one, from a
   root an `effect.Authority` registered. A renderer that invents an id, or
   sends a path where an id belongs, reaches nothing. A caller-declared
   `browse_roots[].path` in a `tangent.file-picker` envelope is display text
   and stays display text forever.

2. **A handle is a locator, not a credential.** Same shape ADR 0004 §5 chose
   for room URLs. Authority to *use* one comes from the participant session
   cookie plus the interaction's pinned binding, both re-checked on every
   request. This is what lets a handle id travel in a renderer payload without
   breaking ADR 0002's rule that no short-lived grant reaches a renderer or
   browser storage: an id that leaks into a transcript grants its reader
   nothing.

3. **A handle carries no authority of its own.** It narrows an effect the
   principal could already have requested; it never widens one. Selecting a
   file mints a handle, and a handle is not permission to read.

The renderer-visible projection is `effect.View`: an id, a class, a display
label, a media type, a size, and an expiry. No root id, no path, no capability
set, no binding digest.

## What is enforced, and what is only declared

Not every effect class can be made real in a browser that runs the renderer
same-origin with the host. `effect.Mediation` records which is which, per
capability, in the type system rather than in a comment:

| Mediation | Meaning | Capabilities |
|---|---|---|
| `host` | The host is the only possible actor. Refusing the request refuses the effect. **Genuinely enforced.** | `file.read_scoped`, `file.write_scoped`, `evidence.preview` |
| `declared` | The browser hands a same-origin renderer the same power directly — `navigator.clipboard`, an `<a download>` over a Blob, a bare `fetch()`. The grant is a declaration, an audit trail, and a scoped path for renderers that cooperate. **Not a barrier against one that does not.** | `export.download`, `clipboard.write`, `network.fetch` |
| `unimplemented` | This build ships no executor. Every request is refused with `effect_unavailable`. | `process.exec` |

A `declared` capability becomes `host` for a renderer that
`CW-20260825-0073` places in a sandboxed trust class with a CSP, because then
the browser stops handing it the power. **Until that lands, nothing in the
`declared` row may be described as enforced.** The receipt says `declared` so
an audit trail cannot be misread as one.

The `MediationHost` row is real because Tangent has no other filesystem, exec,
or outbound-HTTP path at all: there is exactly one filesystem write in the Go
tree (the SQLite database), no `os/exec`, and no `http.Client`.

## Enforcement, for the file class

`effect.NormalizeRelative` is the first defense and deliberately not the only
one. It refuses leading `..`, absolute paths, Windows separators, NUL bytes,
and volume names (on every platform, not only Windows), and canonicalizes what
it accepts so two spellings of one file do not mint two handles.

`effect.OpenInRoot` is the second defense and the one that actually holds. It
uses `os.Root`, which resolves every component against an open directory
descriptor and refuses any component — including a symlink target — that leaves
the root. That closes the two holes a string check cannot:

- **Symlink escape.** `notes/link` where `link` points at `/etc/passwd` is a
  well-formed, traversal-free relative path. Only resolution catches it.
- **TOCTOU.** Between an `EvalSymlinks`-and-compare and the `os.Open` that
  follows, the same local user can replace a checked directory with a symlink.
  No amount of re-checking fixes it, because the check and the open are two
  syscalls against a name. `os.Root` walks descriptors, so there is no window.

An in-root symlink still resolves, because the rule is "may not leave the
root", not "no symlinks" — a link to a sibling file is ordinary filesystem
hygiene and a mediated root has to work on real trees.

## Receipts

Every request — granted and refused alike — writes an immutable
`effect_receipts` row. An audit trail that records only successes cannot answer
the question anyone asks it.

A receipt carries the capability, the decision and its typed code, the
mediation, the handle id, the interaction, the participant scope, the binding
digest, the idempotency key, the intent, a byte count, and a SHA-256 of the
content. It carries **no content, no path, no root id, and no session** — a
digest is an identity, not a payload (ADR 0002 §8). `effect_receipts` has no
column whose name contains `path` or `root`, and a test asserts that.

Refusal codes are fixed and none of them names what it refused, for the same
reason ADR 0004 §6.5 fixes the text of an authorization failure. Every
filesystem refusal collapses to `effect_path_refused`, so a refusal cannot be
used as an existence oracle for paths outside the root.

## Composition with a host authority

`effect.Authority` is the Cerberus Workspace composition point (ADR 0004 §10).
It supplies workspace roots, the grantable capability set, and the two
privileged-actor answers. It is an interface with **no in-tree
implementation**: Tangent never calls out to a composing product to authorize a
request, so an authority is *installed* at construction by an embedder that
already holds the trust relationship.

Composition is never mandatory. `effect.Standalone()` registers no root, grants
no capability, and holds no administrator — and that is the shipped
configuration, not a degraded one. Every shipped workflow works with no
authority present, because no shipped workflow performs a host effect.

### `PrivilegedActorPolicy`

ADR 0004 §Q10 left the choice to this task. **The decision: wire the seam, keep
the default deny.**

Granting `administer` needs a credential-custody decision ADR 0004 §12 puts out
of scope, and inventing one here would be exactly the administrator credential
ADR 0004 §7 forbids. What was wrong before was not the denial — it was that
`interaction.NewService` fell back to `denyPrivilegedActors{}` because no call
site ever constructed a policy, so the composition point had no wiring and no
test. `cmd/tangent` now constructs
`interaction.NewEffectPrivilegedActorPolicy(hostAuthority)` over
`effect.Standalone()`, and it denies.

The same authority now also populates
`definition.HostPolicy.GrantableCapabilities`, which had no producer at all
before this change.

## The transport

`POST /api/effects` — participant-session authenticated, origin guarded,
`Cache-Control: no-store`, mirroring `/api/hitl` and `/api/rooms`. One route,
not one per capability: an effect model with a route per capability has as many
places to forget a check as it has effects.

Nothing in the shipped SPA calls it. That is the honest state of v0.x: no
shipped definition declares a `required_capability`, so every request through
it is refused with `effect_capability_undeclared`, and `drift_test.go` fails
the build if a manifest starts declaring one. The refusal is the product. The
alternative — no channel at all — would mean the first renderer to need an
effect invents its own.

**When a refusal does reach the SPA it belongs to
`ConnectionStatus.describeServerError`** — the red `role="alert"` surface,
strings starting `"Not submitted: <reason>. <what to do>."`
(see [`room-validation-affordances.md`](room-validation-affordances.md)).
No cases are added there yet, deliberately: with no caller, a `case` per
refusal code would be untestable dead TypeScript, and that document's own rule
is to add a case when a code deserves better wording than its raw text, not
merely because the code exists. The constraint is recorded here so the first
implementer extends that function rather than inventing a second pattern.

## Compatibility adapters

| Path | Before | After |
|---|---|---|
| `tangent.file-picker` | `browse_roots[].path` was a raw caller-declared absolute path, carried through with trim and dedupe. Selection refs were already hardened to `artifact://` with a normalized relative path. | Unchanged on the wire. The path is display text and can never become a mediatable root — only an `effect.Authority` registration can, and it is keyed by root id. Minting a handle against an unregistered root id is refused. |
| `tangent.diff-review` | `before_ref.uri`, `after_ref.uri`, and `export_refs[].uri` had **no scheme validation at all**, unlike the structurally identical fields in file-picker and whiteboard. | Held to `artifact://` through the shared `room.NormalizeArtifactURI`. |
| `tangent.form-collect` attachments | `attachment_refs[].uri` had no scheme validation. | Held to `artifact://`. The rule is spelled inside the package, because a publisher package owns its own validation (ADR 0003 §5). |
| `tangent.output_render` export | `filename` reached `link.download` after `TrimSpace` alone, so a caller chose the name a file landed under in the operator's Downloads folder — separators included. | Reduced to a safe basename by `room.SafeExportFilename`. |
| `tangent.whiteboard` assets | `assets[].source` rejected only `data:` and `blob:`. An `https://…` was persisted and written into a tldraw asset record's `props.src`, making the operator's browser fetch an origin the caller chose. | Held to `artifact://`, the same rule `uri` always had. |
| HITL evidence preview | `ArtifactPreviewCapability{Authority, CapabilityID}`, an undocumented third capability namespace. | Mapped onto `effect.EvidencePreview`; the registry itself is unchanged pending the ADR 0003 amendment below. |

## What could not be made real

Stated plainly, because a stub that looks enforced is worse than an honest gap.

1. **`clipboard.write` cannot be enforced.** `navigator.clipboard.writeText` is
   the renderer's own DOM API. The host can declare, scope, and audit it; it
   cannot prevent it. Four shipped components call it directly today and this
   change does not stop them.

2. **`export.download` cannot be enforced.** A same-origin renderer can build a
   `Blob` and an `<a download>` without asking. Five shipped components do.
   What the host *can* control is the half it owns — the caller-supplied
   filename — and that is now sanitized.

3. **`network.fetch` cannot be enforced without a CSP.** There is no
   `Content-Security-Policy` on the SPA document at all, so a renderer can
   `fetch()` any origin. Closing the whiteboard-asset hole at the *authority*
   (rejecting the source string) is real and lands here; closing it at the
   *browser* needs a document CSP, which belongs with renderer trust classes.

4. **`process.exec` has no executor and is not implemented.** It is reserved so
   the manifest format does not change later. It is refused twice over.

5. **`file.write_scoped` follows an in-root symlink.** `os.Root` cannot be
   asked for `O_NOFOLLOW` portably from the `os` package. The write is
   contained — it cannot leave the root — but it can land on a link's target
   rather than on the link. No shipped definition declares the capability.

6. **The manifest's `Capability.scope` object is not consumed.** ADR 0003 §2.5
   describes it as "allowed roots, allowed origins, byte ceilings". This model
   answers two of the three from the host instead — roots come from an
   `Authority` registration and byte ceilings from the `Root` — which is the
   stronger arrangement, because which directories a renderer may reach is the
   host's answer and not the publisher's. But it means a publisher-authored
   `scope` is descriptive rather than enforced today, and allowed-origins has
   no enforcement anywhere. Proposed as an ADR 0003 amendment rather than
   silently reinterpreted.

7. **Handles are not revoked when their interaction terminalizes.**
   `SQLStore.RevokeHandlesForInteraction` exists and nothing calls it: wiring
   it needs a hook in the interaction service's terminalize path, and that
   transaction is not this task's to widen. The practical exposure is bounded
   by the 15-minute handle lifetime and by the fact that no handle is minted in
   production, but the rule "a terminal interaction leaves no live grant" is
   stated and not yet enforced.

8. **Nothing exercises the model end to end in production.** No shipped
   definition declares a capability, so `granted_capabilities` is empty
   everywhere and every effect request is refused. The model is proven by
   tests and by a composed-authority fixture, not by a shipped workflow.
