#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7849", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[block-draft-mock]", ...args);

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

    await runOutlineFirstPath();
    await runSkipOutlinePath();
    log("outline-first and skip-outline draft paths OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function runOutlineFirstPath() {
  const roomID = await createRoom("outline-first-block-draft");
  const ws = await openRoomSocket(roomID);

  await seedSynthesis(roomID, "present");
  await callSessionAdvancePhase(roomID, "drafting");

  await expectBlockDraft(ws, callBlockDraft(roomID, {
    envelopeID: `draft-outline-${Date.now()}-1`,
    blockID: "intro",
    label: "Intro",
    content: "Outline-first intro paragraph.",
  }), {
    decision: "accept",
  });

  await expectBlockDraft(ws, callBlockDraft(roomID, {
    envelopeID: `draft-outline-${Date.now()}-2`,
    blockID: "body",
    label: "Body",
    content: "Outline-first body paragraph.",
    mode: "paragraph",
  }), {
    decision: "inline_edit",
    feedback: "Tighten the second sentence.",
    editedText: "Outline-first body paragraph, tightened.",
  });

  const state = await callSessionGet(roomID);
  if (state.accepted_draft_blocks?.length !== 2) {
    throw new Error(`expected 2 accepted blocks, got ${state.accepted_draft_blocks?.length}`);
  }
  if (state.current_draft?.block_count !== 2) {
    throw new Error(`expected current draft block_count=2, got ${state.current_draft?.block_count}`);
  }
  ws.close(1000, "done");
}

async function runSkipOutlinePath() {
  const roomID = await createRoom("skip-outline-block-draft");
  const ws = await openRoomSocket(roomID);

  await seedSynthesis(roomID, "skipped");
  await callSessionAdvancePhase(roomID, "drafting");

  await expectBlockDraft(ws, callBlockDraft(roomID, {
    envelopeID: `draft-skip-${Date.now()}-1`,
    blockID: "intro",
    label: "Intro",
    content: "Skip-outline intro paragraph.",
  }), {
    decision: "revise",
    feedback: "Make the opening less formal.",
  });

  await expectBlockDraft(ws, callBlockDraft(roomID, {
    envelopeID: `draft-skip-${Date.now()}-2`,
    blockID: "intro",
    label: "Intro",
    content: "Skip-outline intro paragraph.",
  }), {
    decision: "inline_edit",
    feedback: "Use the friendlier opening.",
    editedText: "Skip-outline intro paragraph, friendlier revision.",
  });

  const state = await callSessionGet(roomID);
  if (state.accepted_draft_blocks?.length !== 1) {
    throw new Error(`expected 1 accepted block after revise+inline edit, got ${state.accepted_draft_blocks?.length}`);
  }
  if (state.current_draft?.blocks?.[0]?.content !== "Skip-outline intro paragraph, friendlier revision.") {
    throw new Error(`unexpected current draft ${JSON.stringify(state.current_draft)}`);
  }
  ws.close(1000, "done");
}

async function seedSynthesis(roomID, outlineState) {
  const data = {
    private_notes: "Keep the draft grounded in constraints before examples.",
    summary: "Start with constraints, then add one concrete example.",
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
  const envelopeID = `synth-${outlineState}-${Date.now()}`;
  const call = callTool("tangent.synthesis_notes", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.synthesis-notes",
      title: "Synthesis handoff",
      data,
      meta: { roomID },
    },
  });
  await expectAckEnvelope(await openExistingSocket(roomID), envelopeID);
  await verifyCall(call, `seed synthesis ${outlineState}`);
}

const roomSockets = new Map();

async function openRoomSocket(roomID) {
  const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
  await once(ws, "open");
  roomSockets.set(roomID, ws);
  return ws;
}

async function openExistingSocket(roomID) {
  const ws = roomSockets.get(roomID);
  if (!ws) {
    throw new Error(`missing room socket for ${roomID}`);
  }
  return ws;
}

async function expectAckEnvelope(ws, envelopeID) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope" || message.envelopeId !== envelopeID) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(message)}`);
  }
  ws.send(
    JSON.stringify({
      type: "response",
      envelopeId: envelopeID,
      response: {
        v: 1,
        envelopeId: envelopeID,
        kind: "ack",
        status: "submitted",
        completedAt: new Date().toISOString(),
      },
    }),
  );
  await sleep(25);
}

function callBlockDraft(roomID, { envelopeID, blockID, label, content, mode = "section" }) {
  return callTool("tangent.block_draft", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.block-draft",
      title: label,
      data: {
        block_id: blockID,
        mode,
        label,
        content,
        rationale: "Lead with the clearest point first.",
        outline_hint: "Draft toward the active section.",
      },
      meta: { roomID },
    },
  });
}

async function expectBlockDraft(ws, call, responsePlan) {
  const frame = await once(ws, "message");
  const message = JSON.parse(frame.data);
  if (message.type !== "envelope") {
    throw new Error(`unexpected frame ${JSON.stringify(message)}`);
  }
  const payload = {
    decision: responsePlan.decision,
    block_id: message.envelope?.data?.block_id,
    mode: message.envelope?.data?.mode ?? "section",
  };
  if (responsePlan.feedback) {
    payload.feedback = responsePlan.feedback;
  }
  if (responsePlan.editedText) {
    payload.edited_text = responsePlan.editedText;
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
  await sleep(50);
  await verifyCall(call, message.envelopeId);
}

async function createRoom(title) {
  const result = await callTool("tangent.session_create", { title });
  const payload = JSON.parse(result.result.content[0].text);
  if (!payload.roomID) {
    throw new Error(`missing roomID in session_create: ${JSON.stringify(payload)}`);
  }
  return payload.roomID;
}

async function callSessionAdvancePhase(roomID, toPhase) {
  await callTool("tangent.session_advance_phase", { roomID, to_phase: toPhase });
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
      // ignore until deadline
    }
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
