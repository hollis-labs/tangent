import { act, render, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Inbox from "./Inbox";

const { fetchInbox } = vi.hoisted(() => ({ fetchInbox: vi.fn() }));
vi.mock("@/lib/inbox-api", async (original) => ({
  ...(await original<typeof import("@/lib/inbox-api")>()),
  fetchInbox,
}));

class FakeSource {
  static instances: FakeSource[] = [];
  static OPEN = 1;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSource.instances.push(this);
  }
  addEventListener() {}
  close() {}
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  vi.stubGlobal("EventSource", FakeSource);
  fetchInbox.mockResolvedValue([]);
});
afterEach(() => {
  FakeSource.instances = [];
  vi.clearAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Inbox fallback polling", () => {
  it("polls on the short interval while streams have never connected, and relaxes once all are open", async () => {
    render(
      <MemoryRouter>
        <Inbox />
      </MemoryRouter>,
    );
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));
    expect(FakeSource.instances).toHaveLength(3);

    // Every stream fails before its first open: the Inbox must tighten to 3s.
    act(() => FakeSource.instances.forEach((source) => source.onerror?.()));
    await act(async () => {
      vi.advanceTimersByTime(3_000);
    });
    expect(fetchInbox).toHaveBeenCalledTimes(2);

    // Streams reconnect: back to the slow safety net.
    act(() => FakeSource.instances.forEach((source) => source.onopen?.()));
    await act(async () => {
      vi.advanceTimersByTime(3_000);
    });
    expect(fetchInbox).toHaveBeenCalledTimes(2);
    await act(async () => {
      vi.advanceTimersByTime(27_000);
    });
    expect(fetchInbox).toHaveBeenCalledTimes(3);
  });
});
