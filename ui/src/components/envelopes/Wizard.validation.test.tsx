// Validation-affordance coverage for the wizard.
//
// The failure this file pins is the largest one in the room workflows: the
// wizard's field type declared `required`, and nothing anywhere read it. A
// required field rendered byte-identically to an optional one, and the terminal
// CTA marked the step "completed" whether or not anything had been filled in —
// a requirement with no affordance and no enforcement. The tests below cover
// the affordance, the enforcement, and the fence around it: only the terminal
// CTA is gated, and only on the current step's fields.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Wizard, type WizardEnvelope, type WizardResponse } from "./Wizard";

const envelope: WizardEnvelope = {
  v: 1,
  id: "wizard-1",
  type: "tangent.wizard",
  title: "Release wizard",
  data: {
    wizard_id: "release",
    title: "Release wizard",
    current_step_id: "step-scope",
    steps: [
      {
        step_id: "step-scope",
        title: "Scope",
        kind: "form",
        fields: {
          fields: [
            {
              field_id: "summary",
              label: "Release summary",
              kind: "textarea",
              required: true,
            },
            { field_id: "owner", label: "Owner", kind: "text" },
          ],
        },
        branches: [
          {
            branch_id: "review",
            label: "Review",
            description: "Send it to the reviewers before shipping.",
            target_step_id: "step-review",
          },
        ],
      },
      { step_id: "step-review", title: "Review", kind: "review" },
    ],
  },
};

function renderWizard(onSubmit = vi.fn<(response: WizardResponse) => void>()) {
  render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} roomID="room-w" />);
  return onSubmit;
}

