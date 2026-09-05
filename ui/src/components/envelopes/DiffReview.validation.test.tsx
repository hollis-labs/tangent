// Validation-affordance coverage for the diff review.
//
// The failure this pins: the reviewer sees "Submit Review" dead at the bottom
// of the right-hand pane with nothing next to it, while the only thing that
// says which files are still untouched is the file list in the *left* column,
// and the buttons that clear the gate render only for whichever file happens
// to be active. Three separate places, none of them adjacent, none of them
// naming the others.
//
// Each test names the pattern it pins so a regression reads as the pattern
// coming back rather than as an assertion count changing.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DiffReview, type DiffReviewEnvelope, type DiffReviewResponse } from "./DiffReview";

const envelope: DiffReviewEnvelope = {
  v: 1,
  id: "diff-1",
  type: "tangent.diff-review",
  title: "Diff review",
  data: {
    review_id: "review-1",
    files: [
      {
        id: "file-1",
        path: "pkg/app.go",
        summary: "Tighten validation",
        hunks: [
          {
            id: "hunk-1",
            header: "@@ -1,3 +1,4 @@",
            before: "return nil",
            after: "return validate()",
            action_options: [{ id: "follow-up", label: "Follow-up" }],
          },
        ],
      },
      {
        id: "file-2",
        path: "ui/view.tsx",
        summary: "Polish spacing",
        hunks: [{ id: "hunk-2", header: "@@ -2,3 +2,4 @@", before: "gap-2", after: "gap-3" }],
      },
    ],
  },
};

