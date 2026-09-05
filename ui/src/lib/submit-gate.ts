// submit-gate — the shared model behind "why can't I submit yet?".
//
// Every room workflow ends in one terminal CTA, and every one of them has a
// gating condition. Before this module each workflow expressed that condition
// as an inline boolean wired straight into `disabled`, which meant the reason
// the button was dead lived only in a source file. The failure that motivated
// the module: an approval-queue reviewer had chosen Defer and written a
// comment, but Submit stayed dead because a *separate* Defer reason field —
// not visibly marked required — was empty, and nothing on screen said so.
//
// A gate names each unmet requirement as data instead of folding them into one
// boolean: what remains, which control fixes it, and how to get there. That is
// what lets a workflow render an adjacent, actionable explanation next to a
// disabled CTA and take the operator to the control, rather than leaving them
// to guess.
//
// The module is deliberately presentation-free and workflow-agnostic. It does
// not own wording: each workflow phrases its own requirements in its own
// vocabulary ("still needs a decision", "no cells selected", "step 2 is
// incomplete"). Flattening those into one generic "please complete the form"
// would cost more than the consistency it bought.

import { useEffect, useState } from "react";

/**
 * One thing the operator still has to do before the terminal CTA will fire.
 *
 * `controlID` is the DOM id of the control that satisfies the requirement —
 * the same id the control's own `<label htmlFor>` points at, so marking a
 * field required and pointing the gate at it cannot drift apart.
 */
export type SubmitRequirement = {
  /**
   * DOM id of the control that clears this requirement. Empty when the
   * requirement is about the envelope rather than about a control the
   * operator can act on (an empty queue, say) — the gate still explains it,
   * it just has nowhere to send anyone.
   */
  controlID: string;
  /** Short control name, used in the "Go to …" affordance. */
  label: string;
  /** One sentence, in the workflow's own words, naming what remains. */
  message: string;
  /**
   * Optional navigation that has to run before `controlID` exists in the DOM:
   * selecting a queue item, switching a wizard step, opening a tab. Focus is
   * deferred until after the resulting render, so a reveal that changes React
   * state still lands on a mounted node.
   */
  reveal?: () => void;
};

/** The evaluated gate for one terminal CTA. */
export type SubmitGate = {
  /** True when at least one requirement is unmet. */
  blocked: boolean;
  /** Every unmet requirement, in the order the operator should address them. */
  requirements: SubmitRequirement[];
  /** The first unmet requirement — what an attempted submit focuses. */
  first: SubmitRequirement | null;
};

/**
 * Build a gate from a list of requirements, dropping the falsy entries so
 * call sites can write `condition && { ... }` inline and keep the gating
 * logic readable next to the condition it encodes.
 */
export function buildSubmitGate(
  requirements: Array<SubmitRequirement | null | false | undefined>,
): SubmitGate {
  const unmet = requirements.filter((entry): entry is SubmitRequirement => Boolean(entry));
  return {
    blocked: unmet.length > 0,
    requirements: unmet,
    first: unmet[0] ?? null,
  };
}

/**
 * Hook returning a "take me to the blocking control" callback.
 *
 * Focus is a two-step operation because `reveal` usually schedules a React
 * state change (switch step, select item) and the control it exposes does not
 * exist until that render commits. The hook records the target and moves focus
 * from an effect, which runs after the DOM is updated.
 */
export function useRevealRequirement(): (requirement: SubmitRequirement | null) => void {
  const [target, setTarget] = useState<string | null>(null);

  useEffect(() => {
    if (target === null) {
      return;
    }
    setTarget(null);
    focusControl(target);
  }, [target]);

  return (requirement) => {
    if (!requirement || requirement.controlID === "") {
      return;
    }
    requirement.reveal?.();
    setTarget(requirement.controlID);
  };
}

/**
 * Scroll a control into view and focus it.
 *
 * Exported for the handful of call sites that already know the id and have no
 * reveal step. `scrollIntoView` and the `preventScroll` focus option are both
 * guarded: happy-dom implements neither completely, and a test environment
 * gap must not turn into a thrown error in production code.
 */
export function focusControl(controlID: string): void {
  const element = document.getElementById(controlID);
  if (!element) {
    return;
  }
  if (typeof element.scrollIntoView === "function") {
    element.scrollIntoView({ block: "center" });
  }
  try {
    element.focus({ preventScroll: true });
  } catch {
    element.focus();
  }
}

/**
 * Render the ids a control needs for `aria-describedby`.
 *
 * A control is described by its hint whenever it has one, and additionally by
 * its error while it is invalid. Returning `undefined` for the empty case
 * keeps the attribute off the element entirely rather than emitting an empty
 * string, which some screen readers announce as a blank description.
 */
export function describedBy(...ids: Array<string | false | null | undefined>): string | undefined {
  const present = ids.filter((id): id is string => typeof id === "string" && id.length > 0);
  return present.length > 0 ? present.join(" ") : undefined;
}
