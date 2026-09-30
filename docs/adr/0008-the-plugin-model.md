# ADR 0008: The Plugin Model — Nanite's Registry, Runtime Bundles, and First-Party Kinds

**Status:** Accepted.

**Date:** 2026-09-11

**Approved:** 2026-09-11 by Chrispian, read end-to-end and ruled through a
Tangent approval queue — room `7c9ae9be-6cf5-4584-bd1f-21250096bbba`,
orchestrated from session `session-20260911-bd8cea23`. **The first ADR in this
set approved through Tangent rather than in chat**, which is worth the sentence:
`CW-20260911-0046` is where that flow gets built properly.

**Drafted `Proposed`, promoted on approval, and that is the standing convention
for an agent-drafted ADR** (review question 4, answered below). An agent cannot
write an approval line for a document Chrispian has not seen, so it lands
`Proposed` and only he moves it. Promotion is a status change and an approval
line, nothing else — every decision date inside this document is unchanged by it.

**Task:** `CW-20260911-0040`

**Extracted from:** [ADR 0007](0007-collaboration-surface-plugin-host-and-view-state.md)
§4. ADR 0007 remains Accepted and remains the collaboration-surface record; §4
remains its plugin host boundary. This document is not a competing decision
about that boundary — it carries the material §4 should not have been holding.

**Supersedes, in part:** ADR 0007 §4, in exactly the four ways §2 below
enumerates — two of its exclusions are **reversed** (runtime asset loading;
subprocess plugins as "a separate decision"), one is **relocated unchanged**
(plugin signing, still out of scope), and its instance stamps are **withdrawn**.
§4's boundary rule, its reserved-to-host list and its unimplemented-surfaces
posture stand unchanged and stay there.

**Baseline reviewed:** `adca3ff`.

**Source:** the ADR 0007 §4 replacement draft (internal, not published),
reviewed 2026-09-11. Tesseract `nanite_plugin_model_is_the_portfolio_model`.

## Context

ADR 0007 §4 was written on 2026-09-09 and amended twice within two days. Across
those rounds one pattern held: **§4's decisions survived and §4's instance
stamps and exclusions rotted.**

The boundary rule — the SDK says what a plugin may offer, the manifest says what
the host will let it do, a registration without a manifest is refused — has not
moved once. Everything that moved was attached to it: which plugin was "the
first" one, which mode the host runs in, what a plugin may not do yet. A third
amendment round was proposed. Chrispian rejected it in favour of a split
(2026-09-11), and the reasoning is worth more than the outcome:

> The boundary rule and the plugin model have different lifetimes. Keeping them
> in one section is what makes that section rot.

`CW-20260911-0035` is about to move the model again — the package name and
boundary for the extracted browser loader are now decided, the loader shape and
the trust answer are not. Stamping the model into an accepted ADR buys a fourth
amendment round within weeks. Separating them gives the two halves the different
lifetimes they actually have: §4 stays short and stable, and this document is
allowed to change as the model does.

**The split also dissolves a second question rather than deciding it.** The
draft asked whether naming open design questions in an ADR brushes ADR 0005 §6's
*"a boundary document holds no status."* Under the split it does not arise: the
open questions live here, in a model document that is allowed to carry them, and
§4 stays a boundary statement holding no status. The tension was an artifact of
putting both in one section.

## Decision

### 1. The standing direction, which predates this document

**From Chrispian, and predating ADR 0007.** Every app in the portfolio adopts
the `plugin-sdk` pattern, and that was the intent from day one. Nanite blazed the
trail, so Nanite leads. Tangent is the second adopter; Hadron is a future one.

A lesser compiled-in proof of concept was authorized for the Torque integration
**so it could be dogfooded** — not as the target shape. That distinction is the
one ADR 0007 §4 lost, and §5 below restores it.

### 2. What ADR 0007 §4 got wrong

Corrected here rather than amended around, and attributed so a reader can tell a
ruling from an inference.

