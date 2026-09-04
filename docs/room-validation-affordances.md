# Room validation affordances

How a Tangent room workflow tells an operator what it still needs, and how it
reports a submission that did not happen.

This is a conventions document, not a component library tour. Every rule here
exists because a shipped room broke it. The originating one: an approval-queue
reviewer had chosen Accept on one item and Defer on another, had written a
comment, and Submit stayed dead — because a *separate* Defer reason field, two
panes away from the decision that made it mandatory, announced its requirement
only through a placeholder that vanished the moment anyone typed.

## The two error surfaces

A room has exactly two ways to tell an operator that something did not go
through, and they are never mixed.

| | **Blocked** | **Refused** |
|---|---|---|
| Means | This tab has not produced a submittable answer yet. **Nothing was sent.** | A submission was sent and the server declined it. |
| Owner | The workflow component, via `SubmitGateNotice` | `ConnectionStatus`, via `describeServerError` |
| Colour | amber | red |
| Role | `role="status"` / `aria-live="polite"` | `role="alert"` |
| Copy | `"<Action> is disabled: <what remains>."` (disabled CTA)<br>`"Cannot <action> yet: <what remains>."` (live CTA) | `"Not submitted: <reason>. <what to do>."` |
| Recovery | a **Go to <control>** button that reveals, scrolls to, and focuses the blocking control | the action that resolves the server's reason (Take over, Resync) |

Keeping them apart is the point. "You still owe a defer reason" is the
operator's own work in progress. "Another tab holds the resolver lease" is a
fact about the world that no amount of typing fixes. A single status line that
mixed the two is what made the old view unreadable.

### For server-side rejections (`CW-20260904-0123`)

Submission rejections raised on the server — schema validation, disposition
conflicts, normalizer failures — are **Refused**, not Blocked. They adopt the
right-hand column above without inventing a third surface:

1. They arrive as a `{"type":"error", code, message, envelopeId, revision}`
   frame and land in `ServerError` (`ui/src/lib/ws-client.ts`).
2. `room-lifecycle` restores the optimistically cleared envelope, exactly as it
   already does for `resolver_lease_held`, so the operator's input survives.
3. `describeServerError` in `ui/src/components/ConnectionStatus.tsx` gains a
   `case` per code. **Every string starts `"Not submitted: "`**, states the
   reason in the operator's terms, and ends with what to do next. The `default`
   branch already handles an unknown code safely; add a case when a code
   deserves better wording than its raw text, not merely because it exists.
4. Nothing is rendered inside the envelope component. A workflow component
   never speaks for the server, and `SubmitGateNotice` never renders a server
   reason.

The one Refused surface that *is* rendered inside a workflow component is
`DesignIteration`'s payload-limit refusal, and it does not break rule 4: it is
not a server rejection being relayed. It is the renderer's own refusal to make
untrusted content active, decided in the browser against the manifest's
`inline_payload_limit_bytes` before anything is sent. It is Refused rather than
Blocked because no amount of the operator's typing changes it — the agent has to
send a smaller variant. See
[`renderer-trust-classes.md`](renderer-trust-classes.md).

If a rejection is genuinely the operator's unfinished work rather than a server
decision — a field the server validates that the client could have validated —
the fix is a client-side gate requirement, not a red banner.

## The primitives

Four modules, and no others. Adding a fifth needs a reason.

- **`ui/src/lib/submit-gate.ts`** — the model. `SubmitRequirement`
  (`controlID`, `label`, `message`, optional `reveal`), `buildSubmitGate`,
  `useRevealRequirement`, `focusControl`, `describedBy`.
- **`ui/src/components/ui/field.tsx`** — `RequiredMark` (the visible badge,
  `aria-hidden` because the control carries `aria-required`) and `FieldMessage`
  (a hint or a `role="alert"` error, always with an `id` so a control can point
  at it).
- **`ui/src/components/ui/submit-gate-notice.tsx`** — the adjacent explanation.
- **`ui/src/lib/refusal.ts`** — `describeRefusal(reason, remedy)`, the Refused
  sentence itself. The reason for the fourth module, per the rule above:
  `CW-20260825-0073` added two refusal *producers* that do not arrive on the
  WebSocket error channel `describeServerError` switches on — a renderer whose
  trust class this build will not dispatch, and a sandboxed payload over its
  declared limit. Both are Refused, both are red and `role="alert"`, and neither
  has a `ServerError` to map. The alternative to a shared function was retyping
  the sentence in three files with three sets of punctuation, which is how the
  two surfaces started blurring in the first place. `describeServerError` still
  owns the *mapping* from wire code to copy; this owns only the shape.

`ui/src/components/envelopes/ApprovalQueue.tsx` is the worked exemplar. Copy its
shape.

## Rules

