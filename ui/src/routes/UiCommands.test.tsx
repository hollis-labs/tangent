import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { UiChannelProvider } from "@/hooks/useUiCommands";
import { SyntheticUiTransport } from "@/test-drivers/synthetic-ui-transport";
import {
  FixtureEventSource,
  fixtureFetch,
  fixtureResponse,
  syntheticDetail,
} from "@/test-drivers/ui-command-fixtures";
import ChannelPane from "./ChannelPane";
import DocsInbox from "./DocsInbox";
import Inbox from "./Inbox";

function mount(path: string, transport?: SyntheticUiTransport) {
  return render(
    <MemoryRouter initialEntries={[path]} useTransitions={false}>
      <UiChannelProvider transport={transport}>
        <Routes>
          <Route path="/inbox" element={<Inbox />} />
          <Route path="/inbox/items/:itemID" element={<Inbox />} />
          <Route path="/docs" element={<DocsInbox />} />
          <Route path="/channels" element={<ChannelPane />} />
          <Route path="/channels/:channelID" element={<ChannelPane />} />
        </Routes>
      </UiChannelProvider>
    </MemoryRouter>,
  );
}
async function ready(t: SyntheticUiTransport) {
  await waitFor(() => expect(t.descriptor()).toBeDefined());
  act(() => t.publish());
  fireEvent.click(screen.getByRole("checkbox"));
}
beforeEach(() => {
  vi.stubGlobal("EventSource", FixtureEventSource);
  vi.spyOn(document, "hasFocus").mockReturnValue(true);
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockReturnValue([
    { width: 100, height: 20 },
  ] as unknown as DOMRectList);
  vi.spyOn(globalThis, "fetch").mockImplementation(fixtureFetch);
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const writes = () =>
  vi.mocked(fetch).mock.calls.filter(([, init]) => init?.method && init.method !== "GET");

describe("first route UI command adoption", () => {
  it("publishes minimal Inbox observations and applies URL filters/selection through router", async () => {
    const t = new SyntheticUiTransport();
    mount("/inbox", t);
    await screen.findByText("Synthetic review");
    await ready(t);
    expect(t.descriptor().visible_rows).toEqual([{ id: "synthetic-item", summary: "Document" }]);
    expect(JSON.stringify(t.descriptor())).not.toMatch(
      /Private fixture|Synthetic review|standalone-local/,
    );
    act(() => t.command("set_filter", { name: "type", value: "document" }, "url-backed"));
    expect(t.ack().status).toBe("applied");
    expect(screen.getByLabelText("Interaction type")).toHaveValue("document");
    await waitFor(() =>
      expect(
        t.descriptor().active_filters?.some((f) => f.name === "type" && f.values[0] === "document"),
      ).toBe(true),
    );
    act(() => t.publish());
    act(() => t.command("select_item", { id: "synthetic-item" }, "url-backed"));
    expect(t.ack().status).toBe("applied");
    expect(writes()).toHaveLength(0);
  });
  it("withholds raw search and unsupported URL filter values from observations", async () => {
    const t = new SyntheticUiTransport();
    mount("/inbox?q=Private-search&type=Private-type&view=Private-view&sort=Private-sort", t);
    await ready(t);
    expect(JSON.stringify(t.descriptor())).not.toContain("Private-");
    expect(t.descriptor().active_filters).toEqual(
      expect.arrayContaining([
        { name: "view", values: ["unsupported"] },
        { name: "type", values: ["unsupported"] },
        { name: "sort", values: ["unsupported"] },
      ]),
    );
    act(() => t.command("navigate", { route: "/unsupported" }, "url-backed"));
    expect(t.ack().status).toBe("rejected");
    expect(writes()).toHaveLength(0);
  });
  it("opens the read-only Docs viewer with accessible focus and no URL/write changes", async () => {
    const t = new SyntheticUiTransport();
    mount("/docs", t);
    await screen.findByRole("heading", { name: "Synthetic document" });
    await ready(t);
    const checkbox = screen.getByRole("checkbox");
    checkbox.focus();
    const before = window.location.href;
    act(() => t.command("open_doc", { id: "synthetic-doc" }));
    const dialog = screen.getByRole("dialog", { name: "Synthetic document" });
    expect(t.ack().status).toBe("applied");
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(window.location.href).toBe(before);
    expect(writes()).toHaveLength(0);
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(document.activeElement).toBe(checkbox));
  });
  it("acknowledges a cross-route navigation before teardown cancels host control", async () => {
    const t = new SyntheticUiTransport();
    mount("/inbox", t);
    await screen.findByText("Synthetic review");
    await ready(t);
    const order: string[] = [];
    t.onSend = (frame) => {
      if (frame.type === "ui.ack") {
        expect(screen.getByRole("heading", { name: "Channels" })).toBeInTheDocument();
        order.push("ack");
      }
      if (frame.type === "ui.control" && frame.enabled === false) order.push("disable");
      if (frame.type === "view.active" && frame.active === false) order.push("inactive");
    };
    act(() => t.command("navigate", { route: "/channels" }, "url-backed"));
    expect(t.ack().status).toBe("applied");
    await screen.findByRole("heading", { name: "Channels" });
    expect(order.filter((event) => event === "ack")).toHaveLength(1);
    expect(order[0]).toBe("ack");
    expect(order).not.toContain("inactive");
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    expect(writes()).toHaveLength(0);
  });
  it("marks a successful participant channel selection once, after detail load", async () => {
    vi.mocked(fetch).mockImplementation((input, init) =>
      init?.method === "POST" ? Promise.resolve(fixtureResponse({})) : fixtureFetch(input, init),
    );
    mount("/channels");
    fireEvent.click(await screen.findByRole("button", { name: /Synthetic channel/ }));
    await waitFor(() => expect(writes()).toHaveLength(1));
    expect(String(writes()[0][0])).toBe("/api/channels/synthetic-channel/read");
    fireEvent.click(screen.getByRole("button", { name: /Synthetic channel/ }));
    await act(async () => {});
    expect(writes()).toHaveLength(1);
  });
  it("preserves successful ordinary direct-link opens, but never marks a failed load read", async () => {
    vi.mocked(fetch).mockImplementation((input, init) =>
      String(input) === "/api/channels/synthetic-channel"
        ? Promise.reject(new Error("detail unavailable"))
        : fixtureFetch(input, init),
    );
    const failed = mount("/channels/synthetic-channel");
    await screen.findByText("detail unavailable");
    expect(writes()).toHaveLength(0);
    failed.unmount();
    vi.mocked(fetch).mockImplementation((input, init) =>
      init?.method === "POST" ? Promise.resolve(fixtureResponse({})) : fixtureFetch(input, init),
    );
    mount("/channels/synthetic-channel");
    await waitFor(() => expect(writes()).toHaveLength(1));
  });
  it("command focus/modal/navigation does not mark channel read or send messages/HITL writes", async () => {
    const t = new SyntheticUiTransport();
    mount("/channels", t);
    await screen.findByRole("button", { name: /Synthetic channel/ });
    await ready(t);
    act(() => t.command("focus_item", { id: "synthetic-channel" }));
    expect(t.ack().status).toBe("applied");
    act(() => t.command("open_modal", { id: "synthetic-channel" }));
    expect(t.ack().status).toBe("applied");
    expect(writes()).toHaveLength(0);
    await waitFor(() =>
      expect(
        t
          .descriptor()
          .active_filters?.some(
            (filter) => filter.name === "modal" && filter.values[0] === "synthetic-channel",
          ),
      ).toBe(true),
    );
    act(() => t.publish());
    fireEvent.click(screen.getByRole("button", { name: "Close view details" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(
        t
          .descriptor()
          .active_filters?.some(
            (filter) => filter.name === "modal" && filter.values[0] === "closed",
          ),
      ).toBe(true),
    );
    act(() => t.publish());
    act(() => t.command("navigate", { route: "/channels/synthetic-channel" }, "url-backed"));
    expect(t.ack().status).toBe("applied");
    await screen.findByText("No messages yet.");
    expect(writes()).toHaveLength(0);
    expect(JSON.stringify(t.descriptor())).not.toMatch(/Private fixture/);
    expect(syntheticDetail.messages).toHaveLength(0);
  });
});
