# Renderer trust classes and presentation sandboxing

What each class of renderer may do, where its code runs, and which of those
limits the browser actually enforces.

Implements [ADR 0003 §2.3 and §2.7](adr/0003-definition-and-package-ownership.md),
generalizes the design-iteration sandbox to every untrusted renderer, and
completes the three capabilities
[`host-mediated-capabilities.md`](host-mediated-capabilities.md) could not.
Task: `CW-20260825-0073`.

## The five classes

A manifest **requests** a class; Tangent decides. The decision produces two
facts, and everything else in this document follows from them: an **isolation**
(where the code runs) and a **capability ceiling** (what the class may ever
declare).

| Class | Isolation | Publisher code runs? | Ambient host authority | Renderer shapes | Capability ceiling |
|---|---|---|---|---|---|
| `core-trusted` | `main-origin` | yes, reviewed with the release | **yes** | `react-component`, `declarative` | all seven, `process.exec` included |
| `portfolio-trusted` | `main-origin` | yes, portfolio package | **yes** | `react-component`, `declarative` | six — no `process.exec` |
| `declarative` | `host-primitive` | **no** | n/a — nothing publisher-authored executes | `declarative` | none |
| `sandboxed-code` | `sandboxed-frame` | yes, untrusted | **no** | `sandboxed-frame` | five — no `file.write_scoped`, no `process.exec` |
| `external-surface` | `external-surface` | not in Tangent | **no** | `external-surface` | none |

The ceilings are strictly ordered —
`core ⊃ portfolio ⊃ sandboxed ⊃ declarative = external = ∅` — which is what
makes ADR 0003 §8 C2's "raising `renderer.trust_class` is a major version bump"
a statement about a set rather than about a label.
`TestTheCapabilityCeilingsAreStrictlyOrdered` holds it.

**Isolation is not the renderer's declared shape.** `RendererClass` is the
publisher's answer to "what shape is my renderer"; `Isolation` is the host's
answer to "what can the browser reach from there". A manifest whose shape and
class disagree — a `react-component` calling itself `sandboxed-code`, a
`sandboxed-frame` calling itself `core-trusted` — is refused at parse time in
both directions. The pre-existing rule caught one of those twenty pairs.

## How each boundary is enforced

### `main-origin` (core-trusted, portfolio-trusted)

Nothing contains code in this isolation, because nothing is trying to: the
material shipped and was reviewed with the release. The enforcement is
**admission**, not containment, and it happens twice:

- **At materialization.** `core-trusted` requires publisher `tangent` or
  `hollis-labs/go-envelopes`, a grantable assurance, and an empty
  `renderer.asset_digest` — a separately distributed bundle is by definition not
  the one that went through the release review. An application package from
  another publisher cannot reach the class however it is reviewed downstream.
- **At dispatch.** `EnvelopeRouter` classifies every kind against the generated
  binding table before rendering it. A registered component whose kind no
  manifest classifies is refused rather than drawn.

The document CSP applies here too, and it is what changed `network.fetch`.

### `sandboxed-frame` (sandboxed-code)

Three independent layers, and the security property is in what is *absent* from
each.

1. **`sandbox="allow-scripts"`, and nothing else.** No `allow-same-origin`, so
   the frame has an opaque origin: no access to Tangent's DOM, storage,
   cookies, session, or same-origin APIs, and no way to call `/api/effects` as
   the participant. No `allow-downloads`, `allow-forms`, `allow-popups`,
   `allow-top-navigation`, or `allow-modals`.
2. **A `default-src 'none'` frame CSP** whose `script-src` is a **hash of
   Tangent's own shim and nothing else**. This is the change that turns "no
   script execution unless the definition declares and policy grants it" from
   aspiration into fact: the previous policy said `'unsafe-inline'`, which
   admitted the shim *and* every script an agent happened to write into a
   preview. `connect-src 'none'`, `form-action 'none'`, and `base-uri 'none'`
   close network, forms, and relative-URL rewriting independently of the sandbox
   attribute, so removing either mechanism alone does not open a hole.
3. **`Permissions-Policy: clipboard-write=(self)`** on the Tangent document.
   `self` is the main origin; an opaque-origin frame is not `self`.

### `host-primitive` (declarative) and `external-surface`

Neither runs publisher-authored code, so neither has a renderer to grant an
effect to. Their ceilings are empty rather than small.

## The `postMessage` rule

`readSandboxMessage` in `ui/src/lib/sandbox-frame.ts` is the only place in the
SPA that decides whether a message from a frame is real, and it requires **all
four** of the direction document's checks:

