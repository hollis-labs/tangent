import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TabStrip } from "./TabStrip";

describe("<TabStrip>", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders active rooms from session_list", async () => {
    mockFetchSequence([
      { rooms: [room("room-a", "Room A", "tangent.triage"), room("room-b", "Room B")] },
    ]);

    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="*" element={<TabStrip />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByTestId("tab-strip-room-room-a")).toHaveTextContent(
      "Room A (tangent.triage)",
    );
    expect(screen.getByTestId("tab-strip-room-room-b")).toHaveTextContent("Room B");
  });

  it("clicking a room tab switches the URL", async () => {
    mockFetchSequence([{ rooms: [room("room-a", "Room A"), room("room-b", "Room B")] }]);

    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route
            path="*"
            element={
              <>
                <TabStrip />
                <PathProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByTestId("tab-strip-room-room-b"));
    expect(await screen.findByTestId("path-probe")).toHaveTextContent("/r/room-b");
  });

  it("closing a room calls session_close and refreshes the list", async () => {
    mockFetchSequence([
      { rooms: [room("room-a", "Room A"), room("room-b", "Room B")] },
      { ok: true, roomID: "room-a", status: "closed" },
      { rooms: [room("room-b", "Room B")] },
    ]);

    render(
      <MemoryRouter initialEntries={["/r/room-a"]}>
        <Routes>
          <Route
            path="*"
            element={
              <>
                <TabStrip />
                <PathProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByTestId("tab-strip-close-room-a"));
    await waitFor(() => {
      expect(screen.queryByTestId("tab-strip-room-room-a")).not.toBeInTheDocument();
    });
    expect(screen.getByTestId("path-probe")).toHaveTextContent("/");
  });
});

function room(id: string, title: string, currentType?: string) {
  return {
    id,
    title,
    current_envelope_type: currentType,
    created_at: "2026-05-08T00:00:00Z",
    updated_at: "2026-05-08T00:00:00Z",
  };
}

function mockFetchSequence(payloads: Array<Record<string, unknown>>) {
  const responses = payloads.map((payload) => ({
    ok: true,
    json: async () => ({
      result: {
        content: [{ text: JSON.stringify(payload) }],
      },
    }),
  }));
  vi.spyOn(globalThis, "fetch").mockImplementation(async () => responses.shift() as Response);
}

function PathProbe() {
  const location = useLocation();
  return <div data-testid="path-probe">{location.pathname}</div>;
}