| The §4 text said | Actually |
|---|---|
| Runtime asset loading is excluded — *"a plugin does not ship a renderer bundle into the browser at runtime"* | **Reversed.** Nanite does exactly this, and has already discarded a worse version of it. The exclusion was an agent's framing, never Chrispian's decision. An agent later built a trust-wall argument on top of it as though it were structural, which is what made the error expensive. §4's real constraint survives as open question 1 below. |
| Compiled-in is the mode; subprocess is *"a separate decision"* | **Compiled-in was the dogfood concession**, never the target. Subprocess plus runtime UI loading is the target. |
| `internal/plugins/appboard/` is *"the first plugin"* and the shipped instance of plugin-contributed kinds | **`appboard` was host plumbing, not a plugin.** It declared `publisher: tangent` and `ownership_class: host-package`, and its renderer compiles into `ui_dist`; it held no dependency, no state and no behaviour. It and the kind were born in the same commit (`7b4803e`) to give the new host a customer. Corrected in `adca3ff` under `CW-20260911-0036`: the kind registers through `RegisterAll` and the plugin is deleted. |
| *"Plugins are how new interaction kinds arrive"* | **True, scoped to first-party.** See §4 below. |

**"First plugin" was overloaded across three different things**, which is part of
how this drifted:

- `appboard` was the first *registration through the host* — and was not a
  plugin at all.
- `torqueboard` (now `internal/plugins/torque/`) was the *prototype application
  plugin*.
- The Tesseract integration was the first *built on the SDK properly*, with the
  torqueboard delta as the evidence for what generalized.

Only the third is the one that matters, and none of the three is named in §4 any
longer. An instance stamp in a boundary section is how all of this happened.

### 3. The model: Nanite's, adopted

**Decided by Chrispian, 2026-09-11:** *"We go with Nanite's model."*

Nanite's shape, read from source at Nanite's `ui/src/lib/plugin-loader.ts`
and `internal/api/plugins_registry.go`:

- The host publishes a **manifest-authoritative registry** at
  `GET /api/plugins/registry`.
- The browser **dynamic-imports** each plugin's ES module bundle and pulls the
  named exports the registry names.
- **Load failures are isolated and attributable per plugin.** One plugin's bad
  bundle is one plugin's absence, not a blank surface.
- **The host manifest is authoritative.** An earlier design in which plugins
  called `register()` at runtime was built and then discarded. A plugin does not
  declare what it registers; the host does.
- A plugin may claim an **unclaimed** name and may **never** displace a core
  one — enforced at both the frontend registry and the backend card rules, with
  a test pinning the refusal.

That last rule is the same refusal ADR 0007 §4 reached independently for MCP tool
names and envelope kinds. Two hosts arriving at it separately is portfolio
convergence rather than an import, and it is the strongest evidence the model is
right.

Adopting this reverses §4's runtime-asset-loading exclusion. It does **not**
relax §4's boundary rule: a bundle the browser loads still renders a kind whose
manifest the host resolved, and a kind with no manifest is still refused.

### 4. Kinds, and who owns which

**Decided by Chrispian, 2026-09-11.** Tangent owns a **small core set** of
interaction kinds that rarely changes. Everything else arrives as a plugin, and
**plugin-contributed kinds are first-party.**

Third-party kind contribution is **not in scope**, and nothing in this document
should be read as preparing for it. The trust model, the signing question and the
capability grants are all sized for first-party code; a third-party path would
reopen every one of them and is a separate ADR if it is ever wanted.

`register_all.go` remains the ownership table for host kinds. **A kind whose
manifest declares `ownership_class: host-package` and whose renderer ships in
`ui_dist` belongs there, not behind a plugin registration** — that is the test
`appboard` failed, stated so the next candidate can be checked against it rather
than argued about.

### 5. Compiled-in is the concession, and it retires

The host this ADR is written against compiles its plugins in. That is the
dogfood concession from §1, and recording it as the mode is what ADR 0007 §4 did
wrong.

The target is **subprocess plus runtime UI loading**, tracked by
`CW-20260910-0034`. One consequence the compiled-in framing obscured: a
goroutine leaked by a plugin handler that ignores its context lasts as long as
the process only because the plugin shares it. Subprocess mode turns *"the
goroutine leaked"* into *"the process was killed"*.

