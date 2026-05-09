#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7852", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[output-render-mock]", ...args);

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
    const roomID = await createRoom("writing-flow-output");
    const ws = await openRoomSocket(roomID);

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "interview" });
    await runInterviewQuestion(ws, roomID, "shape-1", "What are we writing, and how big should it be?");

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "synthesis" });
    await runSynthesis(ws, roomID);

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "drafting" });
    await runDraftAccept(ws, roomID, "draft-1", "Original opening paragraph with too much setup and two examples.");

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "revision" });
    await runRevision(ws, roomID, "review-1", "review");

    await callTool("tangent.session_advance_phase", {
      roomID,
      to_phase: "drafting",
      reason: "jump back after review to tighten the opening",
    });
    await runDraftInlineEdit(
      ws,
      roomID,
      "draft-2",
      "Lead with the claim, then keep one concrete example.",
    );

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "revision" });
    await runRevision(ws, roomID, "copy-1", "copy");

    await callTool("tangent.session_advance_phase", { roomID, to_phase: "output" });
    await runOutputRender(
      ws,
      roomID,
      "# Final draft\n\nLead with the claim, then keep one concrete example.",
    );

    const state = await callSessionGet(roomID);
    if (state.final_output?.markdown !== "# Final draft\n\nLead with the claim, then keep one concrete example.") {
      throw new Error(`unexpected final_output ${JSON.stringify(state.final_output)}`);
    }
    const visited = (state.phases_visited ?? []).join(",");
    if (visited !== "interview,synthesis,drafting,revision,drafting,revision,output") {
      throw new Error(`unexpected phases_visited ${visited}`);
    }
    log("full writing flow with jump-back OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([procExit, sleep(2000).then(() => "timeout")]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function runInterviewQuestion(ws, roomID, envelopeID, prompt) {
  const call = callTool("tangent.interview_question", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.interview-question",
      title: "Writing scope",
      data: {
        prompt,
        topic_label: "scope",
      },
      meta: { roomID },
    },
  });
  await expectDataEnvelope(ws, "tangent.interview-question", {
    answer_text: "A short article, around 800 words, with one clear example.",
    topic_label: "scope",
  });
  await verifyCall(call, "interview question");
}

async function runSynthesis(ws, roomID) {
  const call = callTool("tangent.synthesis_notes", {
    envelope: {
      v: 1,
      id: "synth-1",
      type: "tangent.synthesis-notes",
      title: "Synthesis handoff",
      data: {
        private_notes: "Keep the article direct and constraint-led.",
        summary: "Lead with the claim, then illustrate it with one example.",
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
  await verifyCall(call, "synthesis notes");
}

async function runDraftAccept(ws, roomID, envelopeID, content) {
  const call = callTool("tangent.block_draft", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.block-draft",
      title: "Opening block",
      data: {
        block_id: "intro",
        mode: "section",
        label: "Intro",
        content,
      },
      meta: { roomID },
    },
  });
  await expectDataEnvelope(ws, "tangent.block-draft", {
    decision: "accept",
    block_id: "intro",
    mode: "section",
  });
  await verifyCall(call, "block draft accept");
}

async function runDraftInlineEdit(ws, roomID, envelopeID, editedText) {
  const call = callTool("tangent.block_draft", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.block-draft",
      title: "Opening revision",
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
    decision: "inline_edit",
    block_id: "intro",
    mode: "section",
    edited_text: editedText,
    feedback: "Tighten the setup before the example.",
  });
  await verifyCall(call, "block draft inline edit");
}

async function runRevision(ws, roomID, envelopeID, lens) {
  const call = callTool("tangent.prose_revision", {
    envelope: {
      v: 1,
      id: envelopeID,
      type: "tangent.prose-revision",
      title: `${lens} pass`,
      data: {
        lens,
        revision_id: envelopeID,
        block_id: "intro",
        source_text: "Original opening paragraph with too much setup and two examples.",
        suggestions: [
          {
            id: "s1",
            label: "Lead with the claim",
            suggested_text: "Lead with the main claim before the setup.",
          },
        ],
      },
      meta: { roomID },
    },
  });
  await expectDataEnvelope(ws, "tangent.prose-revision", {
    lens,
    revision_id: envelopeID,
    block_id: "intro",
    outcomes: [{ suggestion_id: "s1", decision: "accept" }],
  });
  await verifyCall(call, `${lens} revision`);
}

async function runOutputRender(ws, roomID, markdown) {
  const call = callTool("tangent.output_render", {
    envelope: {
      v: 1,
      id: "output-1",
      type: "tangent.output-render",
      title: "Final output",
      data: {
        title: "Final draft",
        markdown,
        filename: "final-draft.md",
        format: "markdown",
        summary: "Final polished article.",
      },
      meta: { roomID },
    },
  });
  await expectAckEnvelope(ws, "tangent.output-render");
  await verifyCall(call, "output render");
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

async function expectAckEnvelope(ws, type) {
  const message = await nextEnvelope(ws, type);
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
  const message = await nextEnvelope(ws, type);
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
