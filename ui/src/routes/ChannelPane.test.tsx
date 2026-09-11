import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, it, vi } from "vitest";

import { expectProseRendered, proseProbe } from "@/components/markdown/prose-probe";
import type { ChannelDetail, ChannelSummary } from "@/lib/channel-api";
import ChannelPaneRoute from "./ChannelPane";

class MockEventSource {
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(url: string) {
    this.url = url;
  }

  addEventListener() {}
  removeEventListener() {}
  close() {}
}

describe("<ChannelPaneRoute>", () => {
  beforeEach(() => {
    vi.stubGlobal("EventSource", MockEventSource);
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // Both directions route through the shared renderer, which is the answer to
  // the one judgment call this sweep left open: `MessageRow` renders operator
  // and agent messages from one component, and splitting them would print the
  // operator's literal ** beside the agent's rendered bold in one thread.
  it("routes both message directions through the shared markdown renderer", async () => {
    mockFetch(async (path) => {
      if (path === "/api/channels") return jsonResponse({ channels: [summary()] });
      return jsonResponse(detail());
    });

    renderRoute("/channels/chan-1");

    await screen.findAllByTestId("channel-message-body");

    expectProseRendered("chan-agent");
    expectProseRendered("chan-operator");
  });
});

function summary(): ChannelSummary {
  return {
    channel_id: "chan-1",
    title: "Release check",
    unread_count: 0,
    needs_input_count: 0,
  };
}

function detail(): ChannelDetail {
  return {
    channel_id: "chan-1",
    title: "Release check",
    // Newest first, as the API returns them.
    messages: [
      {
        exchange_id: "x2",
        direction: "operator",
        body: proseProbe("chan-operator"),
        created_at: "2026-09-10T15:01:00Z",
        delivery_state: "accepted-by-peer",
      },
      {
        exchange_id: "x1",
        direction: "agent",
        body: proseProbe("chan-agent"),
        created_at: "2026-09-10T15:00:00Z",
      },
    ],
    hitl_items: [],
  };
}

function renderRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/channels" element={<ChannelPaneRoute />} />
        <Route path="/channels/:channelID" element={<ChannelPaneRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

function mockFetch(handler: (path: string, init?: RequestInit) => Promise<Response>) {
  vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => handler(String(input), init));
}

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}
