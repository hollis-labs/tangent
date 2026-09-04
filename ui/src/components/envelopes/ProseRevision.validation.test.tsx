// Validation-affordance coverage for the prose revision pass.
//
// This file's version of the originating failure is the second disable cause.
// `submitDisabled` was `unresolved > 0 || <a Comment decision with an empty
// comment>`, and the second half had no indicator anywhere: once every
// suggestion was decided the header printed "All suggestions decided" while
// Submit stayed dead, and nothing named the suggestion still owing its
// comment. The reviewer's only clue was a label that quietly swapped between
// "Optional note" and "Required comment" in identical type.
//
// Each test names the failure pattern it pins.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  ProseRevision,
  type ProseRevisionEnvelope,
  type ProseRevisionResponse,
} from "./ProseRevision";

function envelope(): ProseRevisionEnvelope {
  return {
    v: 1,
    id: "rev-1",
    type: "tangent.prose-revision",
    title: "Review the opening",
    data: {
      lens: "review",
      revision_id: "opening-pass",
      block_id: "intro",
      source_text: "The original opening paragraph.",
      suggestions: [
        {
          id: "s1",
          label: "Clarify the claim",
          original_text: "The original opening paragraph.",
          suggested_text: "Open with the main claim before the setup.",
        },
        {
          id: "s2",
          label: "Tighten the example",
          suggested_text: "Cut the second example and keep one concrete case.",
          reason: "The current version feels repetitive.",
        },
      ],
    },
  };
}

function renderRevision(onSubmit = vi.fn<(response: ProseRevisionResponse) => void>()) {
  render(<ProseRevision envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);
  return onSubmit;
}

