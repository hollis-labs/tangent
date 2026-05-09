#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7845", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

async function main() {
  const proc = spawn(BINARY, [`--port=${PORT}`], {
    cwd: REPO_ROOT,
    env: { ...process.env },
    stdio: ["ignore", "inherit", "pipe"],
  });
  const stderrBuf = [];
  proc.stderr.on("data", (chunk) => {
    const text = chunk.toString();
    stderrBuf.push(text);
    process.stderr.write(text);
  });

  try {
    await waitForServer();
    const envelopeId = `form-${Date.now()}`;
    const envelope = {
      v: 1,
      id: envelopeId,
      type: "tangent.form-collect",
      title: "Mock form collect",
      data: {
        form_id: "launch",
        intent: "Collect launch facts.",
        schema: {
          fields: [
            { id: "headline", type: "text", label: "Headline", required: true },
            { id: "ship_now", type: "checkbox", label: "Ship now?" },
          ],
          sections: [
            {
              id: "assets",
              title: "Assets",
              repeatable: true,
              show_when: { field_id: "ship_now", truthy: true },
              fields: [{ id: "name", type: "text", label: "Asset name", required: true }],
            },
          ],
        },
        actions: [{ id: "ship", label: "Ship" }],
      },
    };

    const roomURLPromise = waitForRoomURL(stderrBuf, envelopeId);
    const callPromise = callTool("tangent.form-collect", { envelope });
    const roomURL = await roomURLPromise;
    const roomID = roomURL.split("/r/")[1];

    const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
    await once(ws, "open");
    const message = await once(ws, "message");
    const frame = JSON.parse(message.data);
    if (frame.envelopeId !== envelopeId) {
      throw new Error(`unexpected envelope frame ${JSON.stringify(frame)}`);
    }

    ws.send(
      JSON.stringify({
        type: "response",
        envelopeId,
        response: {
          v: 1,
          envelopeId,
          kind: "data",
          status: "submitted",
          payload: {
            form_id: "launch",
            answers: {
              headline: "Ship the launch",
              ship_now: true,
              assets: [{ name: "Hero image" }],
            },
            action_id: "ship",
            attachment_refs: [{ name: "Spec PDF", uri: "artifact://spec" }],
          },
          completedAt: new Date().toISOString(),
        },
      }),
    );

    const result = await callPromise;
    const body = JSON.parse(result.result.content[0].text);
    if (body.payload?.form_id !== "launch") {
      throw new Error(`unexpected payload ${JSON.stringify(body)}`);
    }
  } finally {
    proc.kill("SIGINT");
    await sleep(200);
  }
}

async function waitForServer() {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(MCP_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 0, method: "tools/list" }),
      });
      if (response.status >= 200 && response.status < 500) {
        return;
      }
    } catch {}
    await sleep(100);
  }
  throw new Error("server did not become reachable");
}

async function callTool(name, args) {
  const response = await fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
  });
  return response.json();
}

async function waitForRoomURL(stderrBuf, envelopeId) {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    const match = stderrBuf
      .join("")
      .match(new RegExp(`form-collect room created.*?url=([^\\s]+/r/[A-Za-z0-9_-]+).*?envelope=${escapeRegExp(envelopeId)}`));
    if (match) {
      return match[1];
    }
    await sleep(50);
  }
  throw new Error("room URL not found");
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function once(target, event) {
  return new Promise((resolve, reject) => {
    const onEvent = (payload) => {
      target.removeEventListener("error", onError);
      resolve(payload);
    };
    const onError = (error) => {
      target.removeEventListener(event, onEvent);
      reject(error instanceof Error ? error : new Error(String(error)));
    };
    target.addEventListener(event, onEvent, { once: true });
    target.addEventListener("error", onError, { once: true });
  });
}

main().catch((error) => {
  console.error(error.stack ?? error.message ?? String(error));
  process.exitCode = 1;
});
