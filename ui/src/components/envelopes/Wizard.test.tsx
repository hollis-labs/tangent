import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Wizard, type WizardEnvelope, type WizardResponse } from "./Wizard";

describe("Wizard", () => {
  it("submits partial progress with updated step state", () => {
    const onSubmit = vi.fn<(response: WizardResponse) => void>();
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-1",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-1",
        title: "Release wizard",
        current_step_id: "step-scope",
        steps: [
          {
            step_id: "step-scope",
            title: "Scope",
            kind: "form",
            fields: {
              fields: [{ field_id: "scope", label: "Scope", kind: "textarea" }],
            },
            branches: [{ branch_id: "review", label: "Review", target_step_id: "step-review" }],
          },
          { step_id: "step-review", title: "Review", kind: "review" },
        ],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-1" />);

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "wizard envelope host" },
    });
    fireEvent.click(screen.getByLabelText("Review"));
    fireEvent.click(screen.getByText("Save Progress"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.status).toBe("partial");
    expect(response.payload.progress[0]?.response?.scope).toBe("wizard envelope host");
    expect(response.payload.branch_selections[0]?.option_id).toBe("review");
  });
});