describe("ProseRevision validation affordances", () => {
  it("names the suggestion that owes a comment when every suggestion is decided", () => {
    // The reported case: nothing is undecided, so the old header said "All
    // suggestions decided" and the dead Submit had no explanation at all.
    renderRevision();

    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-comment"));

    expect(screen.getByTestId("prose-revision-submit")).toBeDisabled();
    expect(screen.getByTestId("prose-revision-counts")).toHaveTextContent("1 awaiting a comment");
    expect(screen.getByTestId("prose-revision-counts")).not.toHaveTextContent(
      "All suggestions decided",
    );

    const notice = screen.getByTestId("prose-revision-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("prose-revision-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: "Tighten the example" is marked Comment and still needs the comment itself.',
    );
    expect(screen.getByTestId("prose-revision-submit")).toHaveAttribute(
      "aria-describedby",
      "prose-revision-submit-gate",
    );

    fireEvent.change(screen.getByTestId("prose-revision-comment-s2"), {
      target: { value: "Keep one case and make it concrete." },
    });
    expect(screen.getByTestId("prose-revision-submit")).not.toBeDisabled();
    expect(screen.getByTestId("prose-revision-counts")).toHaveTextContent(
      "All suggestions decided",
    );
  });

  it("makes the comment box visibly and audibly required only under a Comment decision", () => {
    renderRevision();

    expect(screen.queryByTestId("prose-revision-comment-required-s2")).not.toBeInTheDocument();
    expect(screen.getByTestId("prose-revision-comment-s2")).toHaveAttribute(
      "aria-required",
      "false",
    );

    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-comment"));

    expect(screen.getByTestId("prose-revision-comment-required-s2")).toBeInTheDocument();
    const control = screen.getByTestId("prose-revision-comment-s2");
    expect(control).toHaveAttribute("aria-required", "true");
    expect(control).toHaveAttribute("aria-invalid", "true");
    expect(control).toHaveAttribute(
      "aria-describedby",
      "prose-revision-comment-hint-s2 prose-revision-comment-error-s2",
    );
    expect(screen.getByTestId("prose-revision-comment-error-s2")).toHaveAttribute("role", "alert");

    fireEvent.change(control, { target: { value: "Cut the repetition." } });
    expect(control).toHaveAttribute("aria-describedby", "prose-revision-comment-hint-s2");
    expect(screen.queryByTestId("prose-revision-comment-error-s2")).not.toBeInTheDocument();
  });

  it("keeps the agent's reason and the reviewer's comment apart", () => {
    renderRevision();

    // Both words belong to the workflow and neither is renamed; the hint is
    // what says which is whose.
    expect(screen.getByTestId("prose-revision-reason-s2")).toHaveTextContent(
      "The current version feels repetitive.",
    );
    expect(document.getElementById("prose-revision-comment-hint-s2")).toHaveTextContent(
      "Your reply to this suggestion, separate from the agent's own reason for proposing it.",
    );

    // And the overall note, which is typographically identical to a comment
    // that can be mandatory, says outright that it never stands in for one.
    expect(screen.getByTestId("prose-revision-general-comment-optional")).toHaveTextContent(
      "optional",
    );
    expect(screen.getByTestId("prose-revision-general-comment")).toHaveAttribute(
      "aria-describedby",
      "prose-revision-general-comment-hint",
    );
    expect(document.getElementById("prose-revision-general-comment-hint")).toHaveTextContent(
      "never stands in for a comment a single suggestion is waiting on",
    );
    expect(screen.getByTestId("prose-revision-general-comment")).not.toHaveAttribute(
      "aria-required",
    );
  });

  it("names the undecided suggestions in order and counts the rest", () => {
    renderRevision();

    expect(screen.getByTestId("prose-revision-submit-gate-reason")).toHaveTextContent(
      'Submit is disabled: "Clarify the claim" still needs a decision.',
    );
    expect(screen.getByTestId("prose-revision-submit-gate-more")).toHaveTextContent(
      "+1 more to resolve",
    );
    expect(screen.getByTestId("prose-revision-counts")).toHaveTextContent("2 undecided");
  });

  it("announces the selected decision and groups the pills per suggestion", () => {
    renderRevision();

    const groups = screen.getAllByRole("group");
    expect(groups[0]).toHaveAttribute(
      "aria-labelledby",
      "prose-revision-suggestion-s1-label prose-revision-decision-label-s1",
    );
    expect(screen.getByTestId("prose-revision-decision-s1-accept")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    expect(screen.getByTestId("prose-revision-decision-s1-accept")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.queryByTestId("prose-revision-decision-required-s1")).not.toBeInTheDocument();
  });

  it("takes the reviewer to the comment box the gate is naming", () => {
    renderRevision();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-comment"));
    fireEvent.click(screen.getByTestId("prose-revision-submit-gate-go"));

    expect(document.activeElement).toBe(screen.getByTestId("prose-revision-comment-s2"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = renderRevision();

    fireEvent.click(screen.getByTestId("prose-revision-decision-s1-accept"));
    fireEvent.click(screen.getByTestId("prose-revision-decision-s2-comment"));
    fireEvent.change(screen.getByTestId("prose-revision-comment-s2"), {
      target: { value: "Keep the example, but rewrite it more concretely." },
    });
    fireEvent.change(screen.getByTestId("prose-revision-general-comment"), {
      target: { value: "Prefer the structural fix over new flourishes." },
    });
    fireEvent.click(screen.getByTestId("prose-revision-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response).toMatchObject({
      v: 1,
      envelopeId: "rev-1",
      kind: "data",
      status: "submitted",
    });
    expect(response.payload).toEqual({
      lens: "review",
      revision_id: "opening-pass",
      block_id: "intro",
      outcomes: [
        { suggestion_id: "s1", decision: "accept", comment: undefined },
        {
          suggestion_id: "s2",
          decision: "comment",
          comment: "Keep the example, but rewrite it more concretely.",
        },
      ],
      general_comment: "Prefer the structural fix over new flourishes.",
    });
    expect(typeof response.completedAt).toBe("string");
  });
});
