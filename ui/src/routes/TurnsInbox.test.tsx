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
  agentId = "agent-nanite",
  agentLabel = "Nanite Worker",
): TurnItemView {
  return {
    contract_version: "1.0",
    item_id: id,
    turn_id: `turn-${id}`,
    session_id: "session-1",
    agent_id: agentId,
    agent_label: agentLabel,
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

function createFetchRouter(inboxState: TurnsInbox, sessionReplies: TurnItemView[] = []) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();

    if (url === "/api/turns") {
      return {
        ok: true,
        json: async () => inboxState,
      } as Response;
    }

    if (url.startsWith("/api/turns/sessions/")) {
      return {
        ok: true,
        json: async () => ({
          contract_version: "1.0",
          session_id: "session-1",
          replies: sessionReplies,
        }),
      } as Response;
    }

    if (url.includes("/reply")) {
      return {
        ok: true,
        json: async () => ({ ok: true }),
      } as Response;
    }

    if (url.includes("/dismiss")) {
      return {
        ok: true,
        json: async () => ({ ok: true }),
      } as Response;
    }

    return {
      ok: true,
      json: async () => ({}),
    } as Response;
  });
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
    const fetchMock = createFetchRouter(mockInbox([]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

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

    const fetchMock = createFetchRouter(mockInbox([item1, item2]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

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

    const fetchMock = createFetchRouter(mockInbox([item1]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

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

    const submitBtn = screen.getByRole("button", { name: /Submit Response/i });
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

  it("supports Cmd+Enter shortcut to submit from textarea", async () => {
    const item1 = mockTurn("turn-shortcut", 1, "question");

    const fetchMock = createFetchRouter(mockInbox([item1]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

    render(
      <MemoryRouter initialEntries={["/turns/items/turn-shortcut"]}>
        <Routes>
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Title for turn-shortcut" }),
    ).toBeInTheDocument();

    const textarea = screen.getByPlaceholderText("Type your guidance or decision for the agent...");
    fireEvent.change(textarea, { target: { value: "Quick reply via shortcut" } });
    fireEvent.keyDown(textarea, { key: "Enter", metaKey: true });

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/turns/items/turn-shortcut/reply",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({
            expected_revision: 2,
            action: "respond",
            response_text: "Quick reply via shortcut",
            selected_option: "",
          }),
        }),
      );
    });
  });

  it("handles dismiss turn", async () => {
    const item1 = mockTurn("turn-dismiss", 1, "checkpoint");

    const fetchMock = createFetchRouter(mockInbox([item1]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

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

  it("filters turns by agent", async () => {
    const item1 = mockTurn("turn-nanite", 1, "question", "presented", "agent-nanite", "Nanite");
    const item2 = mockTurn("turn-codex", 2, "question", "presented", "agent-codex", "Codex");

    const fetchMock = createFetchRouter(mockInbox([item1, item2]));
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

    render(
      <MemoryRouter initialEntries={["/turns"]}>
        <Routes>
          <Route path="/turns" element={<TurnsInboxRoute />} />
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Title for turn-nanite" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Title for turn-codex")).toBeInTheDocument();

    // Click on Codex filter pill
    const codexFilterBtn = screen.getByRole("button", { name: /Codex \(1\)/i });
    fireEvent.click(codexFilterBtn);

    // Only Codex should remain visible in the queue
    expect(screen.getAllByText("Title for turn-codex").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Title for turn-nanite")).not.toBeInTheDocument();
  });

  it("renders session context timeline when earlier session turns exist", async () => {
    const priorTurn = mockTurn(
      "turn-prior",
      1,
      "question",
      "resolved",
      "agent-nanite",
      "Nanite Worker",
    );
    const currentTurn = mockTurn(
      "turn-current",
      2,
      "question",
      "presented",
      "agent-nanite",
      "Nanite Worker",
    );

    const fetchMock = createFetchRouter(mockInbox([currentTurn], [priorTurn]), [
      priorTurn,
      currentTurn,
    ]);
    vi.spyOn(globalThis, "fetch").mockImplementation(fetchMock);

    render(
      <MemoryRouter initialEntries={["/turns/items/turn-current"]}>
        <Routes>
          <Route path="/turns/items/:itemID" element={<TurnsInboxRoute />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(
      await screen.findByRole("heading", { name: "Title for turn-current" }),
    ).toBeInTheDocument();

    // Session Context should be visible and show 1 earlier turn
    expect(await screen.findByText(/Session Context \(1 earlier turns\)/i)).toBeInTheDocument();
    expect(screen.getByText(/#1 Title for turn-prior/i)).toBeInTheDocument();
    expect(screen.getAllByText(/Looks good/i).length).toBeGreaterThanOrEqual(1);
  });
});
