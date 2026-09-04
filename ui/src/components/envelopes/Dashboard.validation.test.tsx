// Validation-affordance coverage for the dashboard.
//
// The dashboard has NO terminal gate by design — an unchanged dashboard is a
// legitimate thing to submit, and both Refresh and Submit update run without
// validating anything. Its real defect was elsewhere: the layout name is
// conditionally required by two layout actions, was marked required nowhere,
// and its refusal rendered in the same emerald paragraph as "Saved changes to
// …" — an error in success styling.

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { Dashboard, type DashboardEnvelope } from "./Dashboard";

function sampleEnvelope(): DashboardEnvelope {
  return {
    v: 1,
    id: "dashboard-1",
    type: "tangent.dashboard",
    title: "Ops dashboard",
    data: {
      dashboard_id: "dashboard-1",
      tiles: [
        { tile_id: "tile-open", kind: "room_count", title: "Open rooms", value: "4" },
        {
          tile_id: "tile-progress",
          kind: "workflow_summary",
          title: "Progress panels",
          value: "2",
        },
      ],
      layout: [
        { tile_id: "tile-open", x: 0, y: 0, w: 2, h: 1 },
        { tile_id: "tile-progress", x: 1, y: 0, w: 2, h: 1 },
      ],
    },
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

describe("Dashboard validation affordances", () => {
  it("marks the layout name required while it is missing and binds the semantics", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const name = screen.getByTestId("dashboard-layout-name");
    expect(screen.getByTestId("dashboard-layout-name-required")).toBeInTheDocument();
    expect(name).toHaveAttribute("aria-required", "true");
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAttribute("aria-describedby", "dashboard-layout-name-hint");
    expect(document.querySelector("label[for='dashboard-layout-name']")).toHaveTextContent(
      "Layout name",
    );
    expect(document.getElementById("dashboard-layout-name-hint")).toHaveTextContent(
      "Required by Save layout and Save as new. Submitting the dashboard does not need one.",
    );

    fireEvent.change(name, { target: { value: "Focus" } });
    expect(screen.queryByTestId("dashboard-layout-name-required")).not.toBeInTheDocument();
    expect(name).toHaveAttribute("aria-invalid", "false");
  });

  it("renders the layout-name refusal as an error, not in success green", () => {
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("dashboard-save-layout"));

    const error = screen.getByTestId("dashboard-layout-error");
    expect(error).toHaveTextContent("Layout name is required before saving.");
    expect(error).toHaveAttribute("role", "alert");
    expect(error.className).toContain("text-red-300");
    expect(error.className).not.toContain("emerald");
    // The success surface stays empty rather than carrying the failure.
    expect(screen.queryByTestId("dashboard-layout-message")).not.toBeInTheDocument();

    // The failing control is bound to the error, focused, and scrolled to.
    expect(screen.getByTestId("dashboard-layout-name")).toHaveAttribute(
      "aria-describedby",
      "dashboard-layout-name-hint dashboard-layout-error",
    );
    expect(document.activeElement).toBe(screen.getByTestId("dashboard-layout-name"));
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });

    // And typing clears it without a second click.
    fireEvent.change(screen.getByTestId("dashboard-layout-name"), { target: { value: "Focus" } });
    expect(screen.queryByTestId("dashboard-layout-error")).not.toBeInTheDocument();
  });

  it("reports the apply-order refusal as an error against the layout picker", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.click(screen.getByTestId("dashboard-apply-layout-order"));

    expect(screen.getByTestId("dashboard-layout-error")).toHaveTextContent(
      "Save the current arrangement as a named layout first.",
    );
    // It belongs to the layout picker, so the name field must not claim it.
    expect(screen.getByTestId("dashboard-layout-name")).toHaveAttribute(
      "aria-describedby",
      "dashboard-layout-name-hint",
    );
  });

  it("keeps a successful layout save in the success surface", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByTestId("dashboard-layout-name"), { target: { value: "Focus" } });
    fireEvent.click(screen.getByTestId("dashboard-save-layout"));

    const message = screen.getByTestId("dashboard-layout-message");
    expect(message).toHaveTextContent('Saved changes to "Focus".');
    expect(message).toHaveAttribute("role", "status");
    expect(screen.queryByTestId("dashboard-layout-error")).not.toBeInTheDocument();
  });

  it("names the tile each reorder button moves", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("dashboard-move-down-tile-open")).toHaveAttribute(
      "aria-label",
      "Move Open rooms down",
    );
    expect(screen.getByTestId("dashboard-move-up-tile-progress")).toHaveAttribute(
      "aria-label",
      "Move Progress panels up",
    );
  });

  it("says the status filter is comma-separated somewhere a screen reader hears", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const filter = screen.getByTestId("dashboard-status-filter");
    expect(filter).toHaveAttribute("aria-describedby", "dashboard-status-filter-hint");
    expect(document.getElementById("dashboard-status-filter-hint")).toHaveTextContent(
      "Comma-separated.",
    );
  });

  it("warns that the operator note is not kept in the local draft", () => {
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("dashboard-note")).toHaveAttribute(
      "aria-describedby",
      "dashboard-note-hint",
    );
    expect(document.getElementById("dashboard-note-hint")).toHaveTextContent(
      "the local draft only keeps the layout — a reload clears this note",
    );
  });

  it("leaves both terminal CTAs ungated and the submitted payload untouched", () => {
    const onSubmit = vi.fn();
    render(<Dashboard envelope={sampleEnvelope()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    // No gate notice: nothing is required to submit this workflow.
    expect(screen.queryByTestId("dashboard-submit-gate")).not.toBeInTheDocument();
    expect(screen.getByTestId("dashboard-update")).not.toBeDisabled();
    expect(screen.getByTestId("dashboard-refresh")).not.toBeDisabled();
    expect(screen.getByTestId("dashboard-update")).not.toHaveAttribute("aria-describedby");

    fireEvent.change(screen.getByTestId("dashboard-status-filter"), {
      target: { value: "running, blocked" },
    });
    fireEvent.change(screen.getByTestId("dashboard-note"), { target: { value: "  checked  " } });
    fireEvent.click(screen.getByTestId("dashboard-update"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const response = onSubmit.mock.calls[0][0];
    expect(response.kind).toBe("data");
    expect(response.status).toBe("submitted");
    expect(response.payload.dashboard_id).toBe("dashboard-1");
    expect(response.payload.action).toBe("update");
    expect(response.payload.note).toBe("checked");
    expect(response.payload.active_layout_id).toBeUndefined();
    expect(response.payload.query_state).toEqual({
      search: undefined,
      scope: undefined,
      group_by: undefined,
      filters: [
        { filter_id: "status", label: "Status", operator: "in", values: ["running", "blocked"] },
      ],
      sort: [{ field: "updated_at", direction: "desc" }],
    });
  });
});
