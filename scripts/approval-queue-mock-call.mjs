#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7855", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

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
    await waitForServer();
    const roomID = await createRoom("approval-queue-mock");
    const ws = await openRoomSocket(roomID);

    const firstCall = callTool("tangent.approval_queue", {
      envelope: {
        v: 1,
        id: "approval-1",
        type: "tangent.approval-queue",
        data: {
          queue_id: "queue-1",
          items: [
            { id: "item-1", title: "Update dependency" },
            { id: "item-2", title: "Enable feature flag" },
          ],
        },
        meta: { roomID },
      },
    });
    await respond(ws, {
      queue_id: "queue-1",
      current_index: 1,
      notes: "first pass",
      decisions: [
        { item_id: "item-1", decision: "accept", action_id: "merge", comment: "safe" },
        { item_id: "item-2", decision: "defer", defer_reason: "window" },
      ],
      export_refs: [{ name: "queue-1-audit.json", item_count: 2, decision_count: 2 }],
    });
    await verifyCall(firstCall);

    const secondCall = callTool("tangent.approval_queue", {
      envelope: {
        v: 1,
        id: "approval-2",
        type: "tangent.approval-queue",
        data: {
          queue_id: "queue-1",
          notes: "stale note",
          items: [{ id: "item-1" }, { id: "item-2" }],
        },
        meta: { roomID },
      },
    });
    const reopened = await nextEnvelope(ws);
    if (reopened.envelope?.data?.notes !== "first pass") {
      throw new Error(`reopened notes mismatch: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    ws.send(JSON.stringify({ type: "cancel", envelopeId: reopened.envelopeId }));
    await verifyCall(secondCall);

    const state = await callSessionGet(roomID);
    if (state.approval_queue?.queue_id !== "queue-1") {
      throw new Error(`unexpected approval_queue: ${JSON.stringify(state.approval_queue)}`);
    }
    console.log("[approval-queue-mock] submit -> reopen -> cancel loop OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function respond(ws, payload) {
  const message = await nextEnvelope(ws);
  ws.send(JSON.stringify({
    type: "response",
    envelopeId: message.envelopeId,
    response: { v: 1, envelopeId: message.envelopeId, kind: "data", status: "submitted", payload },
  }));
  await sleep(25);
}

async function waitForServer() {
  const started = Date.now();
  while (Date.now() - started < 5000) {
    try {
      const response = await fetch(MCP_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
      });
      if (response.ok) return;
    } catch {}
    await sleep(100);
  }
  throw new Error("server did not become ready");
}

async function createRoom(title) {
  const result = await callTool("tangent.session_create", { title });
  return JSON.parse(result.result.content[0].text).roomID;
}

async function callSessionGet(roomID) {
  const result = await callTool("tangent.session_get", { roomID });
  return JSON.parse(result.result.content[0].text);
}

async function callTool(name, args) {
  const response = await fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
  });
  return response.json();
}

async function verifyCall(promise) {
  const result = await promise;
  if (result.error) throw new Error(JSON.stringify(result.error));
  return result;
}

async function openRoomSocket(roomID) {
  const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", reject, { once: true });
  });
  return ws;
}

async function nextEnvelope(ws) {
  while (true) {
    const message = await new Promise((resolve, reject) => {
      ws.addEventListener("message", (event) => resolve(JSON.parse(event.data)), { once: true });
      ws.addEventListener("error", reject, { once: true });
    });
    if (message?.type === "envelope" && message?.envelope?.type === "tangent.approval-queue") {
      return message;
    }
  }
}

await main();
