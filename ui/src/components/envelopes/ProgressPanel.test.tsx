import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  ProgressPanel,
  type ProgressPanelEnvelope,
  type ProgressPanelResponse,
} from "./ProgressPanel";

describe("<ProgressPanel>", () => {
  it("renders seeded progress state and submits canonical updates", () => {
    const onSubmit = vi.fn<(response: ProgressPanelResponse) => void>();
    const envelope: ProgressPanelEnvelope = {
      v: 1,
      id: "progress-env-1",
      type: "tangent.progress-panel",
      title: "Build progress",
      data: {
        panel_id: "panel-1",
        items: [
          {
            item_id: "item-1",
            label: "Scan repo",
            status: "running",
            detail: "Indexing files",
          },
          {
            item_id: "item-2",
            label: "Write summary",
            status: "queued",
          },
        ],
        updates: [
          {
            update_id: "upd-001",
            kind: "status",
            item_id: "item-1",
            status: "running",
            summary: "Started scan",
          },
        ],
        checkpoints: [
          {
            checkpoint_id: "cp-001",
            label: "Repo indexed",
            summary: "Indexed 24 files",
          },
        ],
        summary: {
          current_status: "running",
          headline: "1 running",
          detail: "Scanning is underway",
          last_update_id: "upd-001",
          last_checkpoint_id: "cp-001",
        },
      },
    };

    render(<ProgressPanel envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} />);

    expect(screen.getByTestId("progress-panel-item-item-1")).toHaveTextContent("Scan repo");
    expect(screen.getByText("1 running")).toBeInTheDocument();
    expect(screen.getByTestId("progress-panel-update-count")).toHaveTextContent("1");

    fireEvent.change(screen.getByTestId("progress-panel-item-select"), {
      target: { value: "item-2" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-status-select"), {
      target: { value: "running" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-summary-input"), {
      target: { value: "Picked up summary drafting." },
    });
    fireEvent.change(screen.getByTestId("progress-panel-checkpoint-input"), {
      target: { value: "Summary started" },
    });
    fireEvent.click(screen.getByTestId("progress-panel-submit"));

    expect(onSubmit).toHaveBeenCalledWith({
      v: 1,
      envelopeId: "progress-env-1",
      kind: "data",
      status: "submitted",
      payload: {
        panel_id: "panel-1",
        item_id: "item-2",
        status: "running",
        summary: "Picked up summary drafting.",
        checkpoint_label: "Summary started",
      },
    });
  });

  it("blocks submit when no selectable item exists", () => {
    const onSubmit = vi.fn();
    render(
      <ProgressPanel
        envelope={{
          v: 1,
          id: "progress-env-2",
          type: "tangent.progress-panel",
          data: {
            panel_id: "panel-empty",
            items: [],
          },
        }}
        onSubmit={onSubmit}
        onCancel={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId("progress-panel-submit"));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByTestId("progress-panel-submit-error")).toHaveTextContent(
      "Select a progress item before submitting an update.",
    );
  });
});
