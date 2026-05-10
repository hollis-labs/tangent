import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getDashboardDraftStorageKey } from "@/lib/dashboard-draft-storage";
import { Dashboard, type DashboardEnvelope } from "./Dashboard";

describe("<Dashboard>", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.useFakeTimers();
    vi.stubGlobal("navigator", {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders room-backed tiles, saves layouts locally, and submits layout-aware refresh or update actions", async () => {
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
            room_id: "room-123",
          },
          {
            tile_id: "tile-progress",
            kind: "workflow_summary",
            title: "Progress panels",
            value: "2",
            status: "attention",
            workflow: "tangent.progress-panel",
            artifact_ref: "artifact://progress-1",
          },
        ],
        layout: [
          { tile_id: "tile-open", x: 0, y: 0, w: 2, h: 1 },
          { tile_id: "tile-progress", x: 1, y: 0, w: 2, h: 1 },
        ],
        saved_layouts: [
          {
            layout_id: "layout-default",
            name: "Default",
            is_default: true,
            tiles: [
              { tile_id: "tile-open", x: 0, y: 0, w: 2, h: 1 },
              { tile_id: "tile-progress", x: 1, y: 0, w: 2, h: 1 },
            ],
          },
        ],
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
          accepted_snapshot_at: "2026-05-09T22:30:00Z",
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

    render(
      <Dashboard envelope={envelope} onSubmit={onSubmit} onCancel={onCancel} roomID="room-a" />,
    );

    expect(screen.getByTestId("dashboard-summary")).toHaveTextContent("2 workflows need attention");
    expect(screen.getByTestId("dashboard-query-state")).toHaveTextContent("scope: active");
    expect(screen.getByTestId("dashboard-snapshot-history")).toHaveTextContent("snap-001");
    expect(screen.getByTestId("dashboard-export-state")).toHaveTextContent("artifact://progress-1");
    expect(screen.getByTestId("dashboard-open-room-tile-open")).toHaveAttribute(
      "href",
      "/r/room-123",
    );
    expect(screen.getByTestId("dashboard-search")).toHaveValue("progress");

    fireEvent.click(screen.getByTestId("dashboard-move-down-tile-open"));
    fireEvent.change(screen.getByTestId("dashboard-layout-name"), {
      target: { value: "Focus" },
    });
    fireEvent.click(screen.getByTestId("dashboard-save-layout-as-new"));
    expect(screen.getByTestId("dashboard-layout-message")).toHaveTextContent(
      'Created saved layout "Focus".',
    );

    vi.advanceTimersByTime(350);
    const savedDraft = JSON.parse(
      window.localStorage.getItem(getDashboardDraftStorageKey("room-a", "dashboard-1")) ?? "null",
    );
    expect(savedDraft).toMatchObject({
      activeLayoutID: "focus",
      savedLayouts: [{ layout_id: "layout-default" }, { layout_id: "focus", name: "Focus" }],
    });

    fireEvent.click(screen.getByTestId("dashboard-copy-artifact-tile-progress"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.getByTestId("dashboard-artifact-message")).toHaveTextContent(
      "Copied artifact://progress-1.",
    );

    fireEvent.change(screen.getByTestId("dashboard-search"), {
      target: { value: "rooms" },
    });
    fireEvent.change(screen.getByTestId("dashboard-status-filter"), {
      target: { value: "running, blocked" },
    });
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
          active_layout_id: "focus",
          saved_layouts: expect.arrayContaining([
            expect.objectContaining({
              layout_id: "focus",
              name: "Focus",
            }),
          ]),
          layout: expect.arrayContaining([
            expect.objectContaining({
              tile_id: "tile-progress",
              x: 0,
            }),
          ]),
          query_state: expect.objectContaining({
            search: "rooms",
            filters: [
              {
                filter_id: "status",
                label: "Status",
                operator: "in",
                values: ["running", "blocked"],
              },
            ],
          }),
        }),
      }),
    );
    expect(
      window.localStorage.getItem(getDashboardDraftStorageKey("room-a", "dashboard-1")),
    ).toBeNull();

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

  it("recovers a saved dashboard draft from localStorage", () => {
    window.localStorage.setItem(
      getDashboardDraftStorageKey("room-b", "dashboard-2"),
      JSON.stringify({
        activeLayoutID: "focus",
        layout: [
          { tile_id: "tile-b", x: 0, y: 0, w: 1, h: 1 },
          { tile_id: "tile-a", x: 1, y: 0, w: 1, h: 1 },
        ],
        savedLayouts: [
          {
            layout_id: "focus",
            name: "Recovered focus",
            tiles: [
              { tile_id: "tile-b", x: 0, y: 0, w: 1, h: 1 },
              { tile_id: "tile-a", x: 1, y: 0, w: 1, h: 1 },
            ],
          },
        ],
      }),
    );

    render(
      <Dashboard
        envelope={{
          v: 1,
          id: "dashboard-2",
          type: "tangent.dashboard",
          data: {
            dashboard_id: "dashboard-2",
            tiles: [
              { tile_id: "tile-a", kind: "room_count", title: "A" },
              { tile_id: "tile-b", kind: "room_count", title: "B" },
            ],
          },
        }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-b"
      />,
    );

    expect(screen.getByTestId("dashboard-layout-message")).toHaveTextContent(
      "Recovered unsent dashboard layout edits from this browser.",
    );
    expect(screen.getByTestId("dashboard-active-layout")).toHaveValue("focus");
    const orderRoot = screen.getByTestId("dashboard-layout-order");
    expect(orderRoot.textContent?.indexOf("B")).toBeLessThan(
      orderRoot.textContent?.indexOf("A") ?? 0,
    );
  });
});
