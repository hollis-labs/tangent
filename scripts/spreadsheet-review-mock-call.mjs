#!/usr/bin/env node

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(SCRIPT_DIR, "..");
const BINARY = path.join(REPO_ROOT, "tangent");
const PORT = Number.parseInt(process.env.TANGENT_MOCK_PORT ?? "7854", 10);
const BASE = `http://localhost:${PORT}`;
const MCP_URL = `${BASE}/mcp`;

const log = (...args) => console.log("[spreadsheet-review-mock]", ...args);

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
    const roomID = await createRoom("spreadsheet-review-mock");
    const ws = await openRoomSocket(roomID);

    const firstCall = callTool("tangent.spreadsheet-review", {
      envelope: {
        v: 1,
        id: "spreadsheet-1",
        type: "tangent.spreadsheet-review",
        title: "Spreadsheet review pass 1",
        data: {
          table_id: "table-1",
          title: "Leads",
          intent: "Review and route the imported rows.",
          columns: [
            { id: "name", label: "Name" },
            { id: "status", label: "Status" },
          ],
          rows: [
            { id: "row-1", name: "Alpha", status: "open" },
            { id: "row-2", name: "Beta", status: "closed" },
          ],
          row_actions: [{ id: "approve", label: "Approve" }],
        },
        meta: { roomID },
      },
    });

    await respondToSpreadsheetReview(ws, {
      table_id: "table-1",
      selected_row_ids: ["row-1"],
      selected_rows: [{ id: "row-1", name: "Alpha", status: "open" }],
      query_state: {
        search: "Alpha",
        filters: [{ column_id: "status", op: "eq", value: "open" }],
      },
      notes: "first pass",
      action_id: "approve",
      saved_views: [
        {
          name: "Open rows",
          query_state: {
            filters: [{ column_id: "status", op: "eq", value: "open" }],
          },
        },
      ],
      export_refs: [
        {
          name: "table-1-open-rows.csv",
          kind: "csv",
          mime_type: "text/csv",
          row_count: 1,
          column_count: 2,
        },
      ],
    });

    const firstResult = parseToolEnvelope(
      await verifyCall(firstCall, "spreadsheet-review pass 1"),
    );
    if (firstResult.payload?.table_id !== "table-1") {
      throw new Error(`first result missing table_id: ${JSON.stringify(firstResult)}`);
    }
    if (firstResult.payload?.action_id !== "approve") {
      throw new Error(`first result missing action_id: ${JSON.stringify(firstResult)}`);
    }

    const secondCall = callTool("tangent.spreadsheet-review", {
      envelope: {
        v: 1,
        id: "spreadsheet-2",
        type: "tangent.spreadsheet-review",
        title: "Spreadsheet review pass 2",
        data: {
          table_id: "table-1",
          title: "Leads",
          intent: "Confirm the saved room state reopens.",
          columns: [
            { id: "name", label: "Name" },
            { id: "status", label: "Status" },
          ],
          rows: [
            { id: "row-1", name: "Alpha", status: "open" },
            { id: "row-2", name: "Beta", status: "closed" },
          ],
          query_state: { search: "stale" },
          notes: "stale note",
        },
        meta: { roomID },
      },
    });

    const reopened = await nextEnvelope(ws, "tangent.spreadsheet-review");
    if (reopened.envelope?.data?.notes !== "first pass") {
      throw new Error(`reopened notes mismatch: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    if (reopened.envelope?.data?.query_state?.search !== "Alpha") {
      throw new Error(`reopened search mismatch: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    if ((reopened.envelope?.data?.selected_row_ids ?? [])[0] !== "row-1") {
      throw new Error(`reopened selected rows mismatch: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    if ((reopened.envelope?.data?.saved_views ?? []).length !== 1) {
      throw new Error(`reopened saved views missing: ${JSON.stringify(reopened.envelope?.data)}`);
    }
    if ((reopened.envelope?.data?.export_refs ?? []).length !== 1) {
      throw new Error(`reopened export refs missing: ${JSON.stringify(reopened.envelope?.data)}`);
    }

    ws.send(
      JSON.stringify({
        type: "cancel",
        envelopeId: reopened.envelopeId,
      }),
    );
    await verifyCall(secondCall, "spreadsheet-review pass 2");

    const state = await callSessionGet(roomID);
    if (state.spreadsheet_review?.notes !== "first pass") {
      throw new Error(`unexpected spreadsheet_review notes: ${JSON.stringify(state.spreadsheet_review)}`);
    }
    if (state.spreadsheet_review?.action_id !== "approve") {
      throw new Error(`unexpected spreadsheet_review action_id: ${JSON.stringify(state.spreadsheet_review)}`);
    }
    if ((state.spreadsheet_review?.selected_row_ids ?? [])[0] !== "row-1") {
      throw new Error(`unexpected spreadsheet_review selected_row_ids: ${JSON.stringify(state.spreadsheet_review)}`);
    }
    if ((state.spreadsheet_review?.saved_views ?? []).length !== 1) {
      throw new Error(`unexpected spreadsheet_review saved_views: ${JSON.stringify(state.spreadsheet_review)}`);
    }
    if ((state.spreadsheet_review?.export_refs ?? []).length !== 1) {
      throw new Error(`unexpected spreadsheet_review export_refs: ${JSON.stringify(state.spreadsheet_review)}`);
    }
    if ((state.envelopes_history ?? []).length !== 2) {
      throw new Error(`unexpected envelopes_history: ${JSON.stringify(state.envelopes_history)}`);
    }

    log("submit -> reopen -> cancel loop OK");
  } finally {
    proc.kill("SIGINT");
    const winner = await Promise.race([
      procExit,
      sleep(2000).then(() => "timeout"),
    ]);
    if (winner === "timeout") {
      proc.kill("SIGKILL");
    }
  }
}

async function respondToSpreadsheetReview(ws, payload) {
  const message = await nextEnvelope(ws, "tangent.spreadsheet-review");
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
      },
    }),
  );
  await sleep(25);
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
  const response = await fetch(MCP_URL, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name, arguments: args },
    }),
  });
  if (!response.ok) {
    throw new Error(`MCP HTTP ${response.status}: ${await response.text()}`);
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (contentType.includes("text/event-stream")) {
    return parseSSE(await response.text());
  }
  return response.json();
}

async function openRoomSocket(roomID) {
  const ws = new WebSocket(
    `${BASE.replace("http", "ws")}/ws?roomID=${encodeURIComponent(roomID)}`,
  );
  await once(ws, "open");
  return ws;
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
  if (result.result?.isError) {
    throw new Error(`${label} returned IsError=true: ${result.result.content?.[0]?.text}`);
  }
  return result;
}

function parseToolEnvelope(result) {
  const text = result.result?.content?.[0]?.text;
  if (typeof text !== "string") {
    throw new Error(`tool result missing text payload: ${JSON.stringify(result)}`);
  }
  return JSON.parse(text);
}

function parseSSE(body) {
  const chunks = body
    .split("\n\n")
    .map((chunk) => chunk.trim())
    .filter(Boolean);
  for (const chunk of chunks.reverse()) {
    const dataLine = chunk
      .split("\n")
      .find((line) => line.startsWith("data: "));
    if (dataLine) {
      return JSON.parse(dataLine.slice(6));
    }
  }
  throw new Error(`unable to parse SSE response: ${body}`);
}

function once(target, eventName) {
  return new Promise((resolve, reject) => {
    const handleSuccess = (event) => {
      cleanup();
      resolve(event);
    };
    const handleError = (event) => {
      cleanup();
      reject(event instanceof Error ? event : new Error(String(event)));
    };
    const cleanup = () => {
      target.removeEventListener(eventName, handleSuccess);
      target.removeEventListener("error", handleError);
    };
    target.addEventListener(eventName, handleSuccess, { once: true });
    target.addEventListener("error", handleError, { once: true });
  });
}

async function waitForServer(url, timeoutMs) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          jsonrpc: "2.0",
          id: 1,
          method: "tools/list",
        }),
      });
      if (response.ok) {
        return;
      }
    } catch {
      // ignore until timeout
    }
    await sleep(100);
  }
  throw new Error(`Timed out waiting for Tangent at ${url}`);
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