function renderReview(onSubmit = vi.fn<(response: DiffReviewResponse) => void>()) {
  render(<DiffReview envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-a" />);
  return onSubmit;
}

describe("DiffReview validation affordances", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("explains the disabled Submit and names the file that still owes a decision", () => {
    renderReview();

    expect(screen.getByTestId("diff-review-submit")).toBeDisabled();
    const notice = screen.getByTestId("diff-review-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("diff-review-submit-gate-reason")).toHaveTextContent(
      'Submit Review is disabled: no decisions are recorded yet — "pkg/app.go" still needs one.',
    );
    expect(screen.getByTestId("diff-review-submit")).toHaveAttribute(
      "aria-describedby",
      "diff-review-submit-gate",
    );

    // And it clears through the control the notice points at.
    fireEvent.click(screen.getByTestId("diff-review-hunk-decision-hunk-1-accept"));
    expect(screen.getByTestId("diff-review-submit")).not.toBeDisabled();
    expect(screen.queryByTestId("diff-review-submit-gate-reason")).not.toBeInTheDocument();
    expect(screen.getByTestId("diff-review-submit")).not.toHaveAttribute("aria-describedby");
  });

  it("switches the active file before focusing the decision that clears the gate", () => {
    renderReview();
    const originalScrollIntoView = Element.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    try {
      // Look at file-2 while file-1 is the one the gate names. The decision
      // buttons for file-1 are not mounted at all until the reveal runs.
      fireEvent.click(screen.getByTestId("diff-review-file-file-2"));
      expect(
        screen.queryByTestId("diff-review-hunk-decision-hunk-1-accept"),
      ).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("diff-review-submit-gate-go"));

      const control = screen.getByTestId("diff-review-hunk-decision-hunk-1-accept");
      expect(document.activeElement).toBe(control);
      expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
      // Revealing moved the right-hand pane to file-1, and the left-hand list
      // says so programmatically rather than by border colour alone.
      expect(screen.getByTestId("diff-review-file-file-1")).toHaveAttribute("aria-current", "true");
      expect(screen.getByTestId("diff-review-file-file-2")).not.toHaveAttribute("aria-current");
    } finally {
      Element.prototype.scrollIntoView = originalScrollIntoView;
    }
  });

  it("keeps the existing gate: one decision anywhere is enough, not one per file", () => {
    renderReview();

    fireEvent.click(screen.getByTestId("diff-review-hunk-decision-hunk-1-accept"));

    // file-2 is still undecided, and Submit is deliberately live anyway — this
    // pass made the existing gate legible, it did not tighten it.
    expect(screen.getByTestId("diff-review-file-file-2")).toHaveTextContent("0/1 decided");
    expect(screen.getByTestId("diff-review-submit")).not.toBeDisabled();
  });

  it("labels the review comment and names the decision that makes it the formal reason", () => {
    renderReview();

    const comment = screen.getByTestId("diff-review-hunk-decision-hunk-1-notes");
    expect(comment).toHaveAttribute("id", "diff-review-hunk-decision-hunk-1-notes");
    expect(
      document.querySelector("label[for='diff-review-hunk-decision-hunk-1-notes']"),
    ).toHaveTextContent("Review comment");
    // The placeholder is no longer the only thing describing the field, and no
    // longer reads as an instruction to write something.
    expect(comment).toHaveAttribute("placeholder", "Note recorded with this decision");
    expect(comment).toHaveAttribute(
      "aria-describedby",
      "diff-review-hunk-decision-hunk-1-notes-hint",
    );
    expect(
      document.getElementById("diff-review-hunk-decision-hunk-1-notes-hint"),
    ).toHaveTextContent(
      /Optional freeform note.*recorded reason when the decision is Request Changes.*whole substance of a Comment decision/,
    );
    // Still optional on every decision: nothing claims it is required.
    expect(comment).not.toHaveAttribute("aria-required", "true");
    expect(screen.queryByText("required")).not.toBeInTheDocument();
  });

  it("exposes the decision buttons as one labelled choice", () => {
    renderReview();

    const group = screen.getByRole("group", { name: "Decision for pkg/app.go @@ -1,3 +1,4 @@" });
    expect(group).toBeInTheDocument();

    const accept = screen.getByTestId("diff-review-hunk-decision-hunk-1-accept");
    const reject = screen.getByTestId("diff-review-hunk-decision-hunk-1-reject");
    expect(accept).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(accept);
    expect(accept).toHaveAttribute("aria-pressed", "true");
    expect(reject).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(reject);
    expect(accept).toHaveAttribute("aria-pressed", "false");
    expect(reject).toHaveAttribute("aria-pressed", "true");
  });

  it("labels the file filters and the per-decision action select", () => {
    renderReview();

    expect(document.querySelector("label[for='diff-review-search']")).toHaveTextContent(
      "Filter files by path",
    );
    expect(screen.getByTestId("diff-review-search")).toHaveAttribute("id", "diff-review-search");
    expect(document.querySelector("label[for='diff-review-filter']")).toHaveTextContent(
      "Show files by decision",
    );
    expect(screen.getByTestId("diff-review-filter")).toHaveAttribute("id", "diff-review-filter");

    const action = screen.getByTestId("diff-review-hunk-decision-hunk-1-action");
    expect(
      document.querySelector("label[for='diff-review-hunk-decision-hunk-1-action']"),
    ).toHaveTextContent("Action ID");
    expect(action).toHaveAttribute(
      "aria-describedby",
      "diff-review-hunk-decision-hunk-1-action-hint",
    );
    expect(
      document.getElementById("diff-review-hunk-decision-hunk-1-action-hint"),
    ).toHaveTextContent("Optional.");
  });

  it("announces the export and draft-recovery notice politely without competing with the gate", () => {
    renderReview();
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:diff-review");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tagName: string) => {
      if (tagName === "a") {
        return { click: vi.fn(), href: "", download: "" } as unknown as HTMLAnchorElement;
      }
      return originalCreateElement(tagName);
    });

    fireEvent.click(screen.getByTestId("diff-review-export"));

    const message = screen.getByTestId("diff-review-message");
    expect(message).toHaveAttribute("role", "status");
    expect(message).toHaveAttribute("aria-live", "polite");
    // Informational, so it keeps its emerald styling and its place; the amber
    // blocked reason is a different element next to the button.
    expect(message).toHaveTextContent("Exported");
    expect(screen.getByTestId("diff-review-submit-gate-reason")).toHaveTextContent(
      "still needs one",
    );
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderReview();

    fireEvent.click(screen.getByTestId("diff-review-hunk-decision-hunk-1-accept"));
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-1-action"), {
      target: { value: "follow-up" },
    });
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-1-notes"), {
      target: { value: "Looks good." },
    });
    fireEvent.click(screen.getByTestId("diff-review-file-file-2"));
    fireEvent.click(screen.getByTestId("diff-review-hunk-decision-hunk-2-reject"));
    fireEvent.change(screen.getByTestId("diff-review-hunk-decision-hunk-2-notes"), {
      target: { value: "Spacing regressed." },
    });
    fireEvent.click(screen.getByTestId("diff-review-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload.review_id).toBe("review-1");
    expect(response.payload.current_file).toBe("file-2");
    expect(response.payload.filter_state).toEqual({ search: "", decision: "all" });
    expect(response.payload.decisions).toEqual([
      expect.objectContaining({
        file_id: "file-1",
        hunk_id: "hunk-1",
        decision: "accept",
        action_id: "follow-up",
      }),
      expect.objectContaining({ file_id: "file-2", hunk_id: "hunk-2", decision: "reject" }),
    ]);
    // action_id stays absent rather than becoming "" — the `|| undefined` shape
    // in buildOrderedDecisions is contract-critical.
    expect(response.payload.decisions[1].action_id).toBeUndefined();
    expect(response.payload.comments).toEqual({
      "file-1::hunk-1": "Looks good.",
      "file-2::hunk-2": "Spacing regressed.",
    });
    expect(response.payload.summary).toMatchObject({
      decision_file_count: 2,
      decision_hunk_count: 2,
      pending_count: 0,
      decision_summary: { accept: 1, reject: 1 },
    });
    expect(response.payload.export_refs).toEqual([]);
  });
});
