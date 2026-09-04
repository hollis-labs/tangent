import { createInterface } from "node:readline";

import {
  createRoomLifecycle,
  type ActiveRoomEnvelope,
  type RoomLifecycle,
} from "../ui/src/lib/room-lifecycle";
import { connect, type WSClient } from "../ui/src/lib/ws-client";

type BrowserSession = {
  name: string;
  roomID: string;
  status: string;
  pending: ActiveRoomEnvelope | null;
  client: WSClient;
  lifecycle: RoomLifecycle;
};

type DriverCommand = {
  command: string;
  browser?: string;
  roomID?: string;
  wsURL?: string;
  marker?: string;
  revision?: number;
};

const sessions = new Map<string, BrowserSession>();

function emit(event: Record<string, unknown>) {
  process.stdout.write(`${JSON.stringify(event)}\n`);
}

function state(session: BrowserSession) {
  return {
    browser: session.name,
    roomID: session.lifecycle.currentRoomID(),
    status: session.status,
    envelopeID: session.lifecycle.activeEnvelope()?.envelopeId ?? null,
    revision: session.lifecycle.activeEnvelope()?.revision ?? null,
  };
}

function requireSession(name: string | undefined): BrowserSession {
  if (!name) {
    throw new Error("browser is required");
  }
  const session = sessions.get(name);
  if (!session) {
    throw new Error(`unknown browser ${name}`);
  }
  return session;
}

function openBrowser(command: DriverCommand) {
  if (!command.browser || !command.roomID || !command.wsURL) {
    throw new Error("open requires browser, roomID, and wsURL");
  }
  if (sessions.has(command.browser)) {
    throw new Error(`browser ${command.browser} already exists`);
  }

  let session: BrowserSession;
  const client = connect(command.roomID, {
    wsURL: command.wsURL,
    onOpen: () => {
      session.status = "connected";
      emit({ event: "opened", ...state(session) });
    },
    onEnvelope: (envelopeId, envelope, revision) => {
      session.lifecycle.receiveEnvelope(envelopeId, envelope, revision);
      session.pending = { envelopeId, envelope, revision };
      session.status = "envelope received";
      emit({ event: "envelope", ...state(session), revision, envelopeType: readEnvelopeType(envelope) });
    },
    onClose: (reason) => {
      session.lifecycle.clearEnvelope();
      session.pending = null;
      session.status = `disconnected: ${reason}`;
      emit({ event: "closed", ...state(session), reason });
    },
    onError: (error) => {
      emit({ event: "browser-error", browser: command.browser, message: error.message });
    },
  });
  const lifecycle = createRoomLifecycle(command.roomID, client);
  session = {
    name: command.browser,
    roomID: command.roomID,
    status: "connecting...",
    pending: null,
    client,
    lifecycle,
  };
  sessions.set(command.browser, session);
  emit({ event: "open-started", ...state(session) });
}

function handle(command: DriverCommand) {
  switch (command.command) {
    case "open":
      openBrowser(command);
      return;
    case "switch": {
      const session = requireSession(command.browser);
      if (!command.roomID) {
        throw new Error("switch requires roomID");
      }
      const switched = session.lifecycle.switchRoom(command.roomID);
      if (switched) {
        session.roomID = command.roomID;
        session.pending = null;
        session.status = "switching rooms...";
      }
      emit({ event: "switched", ...state(session), switched });
      return;
    }
    case "submit": {
      const session = requireSession(command.browser);
      if (!command.marker) {
        throw new Error("submit requires marker");
      }
      const submitted = session.lifecycle.submit({
        v: 1,
        envelopeId: session.pending?.envelopeId,
        kind: "data",
        status: "submitted",
        payload: { browser: command.marker },
      });
      if (submitted) {
        session.pending = null;
      }
      session.status = submitted ? "response submitted" : session.status;
      emit({ event: "submitted", ...state(session), submitted });
      return;
    }
    case "submit-revision": {
      const session = requireSession(command.browser);
      if (!command.marker || command.revision === undefined || !session.pending) {
        throw new Error("submit-revision requires marker, revision, and an active envelope");
      }
      const sent = session.client.submitResponse(
        session.pending.envelopeId,
        {
          v: 1,
          envelopeId: session.pending.envelopeId,
          kind: "data",
          status: "submitted",
          payload: { browser: command.marker },
        },
        command.revision,
      );
      if (!sent) {
        throw new Error("submit-revision could not send on the active socket");
      }
      emit({ event: "revision-submitted", ...state(session), submittedRevision: command.revision });
      return;
    }
    case "cancel": {
      const session = requireSession(command.browser);
      const cancelled = session.lifecycle.cancel();
      if (cancelled) {
        session.pending = null;
      }
      session.status = cancelled ? "cancelled" : session.status;
      emit({ event: "cancelled", ...state(session), cancelled });
      return;
    }
    case "unload": {
      const session = requireSession(command.browser);
      session.pending = session.lifecycle.activeEnvelope();
      session.lifecycle.dispose();
      session.pending = null;
      session.status = "unloading";
      emit({ event: "unloaded", ...state(session) });
      return;
    }
    case "abort-process":
      // Force the Node process down without lifecycle disposal or a WebSocket
      // close handshake. The Go regression uses this to model abrupt browser
      // or transport loss, then starts a fresh production client process.
      process.exit(86);
    case "dispose": {
      const session = requireSession(command.browser);
      session.lifecycle.dispose();
      session.pending = null;
      session.status = "disposed";
      sessions.delete(session.name);
      emit({ event: "disposed", browser: session.name, roomID: session.roomID });
      return;
    }
    case "state": {
      const session = requireSession(command.browser);
      emit({ event: "state", ...state(session) });
      return;
    }
    case "shutdown":
      for (const session of sessions.values()) {
        session.lifecycle.dispose();
      }
      sessions.clear();
      emit({ event: "shutdown" });
      process.exitCode = 0;
      return;
    default:
      throw new Error(`unknown command ${command.command}`);
  }
}

function readEnvelopeType(envelope: unknown): string | null {
  if (!envelope || typeof envelope !== "object") {
    return null;
  }
  const type = (envelope as { type?: unknown }).type;
  return typeof type === "string" ? type : null;
}

const input = createInterface({ input: process.stdin, crlfDelay: Number.POSITIVE_INFINITY });
input.on("line", (line) => {
  try {
    handle(JSON.parse(line) as DriverCommand);
  } catch (error) {
    emit({ event: "driver-error", message: error instanceof Error ? error.message : String(error) });
  }
});
input.on("close", () => {
  for (const session of sessions.values()) {
    session.lifecycle.dispose();
  }
});

emit({ event: "ready" });