This section is about the **mode**, and says nothing about what a plugin in
either mode should be allowed to write. ADR 0007 §6 holds that question.

Nothing here sets a date. What it establishes is the direction, so that a future
reader does not find compiled-in described as the shape and conclude it was
chosen.

### 6. Where the extracted browser loader lives

**Decided by Chrispian, 2026-09-11**, closing `CW-20260911-0035`'s open *"package
name and boundary"* question.

The extracted loader lands as a **TypeScript companion package inside
`libs/plugin-sdk`**, published under `@hollis-labs/*`. Not a new repository, and
not inside `libs/design-kit`.

The reason is the one that decides it: **the registry wire contract must be
defined once, with a Go view and a TypeScript view of the same thing.** Two
repositories is where a wire contract drifts — the Go host and the browser loader
would version independently, and the first disagreement would surface as a plugin
that loads in one app and not another. `libs/design-kit` is the wrong home for the
opposite reason: the loader is not a presentation concern and design-kit has no
stake in the registry contract.

What remains open is the loader's **shape**, not its address — see open question
3.

## Open, and explicitly not decided here

These are named so that absence is not read as decision, and so the next reader
does not mistake an open question for a settled one. This document is a model
record and is allowed to carry them; ADR 0007 §4 is a boundary statement and is
not.

1. **How `core-trusted` survives runtime bundle loading.** This is the real
   constraint the reversed exclusion was standing in for, and it is the one that
   must be answered first. `docs/renderer-trust-classes.md` ties `core-trusted`
   at materialization to publisher `tangent` or `hollis-labs/go-envelopes`, a
   grantable assurance, and an **empty `renderer.asset_digest`** — on the stated
   grounds that *"a separately distributed bundle is by definition not the one
   that went through the release review."* That requirement was written when every
   renderer shipped in `ui_dist`. A plugin serving its own bundle has a digest.
   Until this is answered, no plugin-contributed kind renders above the trust
   floor.
2. **CSP and the renderer sandbox.** Presentation sandboxing per
   `docs/renderer-trust-classes.md` is Tangent's shipped isolation model, and the
   document CSP is part of it. Loading an external ES module interacts with both.
   Unresolved.
3. **The extracted loader's shape.** Its home and package boundary are decided
   (§6). What it exports, how a host configures it, and how it reports a per-plugin
   load failure are not.
4. **Plugin signing and signature verification.** Out of scope by Chrispian's
   direction, 2026-09-09, unchanged. First-party only, and `trust.assurance` is
   not relaxed. Recorded here rather than in ADR 0007 §4 because it is a property
   of the model's maturity, not of the boundary.

## Consequences

### Positive

- ADR 0007 §4 becomes a section that can stay accurate. It holds one rule, its
  consequences, and a list of surfaces this host does not implement — all of
  which have survived three amendment rounds unchanged.
- The model can change without amending ADR 0007. `CW-20260911-0035` is expected
  to move it again, and that now costs an edit to this document rather than a
  fourth amendment to §4 — which is the whole point of giving the two halves
  separate homes.
- The four corrections are attributed. A reader can tell which of §4's statements
  were Chrispian's rulings and which were an agent's framing, which is the
  distinction whose absence made the runtime-asset-loading error expensive.
- The open questions have a home that is allowed to hold them, so ADR 0005 §6
  is respected rather than argued around.

### Negative and costs

- **Two documents now describe the plugin path**, and a reader can land on either.
  Mitigated by cross-references in both directions — §4 carries an amendment note
  pointing here, and this document's header says what it extracted and what it
  superseded — but a reader who finds §4 through a `grep` and stops reading will
  see only the boundary. That is the intended failure mode (the boundary is the
  part that must not be got wrong), not an unnoticed one.
- **This document was drafted `Proposed` while the decisions inside it were
  already made** — a state the repository had not had before. Resolved on the day
  it was written, by Chrispian reading it and approving it, but the state is the
  normal one for an agent-drafted ADR and will recur: the decisions are his and
  dated, and the document recording them has not been reviewed until he reviews
  it. That gap is the convention working, not a defect in it.
