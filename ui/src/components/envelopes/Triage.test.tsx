// Vitest coverage for the <Triage> envelope component. Validates the
// v0.1 acceptance behaviors:
//
//   - Renders all items (mixed string + object shape).
//   - Action buttons assign decisions and update the on-row badge.
//   - Submit is disabled until every item has a decision.
//   - Submit emits a structurally-correct TriageResponse.
//   - Cancel calls onCancel without invoking onSubmit.
//   - Object items without an explicit `id` get a deterministic
//     `item-<index>` itemId and pick a sensible label.

import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Triage, type TriageEnvelope } from "./Triage";

describe("<Triage>", () => {
  it("renders every item from the envelope", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-1",
      type: "tangent.triage",
      data: {
        items: ["alpha", "beta", { id: "gamma-id", title: "gamma" }],
      },
    };

    render(<Triage envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
    expect(screen.getByText("gamma")).toBeInTheDocument();
  });

  it("clicking an action button records the decision and shows it on the row", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-2",
      type: "tangent.triage",
      data: { items: ["alpha"] },
    };

    render(<Triage envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));

    const badge = screen.getByTestId("triage-item-item-0-decision");
    expect(badge).toHaveTextContent("accept");
    expect(screen.getByTestId("triage-action-item-0-accept")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("disables Submit until all items are decided", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-3",
      type: "tangent.triage",
      data: { items: ["one", "two"] },
    };

    render(<Triage envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const submit = screen.getByTestId("triage-submit");
    expect(submit).toBeDisabled();

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));
    expect(submit).toBeDisabled();

    fireEvent.click(screen.getByTestId("triage-action-item-1-backlog"));
    expect(submit).not.toBeDisabled();
  });

  it("Submit emits a TriageResponse with one decision per item", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-4",
      type: "tangent.triage",
      data: {
        items: ["one", { id: "two-id", title: "two" }],
      },
    };
    const onSubmit = vi.fn();

    render(<Triage envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));
    fireEvent.click(screen.getByTestId("triage-action-two-id-delete"));
    fireEvent.click(screen.getByTestId("triage-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const arg = onSubmit.mock.calls[0][0];
    expect(arg).toMatchObject({
      v: 1,
      envelopeId: "env-4",
      kind: "data",
      status: "submitted",
      payload: {
        decisions: [
          { itemId: "item-0", action: "accept" },
          { itemId: "two-id", action: "delete" },
        ],
      },
    });
    expect(typeof arg.completedAt).toBe("string");
  });

  it("Cancel calls onCancel and not onSubmit", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-5",
      type: "tangent.triage",
      data: { items: ["only"] },
    };
    const onSubmit = vi.fn();
    const onCancel = vi.fn();

    render(<Triage envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} />);

    fireEvent.click(screen.getByTestId("triage-cancel"));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("changes a decision when a different action is clicked", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-6",
      type: "tangent.triage",
      data: { items: ["only"] },
    };
    const onSubmit = vi.fn();

    render(<Triage envelope={envelope} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("triage-action-item-0-accept"));
    fireEvent.click(screen.getByTestId("triage-action-item-0-backlog"));
    fireEvent.click(screen.getByTestId("triage-submit"));

    const arg = onSubmit.mock.calls[0][0];
    expect(arg.payload.decisions).toEqual([{ itemId: "item-0", action: "backlog" }]);
  });

  it("renders prompt and item count in the header", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-7",
      type: "tangent.triage",
      data: {
        prompt: "Please decide each item.",
        items: ["a", "b"],
      },
    };

    render(<Triage envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByText("Please decide each item.")).toBeInTheDocument();
    const root = screen.getByTestId("triage-root");
    expect(within(root).getByText(/2 items/)).toBeInTheDocument();
  });

  it("handles object items with extra context by rendering a JSON detail block", () => {
    const envelope: TriageEnvelope = {
      v: 1,
      id: "env-8",
      type: "tangent.triage",
      data: {
        items: [{ id: "x1", title: "Has context", note: "extra detail" }],
      },
    };

    render(<Triage envelope={envelope} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByText(/extra detail/)).toBeInTheDocument();
    expect(screen.getByText("Has context")).toBeInTheDocument();
  });
});