| Check | Requirement |
|---|---|
| Source | `event.source` is present **and** identical to the mounted frame's `contentWindow`, which must itself be present |
| Origin | `event.origin === "null"` — the opaque origin a correctly sandboxed frame has |
| Nonce | matches the per-mount nonce handed to that frame in its payload |
| Schema | type matches, `action_kind` is `click-region`, `action_id` is a non-empty string |

**A provenance check that is skipped when its input is absent is not a check.**
The listener this replaces read
`if (iframeWindow && event.source && event.source !== iframeWindow) return;`,
which skips itself whenever either operand is missing — a message arriving
before the iframe mounted, or one forged with a null source, reached `onSubmit`
unchecked (`CW-20260904-0138`). Every branch here refuses on absence.

The origin check has a second effect worth naming: if someone removes the
sandbox attribute, the frame gets Tangent's real origin, the check fails, and
the workflow breaks. A weakened sandbox becomes a visible failure rather than a
silent escalation.

Only `click-region` crosses the boundary. Buttons and text inputs are the
parent's own controls, so accepting those kinds from inside the sandbox would
let untrusted markup synthesize a participant act it has no affordance for.

## Keyboard and screen-reader access inside the sandbox

The click regions the shim binds are real controls. Each one gets `tabindex`,
`role="button"`, an `aria-label`, a `keydown` handler for Enter and Space, and a
visible focus ring; the parent announces how many regions the preview contains
before an operator enters the frame. Previously a region got a `click` listener
and nothing else, which meant a sandboxed renderer whose only affordance is a
mouse click — unreachable by keyboard, invisible to a screen reader.

That is an acceptance-criterion-3 matter and not a polish item: a renderer that
cannot present its declared interaction to a keyboard operator has not preserved
the interaction's semantics for that operator.

## What the document policy changed, capability by capability

`effect.Mediation` is now a function of the capability **and** the isolation,
because the same `clipboard.write` is a bare DOM call from Tangent's own origin
and an impossibility from an opaque-origin frame.

| Capability | main-origin | sandboxed-frame | What makes it so |
|---|---|---|---|
| `file.read_scoped`, `file.write_scoped`, `evidence.preview` | host | host | Tangent has no other filesystem path at all |
| `network.fetch` | **host** (was `declared`) | host | `connect-src 'self' ws://<host> wss://<host>` — the one directive governing `fetch`, `XHR`, `WebSocket`, `EventSource`, and `sendBeacon` |
| `export.download` | **declared** | host | No CSP directive covers downloads. `allow-downloads` is a frame property and cannot apply to a top-level document |
| `clipboard.write` | **declared** | host | CSP has no clipboard directive; Permissions Policy `clipboard-write=(self)` denies frames and permits the host tree |
| `process.exec` | unimplemented | unimplemented | No executor, and an object precondition nothing holds |

`network.fetch` becoming `host` has a consequence worth stating plainly: with no
performer registered, a declared and granted `network.fetch` is now **refused
with `effect_unavailable`** instead of being admitted and left to the renderer.
That is the honest behaviour — the host is the only possible actor and the host
cannot do it — and it is a behaviour change, not only a label change.

Every receipt records `renderer_trust_class` and `renderer_isolation` beside
`mediation` (migration `0010`), because without the isolation the mediation
column is not interpretable: `clipboard.write / mediation: host` is a true row
about a sandboxed frame and a false one about Tangent's own tree.

## Where a denial shows up

| Denial | Where it surfaces |
|---|---|
| Trust evidence does not support the requested class | `quarantined` + `capability-denied`, `quarantine_reason` names the evidence that was missing |
| Trust class does not permit a declared capability | `quarantined`, `trust_denied_renderer_effect_capabilities` reports it **separately** from a host-policy denial |
| Host policy did not grant a permitted capability | `denied_renderer_effect_capabilities`, as before |
| Renderer shape and trust class disagree | manifest fails to parse; the definition never registers |
| Effect requested in an isolation with no executor | `effect_unavailable` receipt, with the isolation recorded |
| Kind reached the browser with no classification | `EnvelopeRouter` red `role="alert"`, `"Not submitted: … ."` |
| Untrusted payload over the manifest's inline limit | same surface, inside the renderer, with the size named |

The two capability denials stay separate deliberately: an operator who widens
`GrantableCapabilities` widens nothing the trust ceiling already closed, and
needs to be told that rather than sent to edit a policy that will not help.