**Never leave a blocked terminal CTA without an adjacent explanation.** The
notice goes in the same row as the button, before it — not in a header, not in a
banner above a 640px canvas, not in the other pane.

**A gate names requirements, not a boolean.** `buildSubmitGate` takes one
`SubmitRequirement` per unmet condition. Fold them into a single `disabled`
expression and you are back to a dead button that cannot say why.

**`controlID` is a real DOM id, and it is the same id the control's
`<label htmlFor>` points at.** That is what keeps "marked required" and "what
the gate focuses" from drifting apart.

**Supply `reveal` whenever the control is not currently on screen** — another
queue item, another wizard step, another file, another tab. Focus is deferred to
an effect so a reveal that schedules a React state change still lands on a
mounted node.

**On attempted submit, go to the first blocking control.** Every submit handler
starts:

```tsx
if (gate.blocked) {
  revealRequirement(gate.first);
  return;
}
```

For a live CTA this runs on the click. For a disabled CTA it is defence in
depth, and the notice's own **Go to** button is the operator's path.

**Mark conditional requirements visibly, while the condition applies.** A
placeholder is not an affordance: it disappears on the first keystroke and
screen readers do not treat it as a requirement. Neither is a label-text swap
between two identically styled strings. Use `RequiredMark` driven by *the same
expression the gate evaluates*.

**Bind the semantics.** Every required or conditionally required control:

```tsx
<label htmlFor={ID}>Defer reason <RequiredMark active={isDeferred} /></label>
<Input
  id={ID}
  aria-required={isDeferred}
  aria-invalid={isDeferred && empty}
  aria-describedby={describedBy(`${ID}-hint`, invalid && `${ID}-error`)}
/>
<FieldMessage id={`${ID}-hint`}>…</FieldMessage>
{invalid ? <FieldMessage id={`${ID}-error`} tone="error">…</FieldMessage> : null}
```

A hint nobody references is invisible to a screen-reader user. A dangling
`htmlFor` — a label pointing at an id no element renders — is worse than none.

**Never signal state by colour alone.** A selected decision button gets
`aria-pressed`. A current file gets `aria-current`. A tab strip gets tab roles.

**An error is never rendered in success styling.** Amber or red, never emerald.

**Distinguish a comment from a reason.** A freeform note that is always optional
says so in its label and its hint, and its placeholder does not read as a
mandate. A formal reason that gates the CTA says it is the recorded reason and
that a comment does not stand in for it. Two adjacent freeform fields must never
be typographically and semantically indistinguishable.

## Workflow-specific exceptions

These are deliberate. Do not "fix" them into uniformity.

- **CTA shape is per workflow.** Most terminal CTAs are `disabled` while
  blocked. `FilePicker`, `ProgressPanel` and `Wizard` keep live buttons that
  validate on click, because that click is how those workflows report "nothing
  selected"; they pass `mode="attempt"` to the notice, which changes the copy
  from "Submit is disabled: …" to "Cannot submit yet: …". `FormCollect` passes
  the mode its button is actually in. Say what is true of the button beside you;
  do not change a button's shape to match a neighbour's.
- **Some workflows have no terminal gate, by design.** `SynthesisNotes` and
  `OutputRender` are read-only acknowledgements with no editable controls.
  `SpreadsheetReview` and `Dashboard` submit freely — an empty review and an
  unchanged dashboard are legitimate outcomes. Do not add gates to them.
- **Workflow vocabulary wins over consistency.** `BlockDraft` keeps its four
  feedback labels ("Revision notes" / "New direction" / "Edit notes" /
  "Optional note"). `ProseRevision` keeps `reason` for the agent's read-only
  justification and `comment` for the human's. `ProgressPanel` keeps a `summary`
  payload key behind an "Update note" label. Hints disambiguate; renames do not
  happen.
- **Response payloads are frozen.** Affordance work never changes a submitted
  key, shape, trimming rule, or `|| undefined` behaviour. If a fix seems to
  require one, it is not an affordance fix.
- **Draft keys are ADR 0002's.** Nine localStorage families across two
  incompatible shapes, two of them missing `encodeURIComponent`. Record what you
  find; migrate nothing here.

## Testing

Each workflow carries a `<Component>.validation.test.tsx` covering the modes
that apply to it: a hidden conditional requirement becoming visibly required, a
blocked CTA with an adjacent explanation naming what remains, comment-vs-reason
labelling, screen-reader association, first-invalid focus **and** scroll
(assert `document.activeElement` and a mocked `Element.prototype.scrollIntoView`),
keyboard operation, draft recovery, and — always — that the submitted payload is
unchanged.

`ui/src/components/envelopes/registry-smoke.test.tsx` parses both production
registries (`internal/envelope/extensions/*.go` and `ui/src/main.tsx`) from
source and fails if they disagree, so a workflow cannot ship on one side only.
