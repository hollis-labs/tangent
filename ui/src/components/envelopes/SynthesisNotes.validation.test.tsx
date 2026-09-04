// Validation-affordance coverage for synthesis notes.
//
// This workflow has NO terminal gate, by design: it is a read-only
// acknowledgement with no editable controls, so "Continue" is never blocked and
// there is nothing for a gate to name. Adding one would invent validation the
// workflow does not have. What it did owe was a live region — it swaps whole
// content branches (hidden / outline / skipped / empty) inside plain divs — and
// real headings for its pseudo-heading paragraphs.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SynthesisNotes, type SynthesisNotesEnvelope } from "./SynthesisNotes";

function envelope(data: SynthesisNotesEnvelope["data"]): SynthesisNotesEnvelope {
  return {
    v: 1,
    id: "synthesis-1",
    type: "tangent.synthesis-notes",
    title: "Synthesis",
    data,
  };
}

describe("SynthesisNotes validation affordances", () => {
  it("has no submit gate, because it has nothing to gate", () => {
    render(
      <SynthesisNotes
        envelope={envelope({ visibility: "hidden" })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const cta = screen.getByTestId("synthesis-notes-submit");
    expect(cta).not.toBeDisabled();
    expect(cta).not.toHaveAttribute("aria-describedby");
    expect(screen.queryByTestId("synthesis-notes-submit-gate")).not.toBeInTheDocument();
    // And no editable control that could carry a requirement.
    expect(document.querySelectorAll("input, textarea, select")).toHaveLength(0);
  });

  it("announces the branch it is currently showing", () => {
    const { rerender } = render(
      <SynthesisNotes
        envelope={envelope({ visibility: "hidden" })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const content = screen.getByTestId("synthesis-notes-content");
    expect(content).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("synthesis-notes-hidden")).toBeInTheDocument();

    rerender(
      <SynthesisNotes
        envelope={envelope({
          visibility: "visible",
          summary: "Three themes emerged.",
          outline_state: "present",
          outline: { title: "Draft outline", items: [{ label: "Opening" }] },
        })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    // The swap happens inside the same live region, so it is announced.
    expect(screen.getByTestId("synthesis-notes-content")).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("synthesis-notes-outline")).toBeInTheDocument();
  });

  it("uses real headings rather than styled paragraphs", () => {
    render(
      <SynthesisNotes
        envelope={envelope({
          visibility: "visible",
          summary: "Three themes emerged.",
          outline_state: "present",
          outline: { title: "Draft outline", items: [{ label: "Opening" }] },
        })}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByRole("heading", { name: "Summary" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Outline preview" })).toBeInTheDocument();
  });

  it("leaves the acknowledgement payload untouched", () => {
    const onSubmit = vi.fn();
    render(
      <SynthesisNotes
        envelope={envelope({ visibility: "visible", outline_state: "skipped" })}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId("synthesis-notes-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.v).toBe(1);
    expect(response.envelopeId).toBe("synthesis-1");
    expect(response.kind).toBe("ack");
    expect(response.status).toBe("submitted");
    expect(typeof response.completedAt).toBe("string");
  });
});
