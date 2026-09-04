// SubmitGateNotice — the adjacent, actionable explanation that sits beside a
// terminal CTA the operator cannot yet fire.
//
// It is the counterpart to ConnectionStatus's refusal line, and the two split
// the error surface along the only boundary that matters to an operator:
//
//   blocked  — this tab has not produced a submittable answer yet. Nothing has
//              been sent. Amber, polite, and always accompanied by a way to
//              reach the control that clears it.
//   refused  — a submission was sent and the server declined it. Red,
//              assertive, phrased "Not submitted: <reason>." That surface
//              belongs to ConnectionStatus (see describeServerError there);
//              this component never speaks for the server.
//
// Keeping them distinct is deliberate. A single "status" line that mixed "you
// still owe a defer reason" with "another tab holds the resolver lease" is
// what made the old view unreadable: the first is the operator's own work in
// progress, the second is a fact about the world that no amount of typing
// fixes.
//
// The notice renders its live region unconditionally so a screen reader hears
// the requirement clear, not only the requirement appear.

import { Button } from "@/components/ui/button";
import type { SubmitGate, SubmitRequirement } from "@/lib/submit-gate";
import { cn } from "@/lib/utils";

export type SubmitGateNoticeProps = {
  gate: SubmitGate;
  /** Stable test id; the "go to" control derives `${testID}-go`. */
  testID: string;
  /**
   * The workflow's own verb for its terminal CTA — "Submit", "Complete",
   * "Save and continue". The notice quotes the button the operator is looking
   * at rather than inventing generic form language.
   */
  action?: string;
  /**
   * Which shape of terminal CTA this notice sits beside. Workflows differ, and
   * the notice says what is actually true rather than flattening them:
   *
   *   disabled — the CTA is inert until the gate clears (the majority).
   *   attempt  — the CTA stays live and validates on click, because the
   *              workflow's own interaction depends on it (a picker whose
   *              Submit is the only thing that reports "nothing selected").
   */
  mode?: "disabled" | "attempt";
  /** Reveal, scroll to, and focus a blocking control. */
  onReveal: (requirement: SubmitRequirement) => void;
  className?: string;
};

export function SubmitGateNotice({
  gate,
  testID,
  action = "Submit",
  mode = "disabled",
  onReveal,
  className,
}: SubmitGateNoticeProps) {
  const first = gate.first;
  const rest = gate.requirements.slice(1);

  return (
    <div
      data-testid={testID}
      role="status"
      aria-live="polite"
      className={cn("text-xs", gate.blocked ? "text-amber-300" : "sr-only", className)}
    >
      {gate.blocked && first ? (
        <div className="flex flex-wrap items-center gap-2">
          <span data-testid={`${testID}-reason`}>
            {mode === "disabled"
              ? `${action} is disabled: ${first.message}`
              : `Cannot ${action.toLowerCase()} yet: ${first.message}`}
          </span>
          {first.controlID ? (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              data-testid={`${testID}-go`}
              onClick={() => onReveal(first)}
            >
              Go to {first.label}
            </Button>
          ) : null}
          {rest.length > 0 ? (
            <span data-testid={`${testID}-more`} className="text-zinc-400">
              +{rest.length} more to resolve
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
