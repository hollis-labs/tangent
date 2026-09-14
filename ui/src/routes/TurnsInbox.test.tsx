import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TurnItemView, TurnsInbox } from "@/lib/turns-api";
import TurnsInboxRoute from "./TurnsInbox";

class MockEventSource {
  static instances: MockEventSource[] = [];
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Set<() => void>>();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
    queueMicrotask(() => this.onopen?.());
  }

  addEventListener(type: string, listener: () => void) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: () => void) {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) listener();
  }

  close() {}
}

function mockTurn(
  id: string,
  seq: number,
  kind: "question" | "approval" | "checkpoint" = "question",
  state: "presented" | "resolved" = "presented",
): TurnItemView {
  return {
    contract_version: "1.0",
    item_id: id,
    turn_id: `turn-${id}`,
    session_id: "session-1",
    agent_id: "agent-nanite",
    agent_label: "Nanite Worker",
    application_id: "tether",
    kind,
    title: `Title for ${id}`,
    summary: `Summary for ${id}`,
    content: `Detailed content for ${id}`,
    options: [
      { label: "Option One", value: "opt_1", recommended: true },
      { label: "Option Two", value: "opt_2" },
    ],
    state,
    queue_sequence: seq,
    queue_position: seq,
    revision: 2,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    delivery_state: state === "resolved" ? "acknowledged" : "queued",
    resolution:
      state === "resolved"
        ? {
            resolution_id: `res-${id}`,
            action: "approve",
            response_text: "Looks good",
            resolved_at: new Date().toISOString(),
            resolved_by: "local-operator",
          }
        : undefined,
  };
}

function mockInbox(pending: TurnItemView[], history: TurnItemView[] = []): TurnsInbox {
  return {
    contract_version: "1.0",
    surface_id: "surface_turns_default",
    revision: "rev-1",
    synced_at: new Date().toISOString(),
    pending,
    history,
    total_pending: pending.length,
    total_terminal: history.length,
  };
}

describe("<TurnsInboxRoute>", () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("renders empty inbox state", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([]),
    } as Response);

    render(
      <MemoryRouter initialEntries={["/turns"]}>
        <Routes>
          <Route path="/turns" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText("Agent turns FIFO inbox")).toBeInTheDocument();
    expect(await screen.findByText("No pending turns")).toBeInTheDocument();
  });

  it("renders pending turns in queue and displays selected turn details", async () => {
    const item1 = mockTurn("turn-1", 1, "question");
    const item2 = mockTurn("turn-2", 2, "approval");

    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([item1, item2]),
    } as Response);

    render(
      <MemoryRouter initialEntries={["/turns"]}>
        <Routes>
          <Route path="/turns" element={<TurnsInboxRoute />} />
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { name: "Title for turn-1" })).toBeInTheDocument();
    expect(screen.getByText("Title for turn-2")).toBeInTheDocument();
    expect(screen.getByText("#1")).toBeInTheDocument();
    expect(screen.getByText("#2")).toBeInTheDocument();

    // Default selection is first item
    expect(screen.getAllByText("Detailed content for turn-1").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Option One")).toBeInTheDocument();
    expect(screen.getByText("Recommended")).toBeInTheDocument();
  });

  it("submits a reply to a question turn", async () => {
    const item1 = mockTurn("turn-q", 1, "question");

    const fetchMock = vi.spyOn(globalThis, "fetch");
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([item1]),
    } as Response);

    render(
      <MemoryRouter initialEntries={["/turns/items/turn-q"]}>
        <Routes>
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole("heading", { name: "Title for turn-q" })).toBeInTheDocument();

    // Type response in textarea
    const textarea = screen.getByPlaceholderText("Type your guidance or decision for the agent...");
    fireEvent.change(textarea, { target: { value: "Proceed with caution" } });

    // Mock reply POST and subsequent inbox refresh
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ ...item1, state: "resolved" }),
    } as Response);

    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([], [{ ...item1, state: "resolved" }]),
    } as Response);

    const submitBtn = screen.getByRole("button", { name: "Submit Response" });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/turns/items/turn-q/reply",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            expected_revision: 2,
            action: "respond",
            response_text: "Proceed with caution",
            selected_option: "",
          }),
        }),
      );
    });
  });

  it("handles dismiss turn", async () => {
    const item1 = mockTurn("turn-dismiss", 1, "checkpoint");

    const fetchMock = vi.spyOn(globalThis, "fetch");
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([item1]),
    } as Response);

    render(
      <MemoryRouter initialEntries={["/turns/items/turn-dismiss"]}>
        <Routes>
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Title for turn-dismiss" }),
    ).toBeInTheDocument();

    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ ...item1, state: "canceled" }),
    } as Response);

    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => mockInbox([], [{ ...item1, state: "canceled" }]),
    } as Response);

    const dismissBtn = screen.getByRole("button", { name: "Dismiss Turn" });
    fireEvent.click(dismissBtn);

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/turns/items/turn-dismiss/dismiss",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            expected_revision: 2,
            reason: "Dismissed by operator",
          }),
        }),
      );
    });
  });
});
