import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { idCommand, type UiHandler } from "@/lib/ui-channel";
import { SyntheticUiTransport } from "@/test-drivers/synthetic-ui-transport";
import { UiChannelProvider, UiControl, useUiCommands, useViewDescriptor } from "./useUiCommands";

function View({ extra = [] }: { extra?: UiHandler[] }) {
  const [value, setValue] = useState("initial");
  const location = useLocation();
  const commands = useUiCommands([
    idCommand("open_modal", "ephemeral", (args) => {
      if (args.id !== "visible") return "not_visible";
      setValue("applied");
      return "applied";
    }),
    ...extra,
  ]);
  const control = useViewDescriptor(
    { search: value, visible_rows: [{ id: "visible", summary: "Item" }] },
    commands,
  );
  return (
    <>
      <UiControl control={control} />
      <p>{value}</p>
      <p>{location.pathname}</p>
      <button type="button" onClick={() => setValue("changed")}>
        Change view
      </button>
    </>
  );
}
function mount(transport?: SyntheticUiTransport, extra?: UiHandler[]) {
  return render(
    <MemoryRouter initialEntries={["/inbox"]} useTransitions={false}>
      <UiChannelProvider transport={transport}>
        <View extra={extra} />
      </UiChannelProvider>
    </MemoryRouter>,
  );
}
function tick() {
  act(() => vi.advanceTimersByTime(120));
}
function ready(t: SyntheticUiTransport) {
  tick();
  act(() => t.publish());
  fireEvent.click(screen.getByRole("checkbox"));
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(document, "hasFocus").mockReturnValue(true);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("React host UI channel", () => {
  it("refuses unattached views and defaults control off", () => {
    mount();
    expect(screen.getByRole("checkbox")).toBeDisabled();
  });
  it("requires publication and participant opt-in, checks scope/revision/arguments/targets, and acks after commit", () => {
    const t = new SyntheticUiTransport();
    mount(t);
    tick();
    act(() => t.publish());
    act(() => t.command("open_modal", { id: "visible" }));
    expect(t.ack().status).toBe("rejected");
    fireEvent.click(screen.getByRole("checkbox"));
    act(() => t.command("open_modal", { id: "visible" }, "url-backed"));
    expect(t.ack().status).toBe("rejected");
    act(() => t.command("open_modal", { id: "visible" }, "ephemeral", 99));
    expect(t.ack().status).toBe("rejected");
    act(() => t.command("open_modal", { id: "visible", extra: true }));
    expect(t.ack().status).toBe("rejected");
    act(() => t.command("open_modal", { id: "missing" }));
    expect(t.ack().status).toBe("not_visible");
    act(() => t.command("undeclared", { id: "visible" }));
    expect(t.ack().status).toBe("rejected");
    t.onSend = (frame) => {
      if (frame.type === "ui.ack" && (frame.ack as { status: string }).status === "applied")
        expect(screen.getByText("applied")).toBeInTheDocument();
    };
    let command: ReturnType<typeof t.command>;
    act(() => {
      command = t.command("open_modal", { id: "visible" });
    });
    expect(t.ack().status).toBe("applied");
    act(() => t.emit(command));
    expect(t.ack().status).toBe("rejected");
  });
  it("fences changed views immediately and serializes publications", () => {
    const t = new SyntheticUiTransport();
    mount(t);
    tick();
    fireEvent.click(screen.getByText("Change view"));
    tick();
    expect(t.frames.filter((frame) => frame.type === "view.publish")).toHaveLength(1);
    act(() => t.publish());
    tick();
    expect(t.descriptor().search).toBe("changed");
    act(() => t.publish());
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByText("Change view")); // already changed: no new observation
    act(() => t.command("open_modal", { id: "visible" }, "ephemeral", 1));
    expect(t.ack().status).toBe("rejected");
  });
  it("does not let descriptor publication or timers renew latest-active selection", () => {
    const t = new SyntheticUiTransport();
    mount(t);
    ready(t);
    const activeCount = t.frames.filter((frame) => frame.type === "view.active").length;
    fireEvent.click(screen.getByText("Change view"));
    tick();
    act(() => t.publish());
    tick();
    expect(t.frames.filter((frame) => frame.type === "view.active")).toHaveLength(activeCount);
    fireEvent.keyDown(window, { key: "Tab" });
    expect(t.frames.at(-1)).toEqual({ type: "view.active", active: true });
    vi.spyOn(document, "hasFocus").mockReturnValue(false);
    fireEvent.blur(window);
    expect(t.frames.at(-1)).toEqual({ type: "view.active", active: false });
    act(() => t.command("open_modal", { id: "visible" }));
    expect(t.ack().status).toBe("rejected");
  });
  it("blocks ephemeral history mutation and restores the router methods", () => {
    const push = window.history.pushState;
    const t = new SyntheticUiTransport();
    mount(t, [
      idCommand("bad_history", "ephemeral", () => {
        window.history.pushState({}, "", "/bad");
        return "applied";
      }),
    ]);
    ready(t);
    const before = window.location.href;
    act(() => t.command("bad_history", { id: "visible" }));
    expect(t.ack().status).toBe("rejected");
    expect(window.location.href).toBe(before);
    expect(window.history.pushState).toBe(push);
  });
  it("disables and forgets revisions on disconnect; reconnect requires fresh opt-in", () => {
    const t = new SyntheticUiTransport();
    mount(t);
    ready(t);
    act(() => {
      t.online = false;
      t.emit({ type: "detached" });
    });
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    act(() => {
      t.online = true;
      t.emit({ type: "attached" });
    });
    tick();
    act(() => t.publish());
    act(() => t.command("open_modal", { id: "visible" }));
    expect(t.ack().status).toBe("rejected");
  });
  it("does not assign a previous route's delayed publication ack to a replacement route", () => {
    const t = new SyntheticUiTransport();
    const old = mount(t);
    tick();
    old.unmount();
    mount(t);
    tick();
    expect(t.frames.filter((frame) => frame.type === "view.publish")).toHaveLength(1);
    act(() => t.publish());
    expect(screen.getByRole("checkbox")).toBeDisabled();
    tick();
    act(() => t.publish());
    expect(screen.getByRole("checkbox")).not.toBeDisabled();
  });
});
