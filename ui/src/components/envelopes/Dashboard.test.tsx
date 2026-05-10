import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Dashboard, type DashboardEnvelope } from "./Dashboard";

describe("<Dashboard>", () => {
  it("renders room-backed tiles and submits refresh or update actions", () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    const envelope: DashboardEnvelope = {
      v: 1,
      id: "dashboard-1",
      type: "tangent.dashboard",
      title: "Ops dashboard",
      context: "Track active rooms and workflows.",
      data: {
        dashboard_id: "dashboard-1",
        title: "Ops dashboard",
        tiles: [
          {
            tile_id: "tile-open",
            kind: "room_count",
            title: "Open rooms",
            value: "4",
            status: "healthy",
            summary: "Rooms needing review",
          },
          {
            tile_id: "tile-progress",
            kind: "workflow_summary",
            title: "Progress panels",
            value: "2",
            status: "attention",
            workflow: "tangent.progress-panel",
          },
        ],
        layout: [
          { tile_id: "tile-open", x: 0, y: 0, w: 2, h: 1 },
          { tile_id: "tile-progress", x: 2, y: 0, w: 2, h: 1 },
        ],
        saved_layouts: [{ layout_id: "layout-default", name: "Default", is_default: true }],
        active_layout_id: "layout-default",
        query_state: {
          scope: "active",
          search: "progress",
          filters: [{ filter_id: "status", operator: "in", values: ["running"] }],
          sort: [{ field: "updated_at", direction: "desc" }],
        },
        summary: {
          headline: "2 workflows need attention",
          detail: "Refresh after operator review.",
          tile_count: 2,
          active_room_count: 4,
          accepted_snapshot_id: "snap-001",
        },
        snapshot_history: [
          {
            snapshot_id: "snap-001",
            action: "refresh",
            note: "Baseline refresh",
            created_at: "2026-05-09T22:30:00Z",
          },
        ],
      },
    };

    render(<Dashboard envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} />);

    expect(screen.getByTestId("dashboard-summary")).toHaveTextContent("2 workflows need attention");
    expect(screen.getByTestId("dashboard-query-state")).toHaveTextContent("scope: active");
    expect(screen.getByTestId("dashboard-snapshot-history")).toHaveTextContent("snap-001");
    expect(screen.getByTestId("dashboard-tile-tile-progress")).toHaveTextContent(
      "tangent.progress-panel",
    );

    fireEvent.change(screen.getByTestId("dashboard-note"), {
      target: { value: "Need fresh room counts." },
    });
    fireEvent.click(screen.getByTestId("dashboard-refresh"));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        payload: expect.objectContaining({
          dashboard_id: "dashboard-1",
          action: "refresh",
          note: "Need fresh room counts.",
        }),
      }),
    );

    fireEvent.click(screen.getByTestId("dashboard-update"));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        payload: expect.objectContaining({
          action: "update",
        }),
      }),
    );

    fireEvent.click(screen.getByTestId("dashboard-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
