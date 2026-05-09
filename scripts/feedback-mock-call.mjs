#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7844", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log(`[feedback-mock]`, ...args);
const fail = (msg) => {
  console.error(`[feedback-mock] FAIL: ${msg}`);
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

    const envelopeId = `feedback-${Date.now()}`;
    const envelope = {
      v: 1,
      id: envelopeId,
      type: "tangent.feedback",
      title: "Mock feedback",
      data: {
        prompt: "Round-trip feedback mock.",
        questions: [
          {
            id: "headline",
            type: "text",
            label: "Headline",
            required: true,
          },
          {
            id: "direction",
            type: "radio",
            label: "Direction",
            required: true,
            options: [
              { value: "yes", label: "Yes" },
              { value: "no", label: "No" },
            ],
          },
          {
            id: "tags",
            type: "multiselect",
            label: "Tags",
            options: [
              { value: "ux", label: "UX" },
              { value: "bug", label: "Bug" },
            ],
          },
        ],
      },
    };

    const findRoom = waitForRoomURL(stderrBuf, envelopeId, 5000);
    const callPromise = callFeedback(envelope);
    const roomURL = await findRoom;
    log(`room URL: ${roomURL}`);
    const roomID = roomURL.split("/r/")[1];
    if (!roomID) {
      throw new Error(`bad room URL ${roomURL}`);
    }

    await driveBrowser(roomID, envelopeId);

    const result = await callPromise;
    verifyResult(result, envelopeId);
    log("round-trip OK");
  } finally {
    proc.kill("SIGINT");
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
      if (r.status >= 200 && r.status < 500) {
        return;
      }
    } catch {}
    await sleep(100);
  }
  throw new Error(`server did not become reachable within ${timeoutMs}ms`);
}

function waitForRoomURL(stderrBuf, envelopeId, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return (async () => {
    while (Date.now() < deadline) {
      const joined = stderrBuf.join("");
      const match = joined.match(
        new RegExp(`feedback room created.*?url=([^\\s]+/r/[A-Za-z0-9_-]+).*?envelope=${escapeRegExp(envelopeId)}`),
      );
      if (match) {
        return match[1];
      }
      await sleep(50);
    }
    throw new Error(`room URL log line not found within ${timeoutMs}ms`);
  })();
}

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function callFeedback(envelope) {
  const body = {
    jsonrpc: "2.0",
    id: 1,
    method: "tools/call",
    params: { name: "tangent.feedback", arguments: { envelope } },
  };
  const r = await fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify(body),
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

function parseSSE(text) {
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith("data:")) {
      return JSON.parse(line.slice(5).trim());
    }
  }
  throw new Error(`no data line in SSE response: ${text.slice(0, 200)}`);
}

async function driveBrowser(roomID, envelopeId) {
  const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
  await once(ws, "open");

  const frame = await once(ws, "message");
  const msg = JSON.parse(frame.data);
  if (msg.type !== "envelope" || msg.envelopeId !== envelopeId) {
    throw new Error(`unexpected envelope frame ${JSON.stringify(msg)}`);
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
          answers: [
            { questionId: "headline", value: "Ship it" },
            { questionId: "direction", value: "yes" },
            { questionId: "tags", value: ["ux"] },
          ],
        },
        completedAt: new Date().toISOString(),
      },
    }),
  );

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
  if (result.error) {
    throw new Error(`MCP error: ${JSON.stringify(result.error)}`);
  }
  const content = result?.result?.content;
  if (!Array.isArray(content) || content.length === 0) {
    throw new Error(`unexpected MCP result: ${JSON.stringify(result)}`);
  }
  const response = JSON.parse(content[0].text ?? "");
  if (response.envelopeId !== envelopeId) {
    throw new Error(`response envelopeId = ${response.envelopeId}, want ${envelopeId}`);
  }
  if (response.kind !== "data" || response.status !== "submitted") {
    throw new Error(`response kind/status = ${response.kind}/${response.status}`);
  }
  const answers = response.payload?.answers;
  if (!Array.isArray(answers) || answers.length !== 3) {
    throw new Error(`answers length = ${answers?.length}, want 3`);
  }
}

main().catch((err) => {
  fail(err.stack ?? err.message ?? String(err));
});
