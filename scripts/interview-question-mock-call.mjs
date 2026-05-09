#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7847", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[interview-mock]", ...args);
const fail = (msg) => {
  console.error(`[interview-mock] FAIL: ${msg}`);
  process.exitCode = 1;
};

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

    const firstEnvelopeID = `interview-${Date.now()}-1`;
    const firstCall = callInterviewQuestion(firstEnvelopeID, null, "goals", "Workflow goals");
    const roomID = await waitForInterviewRoom(5000);
    if (!roomID) {
      throw new Error("room ID not found");
    }
    const roomURL = `${BASE}/r/${roomID}`;
    log(`room URL: ${roomURL}`);

    const ws = new WebSocket(`${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`);
    await once(ws, "open");

    await respondToEnvelope(
      ws,
      firstEnvelopeID,
      "goals",
      "Workflow goals",
      "quality",
      "Prefer coherence over speed.",
      "Bullets first.",
    );
    verifyResult(await firstCall, firstEnvelopeID, "goals", "quality");

    const followUps = [
      {
        envelopeID: `interview-${Date.now()}-2`,
        threadID: "constraints",
        topicLabel: "Constraints",
        selectedChoiceID: "time",
        answerText: "Keep the protocol short enough for one pass.",
        outputShapeSignal: "",
      },
      {
        envelopeID: `interview-${Date.now()}-3`,
        threadID: "examples",
        topicLabel: "Examples",
        selectedChoiceID: "",
        answerText: "Anchor each thread with one concrete example.",
        outputShapeSignal: "Use numbered sections.",
      },
    ];

    for (const item of followUps) {
      const resultPromise = callInterviewQuestion(
        item.envelopeID,
        roomID,
        item.threadID,
        item.topicLabel,
      );
      await respondToEnvelope(
        ws,
        item.envelopeID,
        item.threadID,
        item.topicLabel,
        item.selectedChoiceID,
        item.answerText,
        item.outputShapeSignal,
      );
      verifyResult(await resultPromise, item.envelopeID, item.threadID, item.selectedChoiceID);
    }

    const state = await callSessionGet(roomID);
    if (!Array.isArray(state.envelopes_history) || state.envelopes_history.length !== 3) {
      throw new Error(`expected 3 history entries, got ${state.envelopes_history?.length}`);
    }
    for (const entry of state.envelopes_history) {
      if (entry.type !== "tangent.interview-question") {
        throw new Error(`unexpected history type ${entry.type}`);
      }
      if (!entry.interview_question?.thread_id || !entry.interview_question?.answer_text) {
        throw new Error(`missing structured interview history: ${JSON.stringify(entry)}`);
      }
    }
    log("3-question round-trip OK");
    ws.close(1000, "done");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function callInterviewQuestion(envelopeID, roomID = null, threadID, topicLabel) {
  const envelope = {
    v: 1,
    id: envelopeID,
    type: "tangent.interview-question",
    title: `Mock ${threadID}`,
    data: {
      prompt: `Tell me about ${topicLabel}.`,
      helper_text: "Use a long-form answer.",
      thread_id: threadID,
      topic_label: topicLabel,
      choices: [
        { id: "quality", label: "Quality" },
        { id: "time", label: "Time" },
      ],
      output_shape: {
        label: "Preferred output shape",
      },
    },
    ...(roomID ? { meta: { roomID } } : {}),
  };
  return callTool("tangent.interview_question", { envelope });
}

async function callSessionGet(roomID) {
  const result = await callTool("tangent.session_get", { roomID });
  return JSON.parse(result.result.content[0].text);
}

async function respondToEnvelope(
  ws,
  envelopeID,
  threadID,
  topicLabel,
  selectedChoiceID,
  answerText,
  outputShapeSignal,
) {
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
          answer_text: answerText,
          selected_choice_id: selectedChoiceID || undefined,
          thread_id: threadID,
          topic_label: topicLabel,
          output_shape_signal: outputShapeSignal || undefined,
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

function verifyResult(result, envelopeID, threadID, selectedChoiceID) {
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
  if (response.payload?.thread_id !== threadID) {
    throw new Error(`unexpected response payload ${JSON.stringify(response.payload)}`);
  }
  if ((response.payload?.selected_choice_id ?? "") !== selectedChoiceID) {
    throw new Error(`unexpected selected choice ${JSON.stringify(response.payload)}`);
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

async function waitForInterviewRoom(timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const listed = await callTool("tangent.session_list", { active_only: true });
    const rooms = JSON.parse(listed.result.content[0].text).rooms ?? [];
    const match = rooms.find((room) => room.current_envelope_type === "tangent.interview-question");
    if (match?.id) {
      return match.id;
    }
    await sleep(50);
  }
  throw new Error(`interview room not found within ${timeoutMs}ms`);
}

function parseSSE(text) {
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith("data:")) {
      return JSON.parse(line.slice(5).trim());
    }
  }
  throw new Error(`no data line in SSE response: ${text.slice(0, 200)}`);
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