describe("Wizard validation affordances", () => {
  it("marks a declared-required field required and leaves optional ones alone", () => {
    renderWizard();

    // Before: these two fields were indistinguishable on screen and in aria.
    expect(screen.getByTestId("wizard-field-summary-required")).toBeInTheDocument();
    expect(screen.queryByTestId("wizard-field-owner-required")).not.toBeInTheDocument();

    const required = screen.getByTestId("wizard-field-summary");
    expect(required).toHaveAttribute("aria-required", "true");
    expect(required).toHaveAttribute("aria-invalid", "true");
    expect(required).toHaveAttribute(
      "aria-describedby",
      "wizard-field-summary-hint wizard-field-summary-error",
    );
    expect(screen.getByTestId("wizard-field-summary-error")).toHaveAttribute("role", "alert");
    expect(document.querySelector("label[for='wizard-field-summary']")).toHaveTextContent(
      "Release summary",
    );

    expect(screen.getByTestId("wizard-field-owner")).toHaveAttribute("aria-required", "false");
  });

  it("refuses the terminal CTA while a required field is empty, and says why", () => {
    const onSubmit = renderWizard();

    const submit = screen.getByTestId("wizard-submit");
    expect(submit).toHaveTextContent("Submit Step");
    expect(submit).toHaveAttribute("aria-describedby", "wizard-submit-gate");
    const notice = screen.getByTestId("wizard-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("wizard-submit-gate-reason")).toHaveTextContent(
      'Cannot submit step yet: "Release summary" is required before this step can be submitted.',
    );

    // The CTA stays live and validates on click — it was never disabled, and
    // making it inert would have been a second behaviour change.
    expect(submit).not.toBeDisabled();
    fireEvent.click(submit);
    expect(onSubmit).not.toHaveBeenCalled();
    // ...and the step is not silently marked completed, which is what used to
    // happen no matter what was left blank.
    expect(screen.getByTestId("wizard-step-step-scope")).toHaveTextContent("pending");
  });

  it("focuses and scrolls to the first missing field on an attempted submit", () => {
    renderWizard();
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;

    fireEvent.click(screen.getByTestId("wizard-submit"));

    expect(document.activeElement).toBe(screen.getByTestId("wizard-field-summary"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });

    // The notice's own affordance goes to the same control.
    fireEvent.click(screen.getByTestId("wizard-submit-gate-go"));
    expect(document.activeElement).toBe(screen.getByTestId("wizard-field-summary"));
  });

  it("gates only the terminal CTA — navigation and Save Progress stay open", () => {
    // A wizard has to remain navigable while it is incomplete, so the new
    // enforcement deliberately stops at Submit Step / Complete Wizard.
    const onSubmit = renderWizard();

    fireEvent.click(screen.getByText("Save Progress"));
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0].status).toBe("partial");

    fireEvent.click(screen.getByText("Next Step"));
    expect(onSubmit).toHaveBeenCalledTimes(2);
    expect(onSubmit.mock.calls[1][0].status).toBe("partial");

    fireEvent.click(screen.getByTestId("wizard-step-step-scope"));
    expect(onSubmit).toHaveBeenCalledTimes(3);
    expect(onSubmit.mock.calls[2][0].status).toBe("partial");

    // And a step that declares no required fields is not gated at all.
    fireEvent.click(screen.getByTestId("wizard-step-step-review"));
    expect(screen.queryByTestId("wizard-submit-gate-reason")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("wizard-submit"));
    expect(onSubmit.mock.calls.at(-1)?.[0].status).toBe("submitted");
  });

  it("announces a branch's description instead of replacing it with an aria-label", () => {
    renderWizard();

    // `aria-label={branch.label}` used to override the visible text outright,
    // so the description — the only thing saying where the branch leads — was
    // never announced.
    const radio = screen.getByLabelText("Review");
    expect(radio).not.toHaveAttribute("aria-label");
    const describedBy = radio.getAttribute("aria-describedby") ?? "";
    expect(document.getElementById(describedBy)).toHaveTextContent(
      "Send it to the reviewers before shipping.",
    );
    // The radios are a named group now, not a bare `<div>` heading.
    expect(screen.getByRole("group", { name: "Choose the next branch" })).toBeInTheDocument();
  });

  it("moves focus to the new step and marks it current", () => {
    renderWizard();

    expect(screen.getByTestId("wizard-step-step-scope")).toHaveAttribute("aria-current", "step");
    expect(screen.getByTestId("wizard-step-step-review")).not.toHaveAttribute("aria-current");

    fireEvent.click(screen.getByTestId("wizard-step-step-review"));

    // Focus used to stay on the footer/stepper button while the entire content
    // region was replaced underneath it.
    const heading = screen.getByRole("heading", { name: "Review" });
    expect(heading).toHaveAttribute("id", "wizard-step-heading");
    expect(document.activeElement).toBe(heading);
    expect(screen.getByTestId("wizard-step-step-review")).toHaveAttribute("aria-current", "step");
  });

  it("keeps a polite live region for the step status message", () => {
    renderWizard();

    // Rendered before there is anything to say, so the region is not created
    // and populated in the same tick.
    const region = screen.getByTestId("wizard-message");
    expect(region).toHaveAttribute("role", "status");
    expect(region).toHaveAttribute("aria-live", "polite");
    expect(region).toHaveTextContent("");

    fireEvent.click(screen.getByText("Next Step"));
    expect(screen.getByTestId("wizard-message")).toHaveTextContent("Moved to the next step.");
  });

  it("submits the unchanged payload once the requirement is met", () => {
    const onSubmit = renderWizard();

    fireEvent.change(screen.getByTestId("wizard-field-summary"), {
      target: { value: "Ship the envelope host." },
    });
    expect(screen.queryByTestId("wizard-submit-gate-reason")).not.toBeInTheDocument();
    expect(screen.getByTestId("wizard-submit")).not.toHaveAttribute("aria-describedby");
    expect(screen.getByTestId("wizard-field-summary")).toHaveAttribute("aria-invalid", "false");

    fireEvent.click(screen.getByTestId("wizard-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.v).toBe(1);
    expect(response.envelopeId).toBe("wizard-1");
    expect(response.kind).toBe("data");
    expect(response.status).toBe("partial");
    expect(Object.keys(response.payload).sort()).toEqual([
      "branch_selections",
      "current_step_id",
      "description",
      "progress",
      "steps",
      "summary",
      "title",
      "updated_at",
      "wizard_id",
    ]);
    expect(response.payload.wizard_id).toBe("release");
    expect(response.payload.current_step_id).toBe("step-review");
    expect(response.payload.progress).toEqual([
      expect.objectContaining({
        step_id: "step-scope",
        status: "completed",
        response: { summary: "Ship the envelope host." },
      }),
    ]);
    expect(response.payload.branch_selections).toEqual([]);
    expect(response.payload.summary).toEqual(
      expect.objectContaining({
        status: "in_progress",
        completed_step_count: 1,
        total_step_count: 2,
        current_step_id: "step-review",
      }),
    );
  });
});
