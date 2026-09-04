import { describe, expect, it, vi } from "vitest";

import { connect } from "./ws-client";

class MockWebSocket extends EventTarget {
  static instances: MockWebSocket[] = [];
  static OPEN = 1;
  static CONNECTING = 0;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  url: string;

  constructor(url: string) {
    super();
    this.url = url;
    MockWebSocket.instances.push(this);
  }

  send(payload: string) {
    this.sent.push(payload);
  }

  close(_code?: number, _reason?: string) {
    this.readyState = MockWebSocket.CLOSED;
  }

  emitOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.dispatchEvent(new Event("open"));
  }

  emitMessage(data: string) {
    this.dispatchEvent(new MessageEvent("message", { data }));
  }

  emitClose(code = 1000, reason = "") {
    this.readyState = MockWebSocket.CLOSED;
    this.dispatchEvent(new CloseEvent("close", { code, reason }));
  }
}

describe("ws-client", () => {
  it("ignores stale socket events after switchRoom", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, {
      OPEN: 1,
      CONNECTING: 0,
      CLOSING: 2,
      CLOSED: 3,
      instances: [],
    });
    vi.stubGlobal("WebSocket", MockWebSocket);

    const onOpen = vi.fn();
    const onClose = vi.fn();
    const onEnvelope = vi.fn();
    const onError = vi.fn();

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        onOpen,
        onClose,
        onEnvelope,
        onError,
      });

      const first = MockWebSocket.instances[0];
      expect(first.url).toContain("roomID=room-a");

      client.switchRoom("room-b");

      const second = MockWebSocket.instances[1];
      expect(second.url).toContain("roomID=room-b");

      first.emitOpen();
      first.emitMessage(
        JSON.stringify({
          type: "envelope",
          envelopeId: "old-env",
          revision: 1,
          envelope: { stale: true },
        }),
      );
      first.emitClose(1006, "stale");

      expect(onOpen).not.toHaveBeenCalled();
      expect(onEnvelope).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();

      second.emitOpen();
      second.emitMessage(
        JSON.stringify({
          type: "envelope",
          envelopeId: "new-env",
          revision: 2,
          envelope: { fresh: true },
        }),
      );
      second.emitClose(1000, "done");

      expect(onOpen).toHaveBeenCalledTimes(1);
      expect(onEnvelope).toHaveBeenCalledWith("new-env", { fresh: true }, 2);
      expect(onClose).toHaveBeenCalledWith("done");
      expect(onError).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("echoes the presentation revision on response and cancel frames", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        onEnvelope: vi.fn(),
      });
      const socket = MockWebSocket.instances[0];
      socket.emitOpen();

      expect(client.submitResponse("env-1", { ok: true }, 7)).toBe(true);
      expect(client.cancel("env-2", 8)).toBe(true);

      expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
        { type: "response", envelopeId: "env-1", revision: 7, response: { ok: true } },
        { type: "cancel", envelopeId: "env-2", revision: 8 },
      ]);

      socket.readyState = MockWebSocket.CLOSING;
      expect(client.cancel("env-3", 9)).toBe(false);
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
});