The refusal *sentence* has one owner — `describeRefusal` in
`ui/src/lib/refusal.ts` — so the trust surfaces read identically to
`ConnectionStatus.describeServerError` without a second pattern. No case was
added to `describeServerError` itself: a definition this host will not serve is
refused before an envelope is pushed, and its error code reaches the MCP caller
rather than the browser's socket. A `case` for a code that cannot arrive is
untestable dead TypeScript.

## What is not enforced

Stated plainly, because a sandbox that looks enforced and is not is worse than
an honestly labelled declarative one — which is exactly why `CW-20260825-0077`
invented `MediationDeclared` rather than claiming three capabilities it could
not deliver.

1. **`clipboard.write` and `export.download` are still only declared in
   Tangent's own origin.** There is no CSP directive for either. Setting
   `clipboard-write=()` would enforce the first, and would break three shipped
   `core-trusted` components that copy to the clipboard **without declaring the
   capability**. Making them declare it is a capability backfill, not a trust
   boundary, and it is not done here. Five more components build a Blob and
   click an `<a download>`; the same applies.

2. **No test in this repository observes a browser enforcing anything.** Vitest
   runs against happy-dom, which parses `sandbox` attributes and CSP meta tags as
   text. The suites prove that the policy Tangent hands the browser denies
   navigation, network, forms, and downloads by construction and cannot be
   weakened silently. Actual enforcement is verified by hand —
   [`manual-tests/renderer-sandbox-e2e.md`](manual-tests/renderer-sandbox-e2e.md).

3. **`portfolio-trusted` is not cryptographically distinguished from
   `core-trusted` today.** Its defining evidence is a signature, `signed-package`
   has no verifier, and `Assurance.Grantable()` therefore refuses it. What
   separates the two classes in this build is the capability ceiling and the
   release-provenance rule, not a signature check. An out-of-tree portfolio
   package is refused at the assurance gate rather than admitted unverified,
   which is the fail-closed direction, but the class is a declaration until a
   verifier exists.

4. **The document CSP omits `default-src`, and therefore `frame-src`.**
   Browsers disagree about whether an `about:srcdoc` frame is subject to
   `frame-src`, and getting it wrong breaks `tangent.design-iteration` outright
   in a way no test here can catch. Frame embedding is left to the `sandbox`
   attribute and the frame's own policy. Defense-in-depth was traded for a
   guarantee that the shipped workflow runs; the trade is recorded rather than
   discovered.

5. **`make dev` relaxes `script-src` to `'unsafe-inline'`.** Vite injects inline
   module scripts no hash can cover, and a policy carrying both a hash and
   `'unsafe-inline'` ignores the latter entirely. The *frame's own* policy still
   carries the hash, so untrusted scripts inside a preview stay blocked in dev
   too; what dev loses is the parent policy's redundancy.

6. **The trust class gates dispatch, not the component's authority.** A
   `react-component` in `main-origin` runs with the document's full authority by
   construction — that is what the isolation means. `classifyRenderer` decides
   whether code runs; it cannot decide what it reaches once it does. Containment
   for untrusted code is the sandboxed frame, and there is no third option in
   this design.

7. **`renderer.entry` is still not what loads a renderer.** `main.tsx` still
   registers by string literal, checked against the generated table by a drift
   test. Every renderer is now classified and every dispatch is gated by that
   classification, which is what this task needed; resolving an entry specifier
   to a module at runtime is a bundling change with no trust consequence, and it
   remains `CW-20260825-0074`'s open leak.

8. **The policy admits one external origin: `https://cdn.tldraw.com`.**
   `tangent.whiteboard` loads tldraw's fonts, icons, watermark, and embed icons
   from that CDN. That dependency is the concrete reason whiteboard is
   `portfolio-trusted` rather than `core-trusted` — it is a package Tangent
   hosts and did not author, reaching an origin Tangent does not control — and
   it is admitted for **passive subresources only** (`img-src`, `font-src`).
   It is deliberately absent from `connect-src`, so tldraw's scripted fetch of a
   non-English translation bundle is refused: that is a `network.fetch` the
   whiteboard manifest has never declared, and refusing it is the model working
   rather than a bug. The honest fixes are to self-host the assets or to declare
   and grant the capability; neither is done here. **This one was found by
   reading the shipped bundle, not by a test — nothing in CI would have caught
   a renderer reaching a CDN.**

9. **`Capability.scope`'s allowed-origins list is still unconsumed.** ADR 0003
   §2.5 describes a publisher-authored origin allowlist; `connect-src` now
   enforces a *host* allowlist that happens to be stricter than any publisher
   could ask for. The publisher-authored field remains descriptive, as
   `CW-20260825-0077` reported.
