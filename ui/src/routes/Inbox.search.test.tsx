import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InboxEntry } from "@/lib/inbox-api";
import Inbox from "./Inbox";

const { fetchInbox, fetchInboxItem, searchInbox } = vi.hoisted(() => ({
  fetchInbox: vi.fn(),
  fetchInboxItem: vi.fn(),
  searchInbox: vi.fn(),
}));
vi.mock("@/lib/inbox-api", async (original) => ({
  ...(await original<typeof import("@/lib/inbox-api")>()),
  fetchInbox,
  fetchInboxItem,
  searchInbox,
}));
vi.mock("./InboxItemBody", () => ({
  InboxItemBody: ({ entry }: { entry: InboxEntry }) => (
    <p>Body for {entry.interaction.interaction_id}</p>
  ),
}));

class FakeSource {
  static instances: FakeSource[] = [];
  listeners = new Map<string, Array<() => void>>();
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSource.instances.push(this);
  }
  addEventListener(type: string, fn: () => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {}
}

function entry(id: string, sequence: number): InboxEntry {
  return {
    sequence,
    interaction: {
      interaction_id: id,
      definition_binding: { kind: "tangent.hitl-item" },
      request_snapshot: { title: `Request ${id}` },
      caller_scope: "standalone-local:test",
      state: "presented",
      revision: 1,
      created_at: "2026-10-01T10:00:00Z",
      updated_at: "2026-10-01T10:00:00Z",
    },
  };
}
function renderInbox(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/" element={<Inbox />} />
        <Route path="/inbox/items/:itemID" element={<Inbox />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeSource);
  fetchInbox.mockResolvedValue([entry("a", 1)]);
  searchInbox.mockResolvedValue([]);
});
afterEach(() => {
  FakeSource.instances = [];
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

describe("Inbox content search", () => {
  it("reruns when the list changes, so a new body-only match appears without retyping", async () => {
    renderInbox("/?q=needle");
    await waitFor(() => expect(searchInbox).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Request b")).not.toBeInTheDocument();

    fetchInbox.mockResolvedValue([entry("a", 1), entry("b", 2)]);
    searchInbox.mockResolvedValue(["b"]);
    act(() => {
      for (const source of FakeSource.instances) {
        for (const fn of source.listeners.get("revision") ?? []) fn();
      }
    });
    expect(await screen.findByText("Request b", {}, { timeout: 2000 })).toBeInTheDocument();
    expect(searchInbox).toHaveBeenCalledTimes(2);
  });

  it("does not reuse results from another query, and reports a failed search", async () => {
    fetchInbox.mockResolvedValue([entry("a", 1), entry("b", 2)]);
    searchInbox.mockResolvedValueOnce(["b"]);
    renderInbox("/?q=alpha");
    expect(await screen.findByText("Request b", {}, { timeout: 2000 })).toBeInTheDocument();

    searchInbox.mockRejectedValue(new Error("down"));
    fireEvent.change(screen.getByPlaceholderText("Search inbox"), { target: { value: "beta" } });
    expect(
      await screen.findByText(/Content search failed/, {}, { timeout: 2000 }),
    ).toBeInTheDocument();
    // "b" matched the old query only; it must not survive into "beta".
    expect(screen.queryByText("Request b")).not.toBeInTheDocument();
  });
});

describe("Inbox item detail", () => {
  it("clears the previous item's error when another item is selected", async () => {
    fetchInbox.mockResolvedValue([entry("a", 1), entry("b", 2)]);
    fetchInboxItem.mockImplementation((id: string) =>
      id === "a" ? Promise.reject(new Error("a unavailable")) : Promise.resolve(entry(id, 2)),
    );
    renderInbox("/inbox/items/a");
    expect(await screen.findByText("a unavailable")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Request b/ }));
    expect(await screen.findByText("Body for b")).toBeInTheDocument();
    expect(screen.queryByText("a unavailable")).not.toBeInTheDocument();
  });
});
