import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
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

  it("submits completed state on the final step", () => {
    const onSubmit = vi.fn<(response: WizardResponse) => void>();
    const removeItem = vi.spyOn(window.localStorage.__proto__, "removeItem");
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-2",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-2",
        title: "Finalize wizard",
        current_step_id: "step-final",
        steps: [{ step_id: "step-final", title: "Final", kind: "review" }],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-2" />);
    fireEvent.click(screen.getByText("Complete Wizard"));

    const response = onSubmit.mock.calls[0][0];
    expect(response.status).toBe("submitted");
    expect(response.payload.summary.status).toBe("completed");
    expect(response.payload.progress[0]?.status).toBe("completed");
    expect(removeItem).toHaveBeenCalledWith("tangent:wizard-draft:v1:room-2:wizard-2");
    removeItem.mockRestore();
  });

  it("navigates forward with a partial update", () => {
    const onSubmit = vi.fn<(response: WizardResponse) => void>();
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-3",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-3",
        current_step_id: "step-1",
        steps: [
          { step_id: "step-1", title: "Step 1" },
          { step_id: "step-2", title: "Step 2" },
        ],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-3" />);
    fireEvent.click(screen.getByText("Next Step"));

    const response = onSubmit.mock.calls[0][0];
    expect(response.status).toBe("partial");
    expect(response.payload.current_step_id).toBe("step-2");
  });

  it("captures attachment and action outputs", () => {
    const onSubmit = vi.fn<(response: WizardResponse) => void>();
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-4",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-4",
        current_step_id: "step-artifacts",
        steps: [
          {
            step_id: "step-artifacts",
            title: "Artifacts",
            fields: {
              fields: [{ field_id: "attachments", label: "Attachments", kind: "attachments" }],
              actions: [{ action_id: "queue-followup", label: "Queue Follow-up" }],
            },
          },
        ],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-4" />);
    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "brief|artifact://brief-1" },
    });
    fireEvent.click(screen.getByText("Queue Follow-up"));

    const response = onSubmit.mock.calls[0][0];
    const attachments = (response.payload.progress[0]?.response?.attachments ?? []) as Array<{
      uri?: string;
    }>;
    const actionOutput = (response.payload.progress[0]?.response?.action_output ?? {}) as {
      action_id?: string;
    };
    expect(attachments[0]?.uri).toBe("artifact://brief-1");
    expect(actionOutput.action_id).toBe("queue-followup");
  });

  it("refreshes updated_at when patching existing progress", () => {
    const onSubmit = vi.fn<(response: WizardResponse) => void>();
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-5",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-5",
        current_step_id: "step-1",
        progress: [
          {
            step_id: "step-1",
            status: "in_progress",
            updated_at: "2026-05-09T00:00:00.000Z",
          },
        ],
        steps: [{ step_id: "step-1", title: "Step 1" }],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-5" />);
    fireEvent.click(screen.getByText("Save Progress"));

    const response = onSubmit.mock.calls[0][0];
    expect(response.payload.progress[0]?.updated_at).not.toBe("2026-05-09T00:00:00.000Z");
  });

  it("routes every prose surface through the shared markdown renderer", () => {
    const envelope: WizardEnvelope = {
      v: 1,
      id: "wizard-md",
      type: "tangent.wizard",
      data: {
        wizard_id: "wizard-md",
        description: proseProbe("wz-description"),
        current_step_id: "s1",
        steps: [
          {
            step_id: "s1",
            title: "First",
            description: proseProbe("wz-step"),
            branches: [
              {
                branch_id: "b1",
                label: "Branch one",
                description: proseProbe("wz-branch"),
                target_step_id: "s2",
              },
            ],
          },
          { step_id: "s2", title: "Second" },
        ],
      },
    };

    render(<Wizard envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expectProseRendered("wz-description");
    expectProseRendered("wz-step");
    expectProseRendered("wz-branch");
  });
});
