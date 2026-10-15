import { afterEach, describe, expect, it, vi } from "vitest";
import { subscribeRevision } from "./event-hub";

class FakeSource {
  static instances: FakeSource[] = [];
  static OPEN = 1;
  readyState = 0;
  closed = false;
  listeners = new Map<string, Array<() => void>>();
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeSource.instances.push(this);
  }
  addEventListener(type: string, fn: () => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  emit(type: string) {
    if (type === "open") this.readyState = 1;
    for (const fn of this.listeners.get(type) ?? []) fn();
  }
  close() {
    this.closed = true;
  }
}

afterEach(() => {
  FakeSource.instances = [];
  vi.unstubAllGlobals();
});

describe("event hub", () => {
  it("shares one connection per URL and closes it with the last subscriber", () => {
    vi.stubGlobal("EventSource", FakeSource);
    const a = vi.fn();
    const b = vi.fn();
    const one = subscribeRevision("/api/x/events", a);
    const two = subscribeRevision("/api/x/events", b);
    expect(FakeSource.instances).toHaveLength(1);
    FakeSource.instances[0].emit("revision");
    expect(a).toHaveBeenCalledOnce();
    expect(b).toHaveBeenCalledOnce();
    one.close();
    expect(FakeSource.instances[0].closed).toBe(false);
    two.close();
    expect(FakeSource.instances[0].closed).toBe(true);
  });

  it("reports connection status", () => {
    vi.stubGlobal("EventSource", FakeSource);
    const status = vi.fn();
    const sub = subscribeRevision("/api/y/events", () => {}, status);
    expect(status).toHaveBeenLastCalledWith(false);
    FakeSource.instances[0].emit("open");
    expect(status).toHaveBeenLastCalledWith(true);
    sub.close();
  });
});