- **Reversing the runtime-asset-loading exclusion opens a question the shipped
  trust model does not answer.** Open question 1 is not a formality; until it is
  answered, adopting the model in full would mean either a trust-class change or
  plugin renderers at the floor.

### Risks

- **The model ADR becomes a status inventory.** The failure ADR 0005 §6 names,
  arriving by a different route: a model document that accumulates "what ships
  today" is ADR 0006 §6 again. The guard is that every claim here is either a
  dated decision or an explicitly open question, and neither is an inventory.
- **The split is read as a demotion of §4.** It is not — §4 keeps the rule that
  governs every plugin registration, and this document is subordinate to it. If
  a future reader treats the model as overriding the boundary, that is the
  failure this ADR's header is worded to prevent.
- **`CW-20260911-0035` lands and this document is stale rather than amended.**
  Separating the model from the boundary is only an improvement if the model
  document is actually maintained; one nobody updates is worse than an amended
  §4, because §4 at least had readers. The mitigation is that it is tracked, not
  that it is well written.

## Alternatives considered

### Amend ADR 0007 §4 a third time

Rejected by Chrispian, 2026-09-11. Two amendment rounds have already shown which
half moves; a third would preserve the structure that keeps failing, and
`CW-20260911-0035` is expected to force a fourth within weeks.

### Replace ADR 0007 §4 wholesale

What the draft proposed, and rejected in favour of the split. Replacement throws
away the part that has never been wrong along with the part that keeps being
wrong, and it leaves the model and the boundary sharing a lifetime they do not
have.

### Supersede ADR 0007 entirely, as 0007 did to 0006

Rejected. 0006 was superseded because its §6 was a status inventory that was
false within two days and because Chrispian directed it. Neither applies here:
0007's other five sections are accurate, stamped, and load-bearing, and nothing
about the plugin model bears on the collaboration surface, the vocabulary or
where view state lives.

### Put the extracted loader in a new repository

Rejected — see §6. The registry wire contract would then be defined twice and
versioned independently.

### Put the extracted loader in `libs/design-kit`

Rejected — see §6. A registry loader is not a presentation concern, and
design-kit holds no stake in the wire contract it would have to track.

## Review questions

1. Is the boundary between this document and ADR 0007 §4 legible from §4 alone?
   The amendment note there is the only thing a reader who arrives by `grep`
   will see.
2. **Answered 2026-09-11 by Chrispian: no, it does not gate the extraction.**
   `CW-20260911-0035` proceeds with plugin renderers **at the trust floor**, and
   open question 1 stays open. Getting application dependencies out of the binary
   is the extraction's value and does not depend on the trust answer. The
   constraint that remains is the one already stated there: nothing renders above
   the floor until question 1 is answered.
3. Does §4 read as too thin now that the model has left it? It was thinned
   deliberately, but "short and stable" and "no longer says enough" are
   adjacent.
4. **Answered 2026-09-11 by Chrispian: yes, and it is now the standing
   convention.** An agent-drafted ADR lands `Proposed` and promotes only on his
   approval, with the decision dates left as they are. It is the mechanism that
   stops an agent stamping his authority onto a document he has not read. This
   document promoted because he approved it, not because the convention was
   waived.

## References

- [ADR 0003](0003-definition-and-package-ownership.md) — the definition manifest, package ownership
- [ADR 0005](0005-product-boundary-and-portfolio-composition.md) — the product boundary; §6 forbids the status inventory this document must not become
- [ADR 0007](0007-collaboration-surface-plugin-host-and-view-state.md) §4 — the plugin host boundary this document was extracted from
- `docs/renderer-trust-classes.md` — the shipped isolation model, and open question 1's constraint
- `docs/writing-a-plugin.md` — the scaffold and the eight-item classification it encodes
- Nanite's `ui/src/lib/plugin-loader.ts` and `internal/api/plugins_registry.go` — the model adopted in §3
