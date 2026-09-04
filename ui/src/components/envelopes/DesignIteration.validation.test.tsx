// Validation-affordance coverage for design iteration.
//
// This workflow has no single terminal CTA: a click region inside the sandboxed
// iframe, a button prompt, and each text prompt's own Send all fire `onSubmit`
// independently. So the gate is per prompt and lives beside that prompt's own
// button — a shared footer notice could not have said which prompt it meant.
//
// The audited defect: the only disabled control in the whole component is that
// Send, its label is envelope-supplied and falls back to a raw machine id, and
// nothing adjacent said what the dead button was waiting for.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DesignIteration, type DesignIterationEnvelope } from "./DesignIteration";

function sampleEnvelope(): DesignIterationEnvelope {
  return {
    v: 1,
    id: "design-1",
    type: "tangent.design-iteration",
    title: "Explore a landing page",
    data: {
      variant_id: "variant-a",
      html: "<section><button id='hero'>Hero button</button></section>",
      prompts: [
        { id: "hero", kind: "click-region", label: "Hero button", selector: "#hero" },
        { id: "ship", kind: "button", label: "Ship it" },
        {
          id: "needs-copy",
          kind: "text-input",
          label: "What copy should change?",
          placeholder: "Describe the change",
        },
        // Deliberately unlabelled: the fallback is the raw machine id, which is
        // why requiredness cannot be left to the label text.
        { id: "unlabelled-prompt", kind: "text-input" },
      ],
      prior_variants: [
        {
          envelope_id: "design-old",
          variant_id: "variant-0",
          title: "Previous concept",
          html: "<div>Old</div>",
        },
      ],
    },
  };
}

describe("DesignIteration validation affordances", () => {
  it("gives each text prompt its own adjacent explanation for its own dead Send", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("design-iteration-submit-needs-copy")).toBeDisabled();
    const notice = screen.getByTestId("design-iteration-submit-gate-needs-copy");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("design-iteration-submit-gate-needs-copy-reason")).toHaveTextContent(
      'Send is disabled: "What copy should change?" still needs an answer.',
    );
    expect(screen.getByTestId("design-iteration-submit-needs-copy")).toHaveAttribute(
      "aria-describedby",
      "design-iteration-submit-gate-needs-copy",
    );

    // Each prompt speaks only for itself.
    expect(
      screen.getByTestId("design-iteration-submit-gate-unlabelled-prompt-reason"),
    ).toHaveTextContent('Send is disabled: "unlabelled-prompt" still needs an answer.');

    fireEvent.change(screen.getByTestId("design-iteration-input-needs-copy"), {
      target: { value: "Tighten the hero copy" },
    });
    expect(screen.getByTestId("design-iteration-submit-needs-copy")).not.toBeDisabled();
    expect(
      screen.queryByTestId("design-iteration-submit-gate-needs-copy-reason"),
    ).not.toBeInTheDocument();
    // ...and the other prompt is still blocked.
    expect(screen.getByTestId("design-iteration-submit-unlabelled-prompt")).toBeDisabled();
  });

  it("marks the prompt required rather than relying on an envelope-supplied label", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("design-iteration-required-needs-copy")).toBeInTheDocument();
    // The unlabelled prompt falls back to its machine id, so the marker is the
    // only thing that says it is required at all.
    expect(document.querySelector("label[for='design-input-unlabelled-prompt']")).toHaveTextContent(
      "unlabelled-prompt",
    );
    expect(screen.getByTestId("design-iteration-required-unlabelled-prompt")).toBeInTheDocument();
  });

  it("binds each prompt's requirement and hint to the control", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const input = screen.getByTestId("design-iteration-input-needs-copy");
    expect(input).toHaveAttribute("aria-required", "true");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAttribute("aria-describedby", "design-input-needs-copy-hint");
    expect(document.getElementById("design-input-needs-copy-hint")).toHaveTextContent(
      "Required before Send. Sending answers this prompt on its own and completes the iteration.",
    );

    fireEvent.change(input, { target: { value: "Tighten the hero copy" } });
    expect(input).toHaveAttribute("aria-invalid", "false");
  });

  it("gives the variant strip tab semantics instead of colour alone", () => {
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("design-iteration-tabs")).toHaveAttribute("role", "tablist");
    const current = screen.getByTestId("design-iteration-tab-variant-a");
    const prior = screen.getByTestId("design-iteration-tab-variant-0");
    expect(current).toHaveAttribute("role", "tab");
    expect(current).toHaveAttribute("aria-selected", "true");
    expect(prior).toHaveAttribute("aria-selected", "false");

    fireEvent.click(prior);
    expect(prior).toHaveAttribute("aria-selected", "true");
    expect(current).toHaveAttribute("aria-selected", "false");
    expect(document.getElementById("design-iteration-variant-panel")).toHaveAttribute(
      "aria-labelledby",
      "design-iteration-tab-variant-0",
    );
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = vi.fn();
    render(<DesignIteration envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByTestId("design-iteration-input-needs-copy"), {
      target: { value: "Tighten the hero copy" },
    });
    fireEvent.click(screen.getByTestId("design-iteration-submit-needs-copy"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.v).toBe(1);
    expect(response.envelopeId).toBe("design-1");
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    // No trimming, exactly as before.
    expect(response.payload).toEqual({
      variant_id: "variant-a",
      action_id: "needs-copy",
      action_kind: "text-input",
      value: "Tighten the hero copy",
    });

    // The button path is untouched too.
    fireEvent.click(screen.getByTestId("design-iteration-button-ship"));
    expect(onSubmit.mock.calls[1][0].payload).toEqual({
      variant_id: "variant-a",
      action_id: "ship",
      action_kind: "button",
      value: "Ship it",
    });
  });
});
