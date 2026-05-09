#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7850", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[prose-revision-mock]", ...args);

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
    const roomID = await createRoom("prose-revision-lenses");
    const ws = await openRoomSocket(roomID);

    await seedDraft(roomID, ws);
    await runLens(ws, roomID, "review", [
      { suggestion_id: "s1", decision: "accept" },
      { suggestion_id: "s2", decision: "comment", comment: "Keep one example, but shorten it." },
    ]);
    await runLens(ws, roomID, "copy", [
      { suggestion_id: "s1", decision: "accept" },
      { suggestion_id: "s2", decision: "reject" },
    ]);
    await runLens(ws, roomID, "style", [
      { suggestion_id: "s1", decision: "comment", comment: "Tone this down; keep it plainer." },
      { suggestion_id: "s2", decision: "accept" },
    ]);

    const state = await callSessionGet(roomID);
    if (state.prose_revision_outcomes?.length !== 3) {
      throw new Error(`expected 3 prose revision outcomes, got ${state.prose_revision_outcomes?.length}`);
    }
    const lenses = state.prose_revision_outcomes.map((item) => item.lens).join(",");
    if (lenses !== "review,copy,style") {
      throw new Error(`unexpected lenses ${lenses}`);
    }
    log("review/copy/style passes OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

const roomSockets = new Map();

async function seedDraft(roomID, ws) {
  const synthCall = callTool("tangent.synthesis_notes", {
    envelope: {
      v: 1,
      id: `synth-${Date.now()}`,
      type: "tangent.synthesis-notes",
      title: "Synthesis handoff",
      data: {
        private_notes: "Keep the draft direct and constraint-led.",
        summary: "Lead with the claim, then show one concrete example.",
        outline_state: "present",
        outline: {
          title: "Draft outline",
          items: [{ label: "Claim" }, { label: "Example" }],
        },
      },
      meta: { roomID },
    },
  });
  await expectAckEnvelope(ws, "tangent.synthesis-notes");
  await verifyCall(synthCall, "seed synthesis");

  await callTool("tangent.session_advance_phase", { roomID, to_phase: "drafting" });

  const draftCall = callTool("tangent.block_draft", {
    envelope: {
      v: 1,
      id: `draft-${Date.now()}`,
      type: "tangent.block-draft",
      title: "Opening block",
      data: {
        block_id: "intro",
        mode: "section",
        label: "Intro",
        content: "Original opening paragraph with too much setup and two examples.",
      },
      meta: { roomID },
    },
  });
  await expectDataEnvelope(ws, "tangent.block-draft", {
    decision: "accept",
    block_id: "intro",
    mode: "section",
  });
  await verifyCall(draftCall, "seed draft");
}

async function runLens(ws, roomID, lens, outcomes) {
  const call = callTool("tangent.prose_revision", {
    envelope: {
      v: 1,
      id: `${lens}-${Date.now()}`,
      type: "tangent.prose-revision",
      title: `${lens} pass`,
      data: {
        lens,
        revision_id: `${lens}-pass`,
        block_id: "intro",
        source_text: "Original opening paragraph with too much setup and two examples.",
        suggestions: [
          {
            id: "s1",
            label: "Lead with the claim",
            original_text: "Original opening paragraph with too much setup and two examples.",
            suggested_text: "Start with the main claim before the supporting setup.",
          },
          {
            id: "s2",
            label: "Trim the example load",
            suggested_text: "Keep one concrete example instead of two.",
          },
        ],
      },
      meta: { roomID },
    },
  });
  await expectDataEnvelope(ws, "tangent.prose-revision", {
    lens,
    revision_id: `${lens}-pass`,
    block_id: "intro",
    outcomes,
    general_comment: `${lens} pass complete`,
  });
  await verifyCall(call, `${lens} pass`);
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
  roomSockets.set(roomID, ws);
  return ws;
}

async function expectAckEnvelope(ws, type) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope" || message.envelope?.type !== type) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(message)}`);
  }
  ws.send(
    JSON.stringify({
      type: "response",
      envelopeId: message.envelopeId,
      response: {
        v: 1,
        envelopeId: message.envelopeId,
        kind: "ack",
        status: "submitted",
        completedAt: new Date().toISOString(),
      },
    }),
  );
  await sleep(25);
}

async function expectDataEnvelope(ws, type, payload) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope" || message.envelope?.type !== type) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(message)}`);
  }
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
        completedAt: new Date().toISOString(),
      },
    }),
  );
  await sleep(25);
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
    } catch {}
    await sleep(100);
  }
  throw new Error(`server did not become ready within ${timeoutMs}ms`);
}

function parseSSE(text) {
  const lines = text.split(/\r?\n/);
  const data = [];
  for (const line of lines) {
    if (line.startsWith("data:")) {
      data.push(line.slice(5).trimStart());
    }
  }
  return JSON.parse(data.join("\n"));
}

function once(target, event) {
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      target.removeEventListener(event, onEvent);
      target.removeEventListener("error", onError);
    };
    const onEvent = (value) => {
      cleanup();
      resolve(value);
    };
    const onError = (err) => {
      cleanup();
      reject(err);
    };
    target.addEventListener(event, onEvent, { once: true });
    target.addEventListener("error", onError, { once: true });
  });
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
