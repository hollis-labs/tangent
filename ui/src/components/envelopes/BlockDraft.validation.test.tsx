// Validation-affordance coverage for the block draft response.
//
// BlockDraft's trap is one textarea wearing four labels — "Revision notes",
// "New direction", "Edit notes", "Optional note" — of which exactly two make
// it mandatory, in identical type, with only the Accept wording ever saying
// "optional". Inline edit is the worst pairing: the required-sounding "Edit
// notes" is optional, and it sits directly under "Final block text", which
// has no marker at all and is the field that actually holds Submit down.
//
// Each test names the failure pattern it pins.

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { BlockDraft, type BlockDraftEnvelope, type BlockDraftResponse } from "./BlockDraft";

function envelope(): BlockDraftEnvelope {
  return {
    v: 1,
    id: "draft-1",
    type: "tangent.block-draft",
    title: "Intro block",
    data: {
      block_id: "intro",
      mode: "section",
      label: "Intro",
      content: "This is the candidate draft block.",
      rationale: "Lead with the main point.",
    },
  };
}

function renderDraft(onSubmit = vi.fn<(response: BlockDraftResponse) => void>()) {
  render(<BlockDraft envelope={envelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);
  return onSubmit;
}

describe("BlockDraft validation affordances", () => {
  it("marks the four-mode notes box required exactly in the modes that require it", () => {
    renderDraft();
    const feedback = screen.getByTestId("block-draft-feedback");

    // Accept — the one mode whose label already admitted it was optional.
    expect(document.querySelector("label[for='block-draft-feedback']")).toHaveTextContent(
      "Optional note",
    );
    expect(screen.queryByTestId("block-draft-feedback-required")).not.toBeInTheDocument();
    expect(feedback).toHaveAttribute("aria-required", "false");

    fireEvent.click(screen.getByTestId("block-draft-decision-revise"));
    expect(document.querySelector("label[for='block-draft-feedback']")).toHaveTextContent(
      "Revision notes",
    );
    expect(screen.getByTestId("block-draft-feedback-required")).toBeInTheDocument();
    expect(feedback).toHaveAttribute("aria-required", "true");
    expect(feedback).toHaveAttribute("aria-invalid", "true");

    fireEvent.click(screen.getByTestId("block-draft-decision-redirect"));
    expect(screen.getByTestId("block-draft-feedback-required")).toBeInTheDocument();

    // Inline edit: the label still reads "Edit notes", which sounds every bit
    // as mandatory as "Revision notes" — but the box is not required, and now
    // says so.
    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));
    expect(document.querySelector("label[for='block-draft-feedback']")).toHaveTextContent(
      "Edit notes",
    );
    expect(screen.queryByTestId("block-draft-feedback-required")).not.toBeInTheDocument();
    expect(feedback).toHaveAttribute("aria-required", "false");
    expect(document.getElementById("block-draft-feedback-hint")).toHaveTextContent(
      "Required under Request revision and Different direction",
    );
  });

  it("marks the field that actually blocks an inline edit", () => {
    renderDraft();
    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));

    // "Final block text" carried no marker at all, above a required-sounding
    // optional box.
    expect(screen.getByTestId("block-draft-edited-text-required")).toBeInTheDocument();
    const edited = screen.getByTestId("block-draft-edited-text");
    expect(edited).toHaveAttribute("aria-required", "true");
    expect(document.querySelector("label[for='block-draft-edited-text']")).toHaveTextContent(
      "Final block text",
    );

    fireEvent.change(edited, { target: { value: "   " } });
    expect(edited).toHaveAttribute("aria-invalid", "true");
    expect(edited).toHaveAttribute(
      "aria-describedby",
      "block-draft-edited-text-hint block-draft-edited-text-error",
    );
    expect(screen.getByTestId("block-draft-edited-text-error")).toHaveAttribute("role", "alert");

    fireEvent.change(edited, { target: { value: "The final wording." } });
    expect(edited).toHaveAttribute("aria-describedby", "block-draft-edited-text-hint");
    expect(screen.getByTestId("block-draft-submit")).not.toBeDisabled();
  });

  it("explains the disabled Submit in the words of the decision that caused it", () => {
    renderDraft();

    expect(screen.getByTestId("block-draft-submit")).not.toBeDisabled();

    fireEvent.click(screen.getByTestId("block-draft-decision-revise"));
    const notice = screen.getByTestId("block-draft-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("block-draft-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: a revision request still needs its revision notes.",
    );
    expect(screen.getByTestId("block-draft-submit")).toHaveAttribute(
      "aria-describedby",
      "block-draft-submit-gate",
    );

    fireEvent.click(screen.getByTestId("block-draft-decision-redirect"));
    expect(screen.getByTestId("block-draft-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: a new direction still needs to be described.",
    );

    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));
    fireEvent.change(screen.getByTestId("block-draft-edited-text"), { target: { value: "" } });
    expect(screen.getByTestId("block-draft-submit-gate-reason")).toHaveTextContent(
      "Submit is disabled: an inline edit still needs the final block text.",
    );
  });

  it("takes the operator to the control the gate names", () => {
    renderDraft();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.click(screen.getByTestId("block-draft-decision-revise"));
    fireEvent.click(screen.getByTestId("block-draft-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("block-draft-feedback"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });

    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));
    fireEvent.change(screen.getByTestId("block-draft-edited-text"), { target: { value: "" } });
    fireEvent.click(screen.getByTestId("block-draft-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("block-draft-edited-text"));
  });

  it("announces the selected response instead of showing it in colour only", () => {
    renderDraft();

    const group = screen.getByRole("group");
    expect(group).toHaveAttribute("aria-labelledby", "block-draft-response-label");
    expect(document.getElementById("block-draft-response-label")).toHaveTextContent("Response");

    expect(screen.getByTestId("block-draft-decision-accept")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    fireEvent.click(screen.getByTestId("block-draft-decision-revise"));
    expect(screen.getByTestId("block-draft-decision-accept")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(screen.getByTestId("block-draft-decision-revise")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("leaves the submitted payload shape untouched", () => {
    const acceptSubmit = renderDraft();
    fireEvent.click(screen.getByTestId("block-draft-submit"));
    expect(acceptSubmit.mock.calls[0][0].payload).toEqual({
      decision: "accept",
      block_id: "intro",
      mode: "section",
      feedback: undefined,
      edited_text: undefined,
    });

    cleanup();

    const editSubmit = renderDraft();
    fireEvent.click(screen.getByTestId("block-draft-decision-inline_edit"));
    fireEvent.change(screen.getByTestId("block-draft-edited-text"), {
      target: { value: "  Edited inline block.  " },
    });
    fireEvent.change(screen.getByTestId("block-draft-feedback"), {
      target: { value: "  Tightened the wording.  " },
    });
    fireEvent.click(screen.getByTestId("block-draft-submit"));
    expect(editSubmit.mock.calls[0][0].payload).toEqual({
      decision: "inline_edit",
      block_id: "intro",
      mode: "section",
      feedback: "Tightened the wording.",
      edited_text: "Edited inline block.",
    });
    expect(typeof editSubmit.mock.calls[0][0].completedAt).toBe("string");
  });
});
