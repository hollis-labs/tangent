#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7853", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[whiteboard-mock]", ...args);

async function main() {
  const proc = spawn(BINARY, [`--port=${PORT}`], {
    cwd: REPO_ROOT,
    env: { ...process.env },
    stdio: ["ignore", "inherit", "pipe"],
  });
  proc.stderr.on("data", (chunk) => process.stderr.write(chunk.toString()));

  const procExit = new Promise((resolve, reject) => {
    proc.once("exit", (code, signal) => resolve({ code, signal }));
    proc.once("error", reject);
  });

  try {
    await waitForServer(MCP_URL, 5000);
    const roomID = await createRoom("whiteboard-submit-contract");
    const ws = await openRoomSocket(roomID);

    const firstCall = callTool("tangent.whiteboard", {
      envelope: {
        v: 1,
        id: "whiteboard-1",
        type: "tangent.whiteboard",
        title: "Board pass 1",
        data: {
          board_id: "board-1",
          title: "Whiteboard",
          intent: "Lay out the initial structure.",
          scene: {
            document: {
              pages: [{ id: "page:1" }],
            },
          },
          notes: "seed notes",
        },
        meta: { roomID },
      },
    });
    await respondToWhiteboard(ws, {
      board_id: "board-1",
      scene: {
        document: {
          pages: [{ id: "page:1" }, { id: "shape:1", type: "geo" }],
        },
      },
      notes: "revision one",
      selection_summary: {
        count: 1,
        ids: ["shape:1"],
        types: ["geo"],
      },
    });
    const firstResult = parseToolEnvelope(await verifyCall(firstCall, "whiteboard pass 1"));
    if (!firstResult.payload?.revision_id) {
      throw new Error(`first result missing revision_id: ${JSON.stringify(firstResult)}`);
    }

    const secondCall = callTool("tangent.whiteboard", {
      envelope: {
        v: 1,
        id: "whiteboard-2",
        type: "tangent.whiteboard",
        title: "Board pass 2",
        data: {
          board_id: "board-1",
          title: "Whiteboard",
          intent: "Continue from the saved room board.",
          scene: {
            document: {
              pages: [{ id: "page:stale" }],
            },
          },
          notes: "stale notes",
        },
        meta: { roomID },
      },
    });
    const reopened = await nextEnvelope(ws, "tangent.whiteboard");
    const reopenedPages = reopened.envelope?.data?.scene?.document?.pages ?? [];
    if (reopenedPages.length !== 2) {
      throw new Error(`reopened scene did not reuse latest snapshot: ${JSON.stringify(reopened.envelope)}`);
    }
    if (reopened.envelope?.data?.notes !== "revision one") {
      throw new Error(`reopened notes were not persisted: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    if (reopened.envelope?.data?.revision_id !== firstResult.payload.revision_id) {
      throw new Error(`reopened revision_id mismatch: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    ws.send(
      JSON.stringify({
        type: "response",
        envelopeId: reopened.envelopeId,
        response: {
          v: 1,
          envelopeId: reopened.envelopeId,
          kind: "data",
          status: "submitted",
          payload: {
            board_id: "board-1",
            scene: {
              document: {
                pages: [
                  { id: "page:1" },
                  { id: "shape:1", type: "geo" },
                  { id: "shape:2", type: "arrow" },
                ],
              },
            },
            notes: "revision two",
            export_refs: [
              {
                artifact_id: "artifact-export-1",
                kind: "png",
                mime_type: "image/png",
                uri: "artifact://artifact-export-1",
              },
            ],
          },
        },
      }),
    );
    const secondResult = parseToolEnvelope(await verifyCall(secondCall, "whiteboard pass 2"));
    if (!secondResult.payload?.revision_id || secondResult.payload.revision_id === firstResult.payload.revision_id) {
      throw new Error(`second result missing new revision_id: ${JSON.stringify(secondResult)}`);
    }

    const state = await callSessionGet(roomID);
    if (state.whiteboard?.notes !== "revision two") {
      throw new Error(`unexpected latest whiteboard notes: ${JSON.stringify(state.whiteboard)}`);
    }
    if ((state.whiteboard?.revision_history ?? []).length !== 2) {
      throw new Error(`unexpected revision history: ${JSON.stringify(state.whiteboard)}`);
    }
    if ((state.envelopes_history ?? []).length !== 2) {
      throw new Error(`unexpected envelopes history: ${JSON.stringify(state.envelopes_history)}`);
    }

    log("submit -> reopen -> submit revision loop OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function respondToWhiteboard(ws, payload) {
  const message = await nextEnvelope(ws, "tangent.whiteboard");
  ws.send(
    JSON.stringify({
      type: "response",
      envelopeId: message.envelopeId,
      response: {
        v: 1,
        envelopeId: message.envelopeId,
        kind: "data",
        status: "submitted",
        payload,
      },
    }),
  );
  await sleep(25);
}

async function createRoom(title) {
  const result = await callTool("tangent.session_create", { title });
  const payload = JSON.parse(result.result.content[0].text);
  return payload.roomID;
}

async function callSessionGet(roomID) {
  const result = await callTool("tangent.session_get", { roomID });
  return JSON.parse(result.result.content[0].text);
}

async function callTool(name, args) {
  const r = await fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name, arguments: args },
    }),
  });
  if (!r.ok) {
    throw new Error(`MCP HTTP ${r.status}: ${await r.text()}`);
  }
  const contentType = r.headers.get("content-type") ?? "";
  if (contentType.includes("text/event-stream")) {
    return parseSSE(await r.text());
  }
  return r.json();
}

async function openRoomSocket(roomID) {
  const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
  await once(ws, "open");
  return ws;
}

async function nextEnvelope(ws, type) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope" || message.envelope?.type !== type) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(message)}`);
  }
  return message;
}

async function verifyCall(promise, label) {
  const result = await promise;
  if (result.error) {
    throw new Error(`${label}: MCP error ${JSON.stringify(result.error)}`);
  }
  if (result?.result?.isError) {
    throw new Error(`${label}: tool returned IsError=true: ${result.result.content?.[0]?.text ?? ""}`);
  }
  return result;
}

function parseToolEnvelope(result) {
  return JSON.parse(result.result.content[0].text);
}

async function waitForServer(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          jsonrpc: "2.0",
          id: 1,
          method: "tools/list",
          params: {},
        }),
      });
      if (r.ok) {
        return;
      }
    } catch {}
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${url}`);
}

function parseSSE(raw) {
  const dataLine = raw
    .split("\n")
    .map((line) => line.trim())
    .find((line) => line.startsWith("data:"));
  if (!dataLine) {
    throw new Error(`missing SSE data line in ${raw}`);
  }
  return JSON.parse(dataLine.slice("data:".length).trim());
}

function once(target, event) {
  return new Promise((resolve, reject) => {
    const onEvent = (value) => {
      cleanup();
      resolve(value);
    };
    const onError = (error) => {
      cleanup();
      reject(error);
    };
    const cleanup = () => {
      target.removeEventListener?.(event, onEvent);
      target.removeEventListener?.("error", onError);
      target.off?.(event, onEvent);
      target.off?.("error", onError);
    };
    target.addEventListener?.(event, onEvent, { once: true });
    target.addEventListener?.("error", onError, { once: true });
    target.once?.(event, onEvent);
    target.once?.("error", onError);
  });
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
