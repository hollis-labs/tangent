// Validation-affordance coverage for triage.
//
// Triage's failure was the mildest of the family and the easiest to miss: the
// only thing that ever said why Submit was dead was a header line reading
// "3 undecided", sitting above a scrolling list and naming none of the rows.
// With a long list the operator's job was to hunt for the row without a badge.
//
// Each test names the pattern it pins so a regression reads as the pattern
// coming back rather than as an assertion count changing.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Triage, type TriageEnvelope, type TriageResponse } from "./Triage";

function envelope(): TriageEnvelope {
  return {
    v: 1,
    id: "triage-1",
    type: "tangent.triage",
    title: "Inbox triage",
    data: {
      items: ["Ship the release notes", { id: "flag", title: "Enable feature flag" }],
    },
  };
}

function renderTriage(onSubmit = vi.fn<(response: TriageResponse) => void>()) {
  render(<Triage envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);
  return onSubmit;
}

describe("Triage validation affordances", () => {
  it("explains the disabled Submit next to the button and names the row that owes a decision", () => {
    renderTriage();

    expect(screen.getByTestId("triage-submit")).toBeDisabled();
    const notice = screen.getByTestId("triage-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("triage-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: 2 items still need a decision, starting with "Ship the release notes".',
    );
    expect(screen.getByTestId("triage-submit")).toHaveAttribute(
      "aria-describedby",
      "triage-submit-gate",
    );

    // Deciding the first row moves the explanation on to the next one by name.
    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));
    expect(screen.getByTestId("triage-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: "Enable feature flag" still needs a decision.',
    );

    fireEvent.click(screen.getByTestId("triage-action-flag-backlog"));
    expect(screen.getByTestId("triage-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("triage-submit-gate-reason")).not.toBeInTheDocument();
    expect(screen.getByTestId("triage-submit")).not.toHaveAttribute("aria-describedby");
  });

  it("marks each row required until it has a decision", () => {
    renderTriage();

    expect(screen.getByTestId("triage-item-item-0-required")).toBeInTheDocument();
    expect(screen.getByTestId("triage-item-flag-required")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));

    expect(screen.queryByTestId("triage-item-item-0-required")).not.toBeInTheDocument();
    expect(screen.getByTestId("triage-item-flag-required")).toBeInTheDocument();
  });

  it("names the action buttons as one group per row and announces the selection", () => {
    renderTriage();

    const groups = screen.getAllByRole("group");
    expect(groups[0]).toHaveAttribute(
      "aria-labelledby",
      "triage-item-item-0-label triage-item-item-0-decision-label",
    );
    // Both halves of the accessible name are real elements, so the group reads
    // as "Ship the release notes Decision" rather than three loose buttons.
    expect(document.getElementById("triage-item-item-0-label")).toHaveTextContent(
      "Ship the release notes",
    );
    expect(document.getElementById("triage-item-item-0-decision-label")).toHaveTextContent(
      "Decision",
    );

    expect(screen.getByTestId("triage-action-item-0-backlog")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByTestId("triage-action-item-0-backlog"));
    expect(screen.getByTestId("triage-action-item-0-backlog")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("takes the operator to the first undecided row's Accept button", () => {
    renderTriage();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.click(screen.getByTestId("triage-action-item-0-delete"));
    fireEvent.click(screen.getByTestId("triage-submit-gate-go"));

    expect(document.activeElement).toBe(screen.getByTestId("triage-action-flag-accept"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
  });

  it("leaves an item-less envelope submittable and unblocked", () => {
    const onSubmit = vi.fn<(response: TriageResponse) => void>();
    render(
      <Triage
        envelope={{ v: 1, id: "triage-empty", type: "tangent.triage", data: { items: [] } }}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("triage-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("triage-submit-gate-reason")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("triage-submit"));
    expect(onSubmit.mock.calls[0][0].payload).toEqual({ decisions: [] });
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderTriage();

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));
    fireEvent.click(screen.getByTestId("triage-action-flag-delete"));
    fireEvent.click(screen.getByTestId("triage-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response).toMatchObject({
      v: 1,
      envelopeId: "triage-1",
      kind: "data",
      status: "submitted",
      payload: {
        decisions: [
          { itemId: "item-0", action: "accept" },
          { itemId: "flag", action: "delete" },
        ],
      },
    });
    expect(typeof response.completedAt).toBe("string");
  });
});
