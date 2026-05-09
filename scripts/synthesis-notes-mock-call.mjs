#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7848", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[synthesis-mock]", ...args);

async function main() {
  const proc = spawn(BINARY, [`--port=${PORT}`], {
    cwd: REPO_ROOT,
    env: { ...process.env },
    stdio: ["ignore", "inherit", "pipe"],
  });

  proc.stderr.on("data", (chunk) => {
    process.stderr.write(chunk.toString());
  });

  const procExit = new Promise((resolve, reject) => {
    proc.once("exit", (code, signal) => resolve({ code, signal }));
    proc.once("error", reject);
  });

  try {
    await waitForServer(MCP_URL, 5000);
    log(`server up on ${BASE}`);

    await runOutlinePath();
    await runSkipPath();
    log("outline and skip paths OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function runOutlinePath() {
  const roomID = await createRoom("outline-path");
  const ws = await openRoomSocket(roomID);

  const hiddenID = `synth-outline-${Date.now()}-1`;
  const hidden = callSynthesisNotes(roomID, hiddenID, "present");
  await expectEnvelope(ws, {
    envelopeID: hiddenID,
    visibility: "hidden",
    outlineState: undefined,
    expectOutline: false,
  });
  await verifyCall(hidden, "hidden outline path");

  let state = await callSessionGet(roomID);
  if (state.synthesis_notes?.visibility !== "hidden") {
    throw new Error(`expected hidden synthesis state, got ${JSON.stringify(state.synthesis_notes)}`);
  }

  await callSessionAdvancePhase(roomID, "drafting");

  const visibleID = `synth-outline-${Date.now()}-2`;
  const visible = callSynthesisNotes(roomID, visibleID, "present");
  await expectEnvelope(ws, {
    envelopeID: visibleID,
    visibility: "visible",
    outlineState: "present",
    expectOutline: true,
  });
  await verifyCall(visible, "visible outline path");

  state = await callSessionGet(roomID);
  if (state.synthesis_notes?.visibility !== "visible") {
    throw new Error(`expected visible synthesis state, got ${JSON.stringify(state.synthesis_notes)}`);
  }
  if (state.synthesis_notes?.outline_state !== "present" || !state.synthesis_notes?.outline) {
    throw new Error(`expected visible outline payload, got ${JSON.stringify(state.synthesis_notes)}`);
  }
  ws.close(1000, "done");
}

async function runSkipPath() {
  const roomID = await createRoom("skip-path");
  const ws = await openRoomSocket(roomID);

  const hiddenID = `synth-skip-${Date.now()}-1`;
  const hidden = callSynthesisNotes(roomID, hiddenID, "skipped");
  await expectEnvelope(ws, {
    envelopeID: hiddenID,
    visibility: "hidden",
    outlineState: undefined,
    expectOutline: false,
  });
  await verifyCall(hidden, "hidden skip path");

  await callSessionAdvancePhase(roomID, "drafting");

  const visibleID = `synth-skip-${Date.now()}-2`;
  const visible = callSynthesisNotes(roomID, visibleID, "skipped");
  await expectEnvelope(ws, {
    envelopeID: visibleID,
    visibility: "visible",
    outlineState: "skipped",
    expectOutline: false,
  });
  await verifyCall(visible, "visible skip path");

  const state = await callSessionGet(roomID);
  if (state.synthesis_notes?.outline_state !== "skipped") {
    throw new Error(`expected skipped outline state, got ${JSON.stringify(state.synthesis_notes)}`);
  }
  ws.close(1000, "done");
}

async function createRoom(title) {
  const result = await callTool("tangent.session_create", { title });
  const payload = JSON.parse(result.result.content[0].text);
  if (!payload.roomID) {
    throw new Error(`missing roomID in session_create: ${JSON.stringify(payload)}`);
  }
  return payload.roomID;
}

async function openRoomSocket(roomID) {
  const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
  await once(ws, "open");
  return ws;
}

function callSynthesisNotes(roomID, envelopeID, outlineState) {
  const data = {
    private_notes: "Keep the eventual draft grounded in constraints before examples.",
    summary: "Start with constraints, then show one concrete example.",
    outline_state: outlineState,
  };
  if (outlineState === "present") {
    data.outline = {
      title: "Draft outline",
      items: [
        { label: "Goal", description: "Frame the writing target." },
        { label: "Constraints", description: "Name the key limits first." },
      ],
    };
  }
  return callTool("tangent.synthesis_notes", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.synthesis-notes",
      title: "Synthesis handoff",
      data,
      meta: { roomID },
    },
  });
}

async function callSessionAdvancePhase(roomID, toPhase) {
  await callTool("tangent.session_advance_phase", { roomID, to_phase: toPhase });
}

async function callSessionGet(roomID) {
  const result = await callTool("tangent.session_get", { roomID });
  return JSON.parse(result.result.content[0].text);
}

async function expectEnvelope(ws, expected) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope" || message.envelopeId !== expected.envelopeID) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(message)}`);
  }
  const data = message.envelope?.data ?? {};
  if (data.visibility !== expected.visibility) {
    throw new Error(`visibility = ${data.visibility}, want ${expected.visibility}`);
  }
  if ("private_notes" in data) {
    throw new Error(`private_notes leaked in frame ${JSON.stringify(data)}`);
  }
  if (expected.outlineState !== undefined && data.outline_state !== expected.outlineState) {
    throw new Error(`outline_state = ${data.outline_state}, want ${expected.outlineState}`);
  }
  if (expected.expectOutline && !data.outline) {
    throw new Error(`expected outline in frame ${JSON.stringify(data)}`);
  }
  if (!expected.expectOutline && data.outline) {
    throw new Error(`unexpected outline in frame ${JSON.stringify(data)}`);
  }
  ws.send(
    JSON.stringify({
      type: "response",
      envelopeId: expected.envelopeID,
      response: {
        v: 1,
        envelopeId: expected.envelopeID,
        kind: "ack",
        status: "submitted",
        completedAt: new Date().toISOString(),
      },
    }),
  );
  await sleep(50);
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

async function verifyCall(promise, label) {
  const result = await promise;
  if (result.error) {
    throw new Error(`${label}: MCP error ${JSON.stringify(result.error)}`);
  }
  if (result?.result?.isError) {
    throw new Error(`${label}: tool returned IsError=true`);
  }
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
    } catch {
      // retry
    }
    await sleep(100);
  }
  throw new Error(`server did not become ready at ${url}`);
}

function parseSSE(raw) {
  const lines = raw.split(/\r?\n/);
  let data = "";
  for (const line of lines) {
    if (line.startsWith("data:")) {
      data += line.slice(5).trim();
    }
  }
  return JSON.parse(data);
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

main().catch((err) => {
  console.error(`[synthesis-mock] FAIL: ${err instanceof Error ? err.message : String(err)}`);
  process.exitCode = 1;
});
