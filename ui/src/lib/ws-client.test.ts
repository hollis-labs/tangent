import { describe, expect, it, vi } from "vitest";

import { connect, getTabClientID, getTabClientKind, seedShellClientIdentity } from "./ws-client";

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
        clientID: "tab-1",
        heartbeatMs: 0,
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
        clientID: "tab-1",
        heartbeatMs: 0,
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
  it("carries the tab client id and reports connection, sync, and refusal frames", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);

    const onConnectionState = vi.fn();
    const onSync = vi.fn();
    const onServerError = vi.fn();
    const onError = vi.fn();

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-7",
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
        onConnectionState,
        onSync,
        onServerError,
        onError,
      });
      const socket = MockWebSocket.instances[0];
      // The client id is what tells the server a reconnect is a refresh of
      // this tab rather than a second tab.
      expect(socket.url).toContain("clientID=tab-7");
      expect(client.clientID()).toBe("tab-7");
      socket.emitOpen();

      socket.emitMessage(
        JSON.stringify({
          type: "connection",
          connectionId: "conn-1",
          role: "observer",
          roomId: "room-a",
          lease: { connection_id: "conn-2", label: "tab a" },
          connections: [
            { connection_id: "conn-1", role: "observer", self: true },
            { connection_id: "conn-2", role: "resolver", label: "tab a" },
          ],
        }),
      );
      expect(onConnectionState).toHaveBeenCalledWith(
        expect.objectContaining({
          connectionId: "conn-1",
          role: "observer",
          lease: expect.objectContaining({ connection_id: "conn-2" }),
        }),
      );

      socket.emitMessage(
        JSON.stringify({
          type: "sync",
          sync: {
            room_id: "room-a",
            surface_revision: 12,
            presentations: [
              {
                envelope_id: "env-1",
                durable: true,
                interaction_id: "int-1",
                interaction_revision: 3,
              },
            ],
          },
        }),
      );
      expect(onSync).toHaveBeenCalledWith(expect.objectContaining({ surface_revision: 12 }));

      socket.emitMessage(
        JSON.stringify({
          type: "error",
          code: "resolver_lease_held",
          message: "held by tab a",
          envelopeId: "env-1",
          lease: { connection_id: "conn-2", label: "tab a" },
        }),
      );
      expect(onServerError).toHaveBeenCalledWith(
        expect.objectContaining({ code: "resolver_lease_held", envelopeId: "env-1" }),
      );

      expect(client.claimResolver(true)).toBe(true);
      expect(client.releaseResolver()).toBe(true);
      expect(client.resync()).toBe(true);
      expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
        { type: "claim_resolver", takeover: true },
        { type: "release_resolver" },
        { type: "resync" },
      ]);

      // A frame type this build does not know about is a forward-compatible
      // server, not a protocol fault.
      socket.emitMessage(JSON.stringify({ type: "future-frame", whatever: 1 }));
      expect(onError).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });

  it("attaches as an observer when asked", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);
    try {
      connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-9",
        observer: true,
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
      });
      expect(MockWebSocket.instances[0].url).toContain("role=observer");
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
});

describe("ws-client draft revisions", () => {
  it("advances the draft sequence per envelope so a caller never supplies one", () => {
    const originalWS = globalThis.WebSocket;
    Object.assign(MockWebSocket, {
      OPEN: 1,
      CONNECTING: 0,
      CLOSING: 2,
      CLOSED: 3,
      instances: [],
    });
    vi.stubGlobal("WebSocket", MockWebSocket);

    try {
      const client = connect("room-a", {
        wsURL: "ws://example.test/ws",
        clientID: "tab-1",
        heartbeatMs: 0,
        onEnvelope: vi.fn(),
      });
      const socket = MockWebSocket.instances[0];
      socket.emitOpen();
      socket.sent.length = 0;

      client.saveDraft("board-1", { filters: ["a"] });
      client.saveDraft("board-1", { filters: ["a", "b"] });
      // A second envelope keeps its own sequence — the store counts per
      // interaction, so a shared counter would conflict on the first save.
      client.saveDraft("board-2", { filters: [] });

      const drafts = socket.sent
        .map((raw: string) => JSON.parse(raw))
        .filter((frame: { type: string }) => frame.type === "draft");

      expect(drafts).toHaveLength(3);
      expect(drafts[0]).toMatchObject({ envelopeId: "board-1", draftRevision: 1 });
      expect(drafts[1]).toMatchObject({ envelopeId: "board-1", draftRevision: 2 });
      expect(drafts[2]).toMatchObject({ envelopeId: "board-2", draftRevision: 1 });
      expect(drafts[0].draft).toEqual({ filters: ["a"] });
    } finally {
      vi.unstubAllGlobals();
      globalThis.WebSocket = originalWS;
    }
  });
});

describe("shell client identity", () => {
  const ID_KEY = "tangent:v2:room:client-id";
  const KIND_KEY = "tangent:v2:room:client-kind";

  function withURL(path: string, run: () => void) {
    const original = window.location.href;
    window.history.replaceState(null, "", path);
    try {
      run();
    } finally {
      window.history.replaceState(null, "", original);
    }
  }

  it("survives the first in-app navigation when seeded at boot", () => {
    window.sessionStorage.clear();
    // The shell opens the window with the params on the root URL...
    withURL("/?clientId=shell-abc&clientKind=desktop", () => {
      seedShellClientIdentity();
    });
    // ...and React Router's first pushState drops the query string before
    // connect() ever runs on the room route.
    withURL("/r/room-1", () => {
      expect(getTabClientID()).toBe("shell-abc");
      expect(getTabClientKind()).toBe("desktop");
    });
  });

  it("is a no-op for a plain browser tab and still mints a tab id", () => {
    window.sessionStorage.clear();
    withURL("/", () => {
      seedShellClientIdentity();
      expect(window.sessionStorage.getItem(ID_KEY)).toBeNull();
      expect(window.sessionStorage.getItem(KIND_KEY)).toBeNull();
      const id = getTabClientID();
      expect(id.startsWith("room-tab-")).toBe(true);
      expect(getTabClientID()).toBe(id);
      expect(getTabClientKind()).toBe("");
    });
  });

  it("lets an explicit clientID option win over the seeded slot", () => {
    window.sessionStorage.clear();
    withURL("/?clientId=shell-abc&clientKind=desktop", () => {
      seedShellClientIdentity();
    });
    Object.assign(MockWebSocket, { instances: [] });
    vi.stubGlobal("WebSocket", MockWebSocket);
    withURL("/r/room-1", () => {
      const client = connect("room-1", {
        wsURL: "ws://example.test/ws",
        clientID: "explicit-id",
        onOpen: vi.fn(),
        onClose: vi.fn(),
        onEnvelope: vi.fn(),
        onError: vi.fn(),
      });
      const url = new URL(MockWebSocket.instances[0].url);
      expect(url.searchParams.get("clientID")).toBe("explicit-id");
      expect(url.searchParams.get("clientKind")).toBe("desktop");
      client.close();
    });
    vi.unstubAllGlobals();
  });
});
