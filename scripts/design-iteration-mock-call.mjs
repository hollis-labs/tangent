#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7846", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[design-mock]", ...args);
const fail = (msg) => {
  console.error(`[design-mock] FAIL: ${msg}`);
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
    proc.once("exit", (code, signal) => resolve({ code, signal }));
    proc.once("error", reject);
  });

  try {
    await waitForServer(MCP_URL, 5000);
    log(`server up on ${BASE}`);

    const firstEnvelopeID = `design-${Date.now()}-1`;
    const firstVariantID = "variant-1";
    const firstRoom = waitForRoomURL(stderrBuf, firstEnvelopeID, 5000);
    const firstCall = callDesignIteration(firstEnvelopeID, firstVariantID);
    const roomURL = await firstRoom;
    const roomID = roomURL.split("/r/")[1];
    if (!roomID) {
      throw new Error(`bad room URL ${roomURL}`);
    }
    log(`room URL: ${roomURL}`);

    const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
    await once(ws, "open");

    await respondToEnvelope(ws, firstEnvelopeID, firstVariantID, "hero");
    verifyResult(await firstCall, firstEnvelopeID, firstVariantID, "hero");

    for (let i = 2; i <= 3; i++) {
      const envelopeID = `design-${Date.now()}-${i}`;
      const variantID = `variant-${i}`;
      const actionID = `choice-${i}`;
      const resultPromise = callDesignIteration(envelopeID, variantID, roomID, actionID);
      await respondToEnvelope(ws, envelopeID, variantID, actionID);
      verifyResult(await resultPromise, envelopeID, variantID, actionID);
    }

    const state = await callSessionGet(roomID);
    if (!Array.isArray(state.envelopes_history) || state.envelopes_history.length !== 3) {
      throw new Error(`expected 3 history entries, got ${state.envelopes_history?.length}`);
    }
    log("3-iteration round-trip OK");
    ws.close(1000, "done");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function callDesignIteration(envelopeID, variantID, roomID = null, actionID = "hero") {
  const envelope = {
    v: 1,
    id: envelopeID,
    type: "tangent.design-iteration",
    title: `Mock ${variantID}`,
    data: {
      caption: `Iteration ${variantID}`,
      variant_id: variantID,
      html: `<main><button id="${actionID}">${actionID}</button></main>`,
      prompts: [
        {
          id: actionID,
          kind: "click-region",
          label: `Click ${actionID}`,
          selector: `#${actionID}`,
        },
      ],
    },
    ...(roomID ? { meta: { roomID } } : {}),
  };
  return callTool("tangent.design-iteration", { envelope });
}

async function callSessionGet(roomID) {
  const result = await callTool("tangent.session_get", { roomID });
  return JSON.parse(result.result.content[0].text);
}

async function respondToEnvelope(ws, envelopeID, variantID, actionID) {
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
        kind: "data",
        status: "submitted",
        payload: {
          variant_id: variantID,
          action_id: actionID,
          action_kind: "click-region",
          value: actionID,
        },
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

function verifyResult(result, envelopeID, variantID, actionID) {
  if (result.error) {
    throw new Error(`MCP error: ${JSON.stringify(result.error)}`);
  }
  const content = result?.result?.content;
  if (!Array.isArray(content) || content.length === 0) {
    throw new Error(`unexpected MCP result: ${JSON.stringify(result)}`);
  }
  const response = JSON.parse(content[0].text ?? "");
  if (response.envelopeId !== envelopeID) {
    throw new Error(`response envelopeId = ${response.envelopeId}, want ${envelopeID}`);
  }
  if (response.payload?.variant_id !== variantID || response.payload?.action_id !== actionID) {
    throw new Error(`unexpected response payload ${JSON.stringify(response.payload)}`);
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
    } catch {}
    await sleep(100);
  }
  throw new Error(`server did not become reachable within ${timeoutMs}ms`);
}

function waitForRoomURL(stderrBuf, envelopeID, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return (async () => {
    while (Date.now() < deadline) {
      const joined = stderrBuf.join("");
      const match = joined.match(
        new RegExp(`design-iteration room created.*?url=([^\\s]+/r/[A-Za-z0-9_-]+).*?envelope=${escapeRegExp(envelopeID)}`),
      );
      if (match) return match[1];
      await sleep(50);
    }
    throw new Error(`room URL log line not found within ${timeoutMs}ms`);
  })();
}

function parseSSE(text) {
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith("data:")) {
      return JSON.parse(line.slice(5).trim());
    }
  }
  throw new Error(`no data line in SSE response: ${text.slice(0, 200)}`);
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
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

main().catch((err) => {
  fail(err.stack ?? err.message ?? String(err));
});
