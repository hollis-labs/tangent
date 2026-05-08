#!/usr/bin/env node
// triage-mock-call.mjs — outside-the-process round-trip smoke for
// tangent.triage. Starts ./tangent as a subprocess, fires a
// tangent.triage MCP call over the Streamable-HTTP transport, then
// connects to the WS bridge for the assigned room and submits a
// canned response. Verifies the MCP call returns kind=data,
// status=submitted with the expected payload.
//
// This is the Node analog of the Go in-process integration test
// (internal/server/integration_test.go::TestIntegration_TriageRoundTrip).
// Use it when you want to verify the wire shape from outside the
// process without booting a real Claude Code session.
//
// Usage:
//   make build
//   node scripts/triage-mock-call.mjs
//
// Exits 0 on success, non-zero with a descriptive message otherwise.

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
// Use a non-default port so the script doesn't clash with a Tangent
// the developer left running in another terminal.
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7843", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log(`[mock]`, ...args);
const fail = (msg) => {
  console.error(`[mock] FAIL: ${msg}`);
  process.exitCode = 1;
};

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

  const procExit = new Promise((resolve, reject) => {
    proc.once("exit", (code, signal) => {
      resolve({ code, signal });
    });
    proc.once("error", reject);
  });

  try {
    await waitForServer(MCP_URL, 5000);
    log(`server up on ${BASE}`);

    const envelopeId = `mock-${Date.now()}`;
    const envelope = {
      v: 1,
      id: envelopeId,
      type: "tangent.triage",
      title: "Mock smoke envelope",
      data: {
        prompt: "Mock-driven round-trip from triage-mock-call.mjs.",
        items: ["alpha", "beta", { id: "gamma-id", title: "gamma" }],
      },
    };

    const findRoom = waitForRoomURL(stderrBuf, envelopeId, 5000);
    const callPromise = callTriage(envelope);

    const roomURL = await findRoom;
    log(`room URL: ${roomURL}`);
    const roomID = roomURL.split("/r/")[1];
    if (!roomID) throw new Error(`bad room URL ${roomURL}`);

    await drivBrowser(roomID, envelopeId);

    const result = await callPromise;
    verifyResult(result, envelopeId);
    log("round-trip OK");
  } finally {
    proc.kill("SIGINT");
    // Give the server up to 2s to drain.
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function waitForServer(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 0, method: "tools/list" }),
      });
      if (r.status >= 200 && r.status < 500) return;
    } catch {
      // server not yet listening
    }
    await sleep(100);
  }
  throw new Error(`server did not become reachable within ${timeoutMs}ms`);
}

function waitForRoomURL(stderrBuf, envelopeId, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return (async () => {
    while (Date.now() < deadline) {
      const joined = stderrBuf.join("");
      // Match log line: triage room created room=<roomID> url=<base>/r/<roomID> envelope=<id>
      const match = joined.match(
        new RegExp(`triage room created.*?url=([^\\s]+/r/[A-Za-z0-9_-]+).*?envelope=${escapeRegExp(envelopeId)}`),
      );
      if (match) return match[1];
      await sleep(50);
    }
    throw new Error(`room URL log line not found within ${timeoutMs}ms`);
  })();
}

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function callTriage(envelope) {
  // The streamable-HTTP transport in Tangent v0.1 runs in
  // stateless+JSONResponse mode (see internal/mcp/server.go), so a
  // one-shot tools/call POST returns the JSON-RPC response directly
  // — no session init handshake required.
  const body = {
    jsonrpc: "2.0",
    id: 1,
    method: "tools/call",
    params: { name: "tangent.triage", arguments: { envelope } },
  };
  const r = await fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const text = await r.text();
    throw new Error(`MCP HTTP ${r.status}: ${text}`);
  }
  const ct = r.headers.get("content-type") ?? "";
  if (ct.includes("text/event-stream")) {
    return parseSSE(await r.text());
  }
  return await r.json();
}

function parseSSE(text) {
  // Find the first "data:" line and parse it as JSON.
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith("data:")) {
      return JSON.parse(line.slice(5).trim());
    }
  }
  throw new Error(`no data line in SSE response: ${text.slice(0, 200)}`);
}

async function drivBrowser(roomID, envelopeId) {
  const url = `${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`;
  const ws = new WebSocket(url);
  await once(ws, "open");

  const frame = await once(ws, "message");
  const msg = JSON.parse(frame.data);
  if (msg.type !== "envelope") throw new Error(`expected envelope frame, got ${JSON.stringify(msg)}`);
  if (msg.envelopeId !== envelopeId) {
    throw new Error(`envelopeId mismatch: got ${msg.envelopeId}, want ${envelopeId}`);
  }

  const response = {
    v: 1,
    envelopeId,
    kind: "data",
    status: "submitted",
    payload: {
      decisions: [
        { itemId: "item-0", action: "accept" },
        { itemId: "item-1", action: "backlog" },
        { itemId: "gamma-id", action: "delete" },
      ],
    },
    completedAt: new Date().toISOString(),
  };
  ws.send(JSON.stringify({ type: "response", envelopeId, response }));

  // Give the server a beat to consume the response, then close.
  await sleep(50);
  ws.close(1000, "mock done");
}

function once(target, event) {
  return new Promise((resolve, reject) => {
    const onEvt = (ev) => {
      target.removeEventListener("error", onErr);
      resolve(ev);
    };
    const onErr = (err) => {
      target.removeEventListener(event, onEvt);
      reject(err instanceof Error ? err : new Error(String(err)));
    };
    target.addEventListener(event, onEvt, { once: true });
    target.addEventListener("error", onErr, { once: true });
  });
}

function verifyResult(result, envelopeId) {
  if (result.error) throw new Error(`MCP error: ${JSON.stringify(result.error)}`);
  const content = result?.result?.content;
  if (!Array.isArray(content) || content.length === 0) {
    throw new Error(`unexpected MCP result: ${JSON.stringify(result)}`);
  }
  const text = content[0].text ?? "";
  const env = JSON.parse(text);
  if (env.envelopeId !== envelopeId) {
    throw new Error(`response envelopeId = ${env.envelopeId}, want ${envelopeId}`);
  }
  if (env.kind !== "data" || env.status !== "submitted") {
    throw new Error(`response kind/status: ${env.kind}/${env.status}, want data/submitted`);
  }
  const decisions = env.payload?.decisions;
  if (!Array.isArray(decisions) || decisions.length !== 3) {
    throw new Error(`decisions length ${decisions?.length}, want 3`);
  }
  const ids = decisions.map((d) => d.itemId).join(",");
  if (ids !== "item-0,item-1,gamma-id") {
    throw new Error(`decision itemIds = ${ids}, want item-0,item-1,gamma-id`);
  }
  log(`response validated: 3 decisions, envelopeId=${env.envelopeId}`);
}

main().catch((err) => {
  fail(err.stack ?? err.message ?? String(err));
});
