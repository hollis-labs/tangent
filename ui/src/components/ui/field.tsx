// Field affordances shared by every room workflow: the visible marker that
// says a control is required *right now*, and the message elements a control
// points `aria-describedby` at.
//
// Both exist because "required" in these workflows is frequently conditional —
// a defer reason is required only once the decision is Defer, a rejection note
// only once the mode is Reject. A static asterisk baked into a label cannot
// say that, and a placeholder ("Required when deferred") is not an affordance:
// it disappears the moment the operator types, and screen readers do not treat
// it as a requirement. RequiredMark is driven by the same condition the gate
// evaluates, so what the operator sees and what the CTA enforces are one fact.
//
// These are presentation only. The requirement itself lives in the workflow's
// submit gate (see lib/submit-gate.ts).

import type * as React from "react";

import { cn } from "@/lib/utils";

export type RequiredMarkProps = {
  /** Whether the requirement applies right now. */
  active?: boolean;
  testID?: string;
  className?: string;
};

/**
 * Visible "required" badge, shown while the requirement actually applies.
 *
 * Hidden from assistive technology on purpose: the control itself carries
 * `aria-required`, which is what a screen reader announces. Rendering both
 * would announce the requirement twice.
 */
export function RequiredMark({ active = true, testID, className }: RequiredMarkProps) {
  if (!active) {
    return null;
  }
  return (
    <span
      aria-hidden="true"
      data-testid={testID}
      className={cn(
        "ml-2 rounded bg-amber-400/15 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-[0.12em] text-amber-300",
        className,
      )}
    >
      required
    </span>
  );
}

export type FieldMessageProps = {
  /** DOM id the described control points at through `aria-describedby`. */
  id: string;
  /**
   * `hint` is standing guidance about the control. `error` is a validation
   * failure, and carries `role="alert"` so it is announced when it appears.
   */
  tone?: "hint" | "error";
  testID?: string;
  className?: string;
  children: React.ReactNode;
};

/**
 * A message bound to one control. Always rendered with an id so the control
 * can reference it — an unreferenced hint is invisible to a screen reader
 * user, which is the failure mode this replaces.
 */
export function FieldMessage({
  id,
  tone = "hint",
  testID,
  className,
  children,
}: FieldMessageProps) {
  return (
    <p
      id={id}
      data-testid={testID}
      role={tone === "error" ? "alert" : undefined}
      className={cn("text-xs", tone === "error" ? "text-red-300" : "text-zinc-500", className)}
    >
      {children}
    </p>
  );
}
