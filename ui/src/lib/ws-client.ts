// Typed WebSocket client for the Tangent /ws endpoint. The server
// sends an `envelope` frame per pending workflow; the client replies
// with `response` or `cancel`.
//
// Wire shape (mirrors internal/ws/handler.go):
//
//   server → client : {type:"envelope", envelopeId, envelope}
//   client → server : {type:"response", envelopeId, response}
//   client → server : {type:"cancel", envelopeId}
//
// Inbound messages are zod-validated; malformed frames are reported
// via onError instead of being silently dropped.
//
// PR 4 ships this client + a placeholder Room.tsx that mocks
// responses. PR 5 wires it to the real EnvelopeRouter component map.

import { z } from "zod";

const EnvelopeMessageSchema = z.object({
  type: z.literal("envelope"),
  envelopeId: z.string().min(1),
  envelope: z.unknown(),
});

const InboundSchema = z.discriminatedUnion("type", [EnvelopeMessageSchema]);

type InboundMessage = z.infer<typeof InboundSchema>;
type EnvelopeMessage = z.infer<typeof EnvelopeMessageSchema>;

export type WSClientOptions = {
  /**
   * Callback for inbound envelope frames. Receives the parsed
   * envelope (kept as `unknown` until PR 5 introduces the typed
   * envelope union); the caller is responsible for narrowing.
   */
  onEnvelope: (envelopeId: string, envelope: unknown) => void;

  /**
   * Called when the WebSocket closes. `reason` is best-effort — most
   * browsers redact the WS close reason for security, so callers
   * should treat it as a hint, not an authoritative signal.
   */
  onClose?: (reason: string) => void;

  /**
   * Called for transport-level errors and malformed frames. The
   * default is to log and continue.
   */
  onError?: (err: Error) => void;

  /**
   * Override the WS URL. Defaults to /ws on the current origin.
   * Useful for tests or when the SPA is served from a different
   * host than the API.
   */
  wsURL?: string;
};

export type WSClient = {
  /** True between successful open and close. */
  isConnected: () => boolean;

  /** Sends a response frame for the given envelope id. */
  submitResponse: (envelopeId: string, response: unknown) => void;

  /** Sends a cancel frame for the given envelope id. */
  cancel: (envelopeId: string) => void;

  /** Closes the underlying WebSocket cleanly. */
  close: () => void;
};

/**
 * Opens a WebSocket connection to the Tangent server for the given
 * room id. The returned client is alive until close() is called or
 * onClose fires.
 */
export function connect(roomID: string, opts: WSClientOptions): WSClient {
  if (!roomID) {
    throw new Error("ws-client: roomID is required");
  }

  const baseURL = opts.wsURL ?? defaultWSURL();
  const url = appendQuery(baseURL, "roomID", roomID);
  const ws = new WebSocket(url);
  let connected = false;

  ws.addEventListener("open", () => {
    connected = true;
  });

  ws.addEventListener("message", (ev) => {
    const raw = typeof ev.data === "string" ? ev.data : "";
    if (!raw) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      opts.onError?.(new Error(`ws-client: bad JSON: ${(err as Error).message}`));
      return;
    }
    const result = InboundSchema.safeParse(parsed);
    if (!result.success) {
      opts.onError?.(new Error(`ws-client: schema rejected frame: ${result.error.message}`));
      return;
    }
    const msg: InboundMessage = result.data;
    if (msg.type === "envelope") {
      const env = msg as EnvelopeMessage;
      opts.onEnvelope(env.envelopeId, env.envelope);
    }
  });

  ws.addEventListener("close", (ev) => {
    connected = false;
    const reason = ev.reason || `closed (code=${ev.code})`;
    opts.onClose?.(reason);
  });

  ws.addEventListener("error", () => {
    // The browser deliberately gives us no detail on WS errors.
    // Surface a generic error so callers know to act; the close
    // event that follows carries the canonical reason.
    opts.onError?.(new Error("ws-client: transport error"));
  });

  return {
    isConnected: () => connected,
    submitResponse: (envelopeId, response) => {
      send(ws, { type: "response", envelopeId, response });
    },
    cancel: (envelopeId) => {
      send(ws, { type: "cancel", envelopeId });
    },
    close: () => {
      try {
        ws.close(1000, "client closed");
      } catch {
        // already closed — ignore
      }
    },
  };
}

function send(ws: WebSocket, frame: object): void {
  if (ws.readyState !== WebSocket.OPEN) {
    // The brief lets us drop or queue; we drop with a console warning
    // because v0.1 doesn't have a reliable backoff strategy and silent
    // queuing risks confusion. Future PRs may queue.
    console.warn("ws-client: drop frame, socket not open", frame);
    return;
  }
  ws.send(JSON.stringify(frame));
}

function defaultWSURL(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

function appendQuery(base: string, key: string, value: string): string {
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}${encodeURIComponent(key)}=${encodeURIComponent(value)}`;
}
