// Validation-affordance coverage for the approval queue.
//
// The reproduction at the top of this file is the live acceptance failure that
// motivated CW-20260904-0067: a reviewer had chosen Accept on one item and
// Defer on another, had written a comment, and Submit stayed dead because the
// *separate* Defer reason field — rendered beside Action ID, marked required
// only by a placeholder — was empty. Nothing on screen said so.
//
// Each test below names the failure pattern it pins so a regression reads as
// the pattern coming back rather than as an assertion count changing.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  ApprovalQueue,
  type ApprovalQueueEnvelope,
  type ApprovalQueueResponse,
} from "./ApprovalQueue";

const envelope: ApprovalQueueEnvelope = {
  v: 1,
  id: "approval-1",
  type: "tangent.approval-queue",
  title: "Approval queue",
  data: {
    queue_id: "queue-1",
    current_index: 0,
    items: [
      { id: "item-1", title: "Update dependency", summary: "Low-risk patch" },
      { id: "item-2", title: "Enable feature flag", summary: "Needs follow-up" },
    ],
  },
};

function renderQueue(onSubmit = vi.fn<(response: ApprovalQueueResponse) => void>()) {
  render(
    <ApprovalQueue envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />,
  );
  return onSubmit;
}

describe("ApprovalQueue validation affordances", () => {
  it("explains the disabled Submit when a deferred item still owes a defer reason", () => {
    // The exact reported case: both items decided, a comment written, Defer
    // reason left empty on the item the reviewer is no longer looking at.
    renderQueue();

    fireEvent.click(screen.getByTestId("approval-queue-decision-accept"));
    fireEvent.click(screen.getByTestId("approval-queue-next"));
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));
    fireEvent.change(screen.getByTestId("approval-queue-comment"), {
      target: { value: "Talked it through with the on-call." },
    });

    // Before: Submit was disabled with nothing next to it. Now the reason is
    // adjacent, names the item, and is announced politely.
    expect(screen.getByTestId("approval-queue-submit")).toBeDisabled();
    const notice = screen.getByTestId("approval-queue-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("approval-queue-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: "Enable feature flag" is deferred and still needs a defer reason.',
    );
    expect(screen.getByTestId("approval-queue-submit")).toHaveAttribute(
      "aria-describedby",
      "approval-queue-submit-gate",
    );

    // And the requirement clears through the control the notice points at.
    fireEvent.change(screen.getByTestId("approval-queue-defer-reason"), {
      target: { value: "Waiting on a rollout window." },
    });
    expect(screen.getByTestId("approval-queue-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("approval-queue-submit-gate-reason")).not.toBeInTheDocument();
  });

  it("marks the defer reason required only while the decision is Defer", () => {
    renderQueue();

    expect(screen.queryByTestId("approval-queue-defer-reason-required")).not.toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-defer-reason")).toHaveAttribute(
      "aria-required",
      "false",
    );

    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));

    expect(screen.getByTestId("approval-queue-defer-reason-required")).toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-defer-reason")).toHaveAttribute(
      "aria-required",
      "true",
    );
    expect(screen.getByTestId("approval-queue-defer-reason")).toHaveAttribute(
      "aria-invalid",
      "true",
    );

    fireEvent.click(screen.getByTestId("approval-queue-decision-accept"));
    expect(screen.queryByTestId("approval-queue-defer-reason-required")).not.toBeInTheDocument();
  });

  it("binds the defer reason hint and error to the control for screen readers", () => {
    renderQueue();
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));

    const control = screen.getByTestId("approval-queue-defer-reason");
    expect(control).toHaveAttribute(
      "aria-describedby",
      "approval-queue-defer-reason-hint approval-queue-defer-reason-error",
    );
    expect(screen.getByTestId("approval-queue-defer-reason-error")).toHaveAttribute(
      "role",
      "alert",
    );
    // The label targets the same id the gate focuses, so "marked required" and
    // "what the gate points at" cannot drift apart.
    expect(document.querySelector("label[for='approval-queue-defer-reason']")).toBeTruthy();

    fireEvent.change(control, { target: { value: "Waiting on a rollout window." } });
    expect(control).toHaveAttribute("aria-describedby", "approval-queue-defer-reason-hint");
  });

  it("distinguishes the freeform reviewer comment from the formal defer reason", () => {
    renderQueue();

    expect(document.querySelector("label[for='approval-queue-comment']")).toHaveTextContent(
      "Reviewer comment",
    );
    expect(screen.getByTestId("approval-queue-comment")).toHaveAttribute(
      "placeholder",
      "Optional note for whoever reads this decision",
    );
    expect(screen.getByText(/separate from the/)).toHaveTextContent(
      "Optional freeform note. Kept alongside the decision, and separate from the defer reason.",
    );
    expect(screen.getByText(/a reviewer comment does not stand in for it/)).toBeInTheDocument();
  });

  it("takes the reviewer to the blocking control in another item's pane", () => {
    renderQueue();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.click(screen.getByTestId("approval-queue-decision-accept"));
    fireEvent.click(screen.getByTestId("approval-queue-next"));
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));
    // Navigate away from the offending item; the gate must still find it.
    fireEvent.click(screen.getByTestId("approval-queue-prev"));
    expect(screen.getByTestId("approval-queue-item-needs-reason-item-2")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("approval-queue-submit-gate-go"));

    const control = screen.getByTestId("approval-queue-defer-reason");
    expect(document.activeElement).toBe(control);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
    // Revealing switched the pane to item-2, so the focused control is that
    // item's defer reason rather than the one that was already on screen.
    expect(screen.getByRole("heading", { name: "Enable feature flag" })).toBeInTheDocument();
  });

  it("counts the outstanding defer reasons alongside the unresolved count", () => {
    renderQueue();

    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));

    expect(screen.getByTestId("approval-queue-counts")).toHaveTextContent(
      "2 items · 1 unresolved · 1 awaiting a defer reason",
    );
  });

  it("names the undecided items before it names the missing reasons", () => {
    renderQueue();

    expect(screen.getByTestId("approval-queue-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: 2 items still need a decision.",
    );
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));
    expect(screen.getByTestId("approval-queue-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: 1 item still needs a decision.",
    );
    expect(screen.getByTestId("approval-queue-submit-gate-more")).toHaveTextContent(
      "+1 more to resolve",
    );
  });

  it("marks the batch defer reason required and blocks Apply until it is present", () => {
    renderQueue();

    fireEvent.change(screen.getByTestId("approval-queue-batch-decision"), {
      target: { value: "defer" },
    });

    const batch = screen.getByTestId("approval-queue-batch-defer-reason");
    expect(batch).toHaveAttribute("aria-required", "true");
    expect(batch).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByTestId("approval-queue-batch-defer-reason-required")).toBeInTheDocument();
    expect(screen.getByTestId("approval-queue-batch-defer-reason-error")).toHaveTextContent(
      "Enter a batch defer reason before applying.",
    );
    expect(screen.getByTestId("approval-queue-batch-apply")).toBeDisabled();

    fireEvent.change(batch, { target: { value: "Frozen until the release lands." } });
    expect(screen.getByTestId("approval-queue-batch-apply")).not.toBeDisabled();
  });

  it("keeps the queue keyboard shortcuts working and out of text entry", () => {
    renderQueue();

    // Decision shortcuts act on the current item...
    fireEvent.keyDown(window, { key: "1" });
    expect(screen.getByTestId("approval-queue-submit-gate-reason")).toHaveTextContent(
      "1 item still needs a decision",
    );
    fireEvent.keyDown(window, { key: "ArrowRight" });
    fireEvent.keyDown(window, { key: "3" });
    expect(screen.getByTestId("approval-queue-item-needs-reason-item-2")).toBeInTheDocument();

    // ...and never while the reviewer is typing a reason.
    const control = screen.getByTestId("approval-queue-defer-reason");
    fireEvent.keyDown(control, { key: "1" });
    expect(screen.getByTestId("approval-queue-item-needs-reason-item-2")).toBeInTheDocument();
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderQueue();

    fireEvent.click(screen.getByTestId("approval-queue-decision-accept"));
    fireEvent.click(screen.getByTestId("approval-queue-next"));
    fireEvent.click(screen.getByTestId("approval-queue-decision-defer"));
    fireEvent.change(screen.getByTestId("approval-queue-defer-reason"), {
      target: { value: "Waiting on a rollout window." },
    });
    fireEvent.click(screen.getByTestId("approval-queue-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload.queue_id).toBe("queue-1");
    expect(response.payload.decisions).toEqual([
      expect.objectContaining({ item_id: "item-1", decision: "accept", defer_reason: "" }),
      expect.objectContaining({
        item_id: "item-2",
        decision: "defer",
        defer_reason: "Waiting on a rollout window.",
      }),
    ]);
  });
});
