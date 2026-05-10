import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getProgressPanelDraftStorageKey,
  PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS,
} from "@/lib/progress-panel-storage";
import {
  ProgressPanel,
  type ProgressPanelEnvelope,
  type ProgressPanelResponse,
} from "./ProgressPanel";

describe("<ProgressPanel>", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

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
          last_checkpoint_label: "Repo indexed",
        },
      },
    };

    render(
      <ProgressPanel envelope={envelope} onSubmit={onSubmit} onCancel={() => {}} roomID="room-1" />,
    );

    expect(screen.getByTestId("progress-panel-item-item-1")).toHaveTextContent("Scan repo");
    expect(screen.getByText("1 running")).toBeInTheDocument();
    expect(screen.getByTestId("progress-panel-update-count")).toHaveTextContent("1");
    expect(screen.getByTestId("progress-panel-export-payload")).toHaveTextContent(
      '"panel_id": "panel-1"',
    );
    fireEvent.click(screen.getByTestId("progress-panel-tab-logs"));
    expect(screen.getByTestId("progress-panel-log-detail")).toHaveTextContent("upd-001");

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

  it("persists timeline view filters locally and restores them on reopen", () => {
    vi.useFakeTimers();
    const storageKey = getProgressPanelDraftStorageKey("room-9", "panel-9");
    const envelope: ProgressPanelEnvelope = {
      v: 1,
      id: "progress-env-9",
      type: "tangent.progress-panel",
      data: {
        panel_id: "panel-9",
        items: [
          { item_id: "item-1", label: "Scan repo", status: "running" },
          { item_id: "item-2", label: "Write summary", status: "queued" },
        ],
        updates: [
          {
            update_id: "upd-001",
            kind: "status",
            item_id: "item-1",
            status: "running",
            summary: "Started scan",
          },
          {
            update_id: "upd-002",
            kind: "checkpoint",
            item_id: "item-2",
            checkpoint_id: "cp-001",
            checkpoint_label: "Summary started",
            summary: "Drafting summary",
          },
        ],
        checkpoints: [{ checkpoint_id: "cp-001", label: "Summary started" }],
      },
    };

    const { unmount } = render(
      <ProgressPanel envelope={envelope} onSubmit={() => {}} onCancel={() => {}} roomID="room-9" />,
    );
    fireEvent.click(screen.getByTestId("progress-panel-tab-logs"));
    fireEvent.change(screen.getByTestId("progress-panel-filter-item"), {
      target: { value: "item-2" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-filter-kind"), {
      target: { value: "checkpoint" },
    });
    fireEvent.click(screen.getByTestId("progress-panel-log-upd-002"));
    fireEvent.change(screen.getByTestId("progress-panel-summary-input"), {
      target: { value: "Unsaved note" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-checkpoint-input"), {
      target: { value: "Unsaved checkpoint" },
    });

    act(() => {
      vi.advanceTimersByTime(PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS + 50);
    });
    expect(window.localStorage.getItem(storageKey)).toContain('"activeTab":"logs"');
    unmount();

    render(
      <ProgressPanel envelope={envelope} onSubmit={() => {}} onCancel={() => {}} roomID="room-9" />,
    );
    expect(screen.getByTestId("progress-panel-message")).toHaveTextContent(
      "Recovered progress-panel view state from this browser.",
    );
    expect(screen.getByTestId("progress-panel-logs")).toBeInTheDocument();
    expect(screen.getByTestId("progress-panel-filter-item")).toHaveValue("item-2");
    expect(screen.getByTestId("progress-panel-filter-kind")).toHaveValue("checkpoint");
    expect(screen.getByTestId("progress-panel-log-detail")).toHaveTextContent("upd-002");
    expect(screen.getByTestId("progress-panel-summary-input")).toHaveValue("Unsaved note");
    expect(screen.getByTestId("progress-panel-checkpoint-input")).toHaveValue("Unsaved checkpoint");
  });

  it("offers explicit controls and clears recovered draft state on submit", () => {
    vi.useFakeTimers();
    const onSubmit = vi.fn<(response: ProgressPanelResponse) => void>();
    const storageKey = getProgressPanelDraftStorageKey("room-12", "panel-12");
    const envelope: ProgressPanelEnvelope = {
      v: 1,
      id: "progress-env-12",
      type: "tangent.progress-panel",
      data: {
        panel_id: "panel-12",
        items: [{ item_id: "item-1", label: "Scan repo", status: "running" }],
      },
    };

    render(
      <ProgressPanel
        envelope={envelope}
        onSubmit={onSubmit}
        onCancel={() => {}}
        roomID="room-12"
      />,
    );
    fireEvent.click(screen.getByTestId("progress-panel-control-pause"));
    expect(screen.getByTestId("progress-panel-status-select")).toHaveValue("paused");
    expect(screen.getByTestId("progress-panel-summary-input")).toHaveValue("Paused scan repo.");

    act(() => {
      vi.advanceTimersByTime(PROGRESS_PANEL_AUTOSAVE_DEBOUNCE_MS + 50);
    });
    expect(window.localStorage.getItem(storageKey)).toContain('"status":"paused"');

    fireEvent.click(screen.getByTestId("progress-panel-submit"));
    expect(onSubmit).toHaveBeenCalledWith({
      v: 1,
      envelopeId: "progress-env-12",
      kind: "data",
      status: "submitted",
      payload: {
        panel_id: "panel-12",
        item_id: "item-1",
        status: "paused",
        summary: "Paused scan repo.",
        checkpoint_label: undefined,
      },
    });
    expect(window.localStorage.getItem(storageKey)).toBeNull();
  });

  it("copies an export snapshot from the room-backed summary surface", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    render(
      <ProgressPanel
        envelope={{
          v: 1,
          id: "progress-env-export",
          type: "tangent.progress-panel",
          data: {
            panel_id: "panel-export",
            items: [{ item_id: "item-1", label: "Scan repo", status: "completed" }],
            summary: {
              current_status: "completed",
              headline: "All items completed",
              last_checkpoint_label: "Repo indexed",
              completion_result: "completed",
            },
          },
        }}
        onSubmit={() => {}}
        onCancel={() => {}}
      />,
    );

    await act(async () => {
      fireEvent.click(screen.getByTestId("progress-panel-export-copy"));
    });
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText.mock.calls[0][0]).toContain('"completion_result": "completed"');
  });
});
