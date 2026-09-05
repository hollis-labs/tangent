// Validation-affordance coverage for the progress panel.
//
// This workflow keeps a live "Send update" button that validates on click —
// that click is how it reports "nothing selected" — so the notice runs in
// `attempt` mode. The failures pinned here are the ones the audit found: one
// error string covering two different requirements and telling the truth about
// neither, a card in the left column silently mutating a field 360px away in
// the right one, six unlabelled controls, an optional note whose placeholder
// read as a mandate, quick actions that overwrite that note without saying so,
// and a recovery banner that understated what it had restored.

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getProgressPanelDraftStorageKey } from "@/lib/progress-panel-storage";
import { ProgressPanel, type ProgressPanelEnvelope } from "./ProgressPanel";

function envelopeWithItems(): ProgressPanelEnvelope {
  return {
    v: 1,
    id: "progress-env-1",
    type: "tangent.progress-panel",
    title: "Progress",
    data: {
      panel_id: "panel-1",
      items: [
        { item_id: "item-1", label: "Scan repo", status: "running" },
        { item_id: "item-2", label: "Write summary", status: "queued" },
      ],
      updates: [{ update_id: "upd-001", kind: "status", item_id: "item-1", status: "running" }],
    },
  };
}

function emptyEnvelope(): ProgressPanelEnvelope {
  return {
    v: 1,
    id: "progress-env-2",
    type: "tangent.progress-panel",
    data: { panel_id: "panel-empty", items: [] },
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

describe("ProgressPanel validation affordances", () => {
  it("names the real requirement when the panel has nothing to select", () => {
    // Before: one message said "Select a progress item" on a panel that has no
    // items at all, and the CTA offered no standing explanation.
    render(<ProgressPanel envelope={emptyEnvelope()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const notice = screen.getByTestId("progress-panel-submit-gate");
    expect(notice).toHaveAttribute("role", "status");
    expect(notice).toHaveAttribute("aria-live", "polite");
    expect(screen.getByTestId("progress-panel-submit-gate-reason")).toHaveTextContent(
      "Cannot send update yet: this panel has no progress items to update yet.",
    );
    expect(screen.getByTestId("progress-panel-submit")).toHaveAttribute(
      "aria-describedby",
      "progress-panel-submit-gate",
    );
  });

  it("keeps the CTA live and focuses the item picker on a blocked click", () => {
    // `mode="attempt"`: the button is never disabled, so an attempted submit is
    // what has to take the operator somewhere.
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    const onSubmit = vi.fn();
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    // Clearing the select is the only way to reach the "no item" state with a
    // populated panel; the select has no empty option, so drive it directly.
    const select = screen.getByTestId("progress-panel-item-select") as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "" } });

    expect(screen.getByTestId("progress-panel-submit")).not.toBeDisabled();
    expect(screen.getByTestId("progress-panel-submit-gate-reason")).toHaveTextContent(
      "Cannot send update yet: no progress item is selected yet.",
    );
    expect(screen.getByTestId("progress-panel-item-required")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("progress-panel-submit"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(select);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "center" });
    expect(screen.getByTestId("progress-panel-submit-error")).toHaveTextContent(
      "No progress item is selected yet.",
    );
    expect(screen.getByTestId("progress-panel-submit-error")).toHaveAttribute("role", "alert");
    expect(select).toHaveAttribute(
      "aria-describedby",
      "progress-panel-item-select-hint progress-panel-item-select-error",
    );
  });

  it("labels every send-update control and binds its hint", () => {
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    for (const id of [
      "progress-panel-item-select",
      "progress-panel-status-select",
      "progress-panel-note",
      "progress-panel-checkpoint",
      "progress-panel-filter-item",
      "progress-panel-filter-kind",
    ]) {
      expect(document.querySelector(`label[for='${id}']`)).toBeTruthy();
    }

    expect(screen.getByTestId("progress-panel-status-select")).toHaveAttribute(
      "aria-describedby",
      "progress-panel-status-select-hint",
    );
    expect(screen.getByTestId("progress-panel-checkpoint-input")).toHaveAttribute(
      "aria-describedby",
      "progress-panel-checkpoint-hint",
    );
  });

  it("says the update note is optional and that it carries the reason", () => {
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const note = screen.getByTestId("progress-panel-summary-input");
    // The placeholder used to be an imperative mandate on an optional field.
    expect(note).toHaveAttribute("placeholder", "Optional — what changed, or why");
    expect(note).toHaveAttribute("aria-describedby", "progress-panel-note-hint");
    expect(document.getElementById("progress-panel-note-hint")).toHaveTextContent(
      "Optional. Sent as the update's summary, and the only place a paused, blocked, cancelled or failed status records why. The quick actions above overwrite it.",
    );
    // No RequiredMark: making it required would be new validation, not an
    // affordance fix.
    expect(screen.queryByTestId("progress-panel-note-required")).not.toBeInTheDocument();
  });

  it("warns that the quick actions overwrite the note, and they still do", () => {
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("progress-panel-controls-hint")).toHaveTextContent(
      "Each quick action sets the item and status and replaces anything typed in Update note.",
    );

    fireEvent.change(screen.getByTestId("progress-panel-summary-input"), {
      target: { value: "hand-written note" },
    });
    fireEvent.click(screen.getByTestId("progress-panel-control-pause"));
    // Behaviour is unchanged — only the warning is new.
    expect(screen.getByTestId("progress-panel-summary-input")).toHaveValue("Paused scan repo.");
  });

  it("makes the cross-column item selection legible", () => {
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("progress-panel-items-hint")).toHaveTextContent(
      "Choosing an item here sets Item and Status in the Send update panel.",
    );
    const card = screen.getByTestId("progress-panel-item-item-2");
    expect(card).toHaveAttribute("aria-controls", "progress-panel-item-select");
    expect(card).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(card);
    expect(card).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("progress-panel-item-select")).toHaveValue("item-2");
  });

  it("gives the history tabs tab semantics rather than colour alone", () => {
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("progress-panel-tabs")).toHaveAttribute("role", "tablist");
    const timeline = screen.getByTestId("progress-panel-tab-timeline");
    expect(timeline).toHaveAttribute("role", "tab");
    expect(timeline).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("progress-panel-timeline")).toHaveAttribute("role", "tabpanel");

    fireEvent.click(screen.getByTestId("progress-panel-tab-logs"));
    expect(timeline).toHaveAttribute("aria-selected", "false");
    expect(screen.getByTestId("progress-panel-tab-logs")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByTestId("progress-panel-logs")).toHaveAttribute(
      "aria-labelledby",
      "progress-panel-tab-logs",
    );
  });

  it("says what the recovered draft actually restored", () => {
    // The store is named ".view." but persists the unsent status, note and
    // checkpoint label too; the banner used to mention only "view state".
    window.localStorage.setItem(
      getProgressPanelDraftStorageKey("room-9", "panel-1"),
      JSON.stringify({
        activeTab: "timeline",
        filterItemID: "",
        filterKind: "all",
        selectedUpdateID: "",
        selectedItemID: "item-2",
        status: "blocked",
        note: "Unsaved note",
        checkpointLabel: "Unsaved checkpoint",
      }),
    );

    render(
      <ProgressPanel
        envelope={envelopeWithItems()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
        roomID="room-9"
      />,
    );

    const banner = screen.getByTestId("progress-panel-message");
    expect(banner).toHaveAttribute("role", "status");
    expect(banner).toHaveAttribute("aria-live", "polite");
    expect(banner).toHaveTextContent("item, status, note, and checkpoint label");
    expect(screen.getByTestId("progress-panel-summary-input")).toHaveValue("Unsaved note");
  });

  it("leaves the submitted payload shape untouched", () => {
    const onSubmit = vi.fn();
    render(<ProgressPanel envelope={envelopeWithItems()} onSubmit={onSubmit} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByTestId("progress-panel-item-select"), {
      target: { value: "item-2" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-status-select"), {
      target: { value: "completed" },
    });
    fireEvent.change(screen.getByTestId("progress-panel-summary-input"), {
      target: { value: "  Summary drafted  " },
    });
    fireEvent.click(screen.getByTestId("progress-panel-submit"));

    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[0][0]).toEqual({
      v: 1,
      envelopeId: "progress-env-1",
      kind: "data",
      status: "submitted",
      payload: {
        panel_id: "panel-1",
        item_id: "item-2",
        status: "completed",
        summary: "Summary drafted",
        // Still `|| undefined` rather than an empty string.
        checkpoint_label: undefined,
      },
    });
  });
});
