// Real Chrome acceptance of production React routes through a synthetic transport.
// No Tangent server, participant identity, provider, shared profile or live data.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { access, mkdtemp, readFile, rm } from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { createServer } from "../ui/node_modules/vite/dist/node/index.js";

const root = fileURLToPath(new URL("../", import.meta.url));
const browser = process.env.TANGENT_BROWSER_BIN ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
await access(browser);
assert(process.env.TMPDIR, "TMPDIR must name the worker's private scratch directory");
const scratch = await mkdtemp(path.join(process.env.TMPDIR, "ui-command-browser-"));
let chrome, socket, server;
try {
  server = await createServer({ root: path.join(root, "ui"), configFile: path.join(root, "ui/vite.config.ts"), server: { host: "127.0.0.1", port: 0, strictPort: false }, logLevel: "error" });
  await server.listen();
  const address = server.httpServer.address();
  assert(address && typeof address !== "string");
  chrome = spawn(browser, ["--headless=new", "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", `--user-data-dir=${scratch}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-extensions", "--disable-sync", "about:blank"], { stdio: "ignore" });
  let devtools;
  for (let i = 0; i < 150; i++) {
    try { devtools = await readFile(path.join(scratch, "DevToolsActivePort"), "utf8"); break; } catch { await delay(100); }
  }
  assert(devtools, "isolated Chrome failed to expose its loopback debugging endpoint");
  const [port, endpoint] = devtools.trim().split("\n");
  socket = new WebSocket(`ws://127.0.0.1:${port}${endpoint}`);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }); });
  let serial = 0;
  const pending = new Map();
  socket.addEventListener("message", ({ data }) => {
    const message = JSON.parse(String(data));
    const waiter = pending.get(message.id);
    if (!waiter) return;
    pending.delete(message.id);
    if (message.error) waiter.reject(new Error(JSON.stringify(message.error))); else waiter.resolve(message.result);
  });
  const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++serial;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)); }, 15000);
    pending.set(id, { resolve: (value) => { clearTimeout(timer); resolve(value); }, reject: (error) => { clearTimeout(timer); reject(error); } });
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
  const { targetId } = await call("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await call("Target.attachToTarget", { targetId, flatten: true });
  await call("Runtime.enable", {}, sessionId);
  await call("Page.enable", {}, sessionId);
  const evaluate = async (expression) => {
    const result = await call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }, sessionId);
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description ?? "browser evaluation failed");
    return result.result.value;
  };
  const wait = async (expression) => {
    for (let i = 0; i < 100; i++) { if (await evaluate(expression)) return; await delay(50); }
    throw new Error(`browser condition timed out: ${expression}`);
  };
  const url = `http://127.0.0.1:${address.port}/test-drivers/ui-commands.html?view=pending`;
  await call("Page.navigate", { url }, sessionId);
  await wait('window.uiCommandFixture && document.querySelector("input[type=checkbox]")?.disabled === false && document.body.textContent.includes("Synthetic review")');
  assert.equal(await evaluate("document.hasFocus()"), true);
  const before = await evaluate("({url: location.href, history: history.length})");
  await evaluate('document.querySelector("input[type=checkbox]").click(); document.querySelector("input[type=checkbox]").focus()');
  await evaluate('window.uiCommandFixture.transport.command("open_modal", {id: "synthetic-item"})');
  await wait('document.querySelector("[role=dialog]")?.contains(document.activeElement)');
  assert.equal(await evaluate('window.uiCommandFixture.transport.ack().status'), "applied");
  assert.deepEqual(await evaluate("({url: location.href, history: history.length})"), before);
  // Real keyboard Tab cannot escape the agent-opened modal's focus trap.
  await call("Input.dispatchKeyEvent", { type: "keyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 }, sessionId);
  await call("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 }, sessionId);
  assert.equal(await evaluate('document.querySelector("[role=dialog]").contains(document.activeElement)'), true);
  await call("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, sessionId);
  await call("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 }, sessionId);
  await wait('!document.querySelector("[role=dialog]") && document.activeElement === document.querySelector("input[type=checkbox]")');
  assert.deepEqual(await evaluate("({url: location.href, history: history.length})"), before);
  assert.deepEqual(await evaluate('window.uiCommandFixture.requests.filter(request => request.method !== "GET")'), []);
  await wait('window.uiCommandFixture.transport.descriptor().active_filters.some(filter => filter.name === "modal" && filter.values[0] === "closed")');
  const carrier = await evaluate('window.uiCommandFixture.transport.descriptor()');
  assert.equal(JSON.stringify(carrier).includes("Private fixture"), false);
  // A URL-backed filter really uses BrowserRouter and changes the query.
  await delay(150);
  await evaluate('window.uiCommandFixture.transport.command("set_filter", {name: "type", value: "document"}, "url-backed")');
  assert.equal(await evaluate('new URLSearchParams(location.search).get("type")'), "document");
  assert.equal(await evaluate('window.uiCommandFixture.transport.ack().status'), "applied");
  await delay(150);
  await evaluate('window.uiCommandFixture.transport.command("navigate", {route: "/channels/synthetic-channel"}, "url-backed")');
  assert.equal(await evaluate('document.querySelector("h1")?.textContent'), "Channels");
  assert.equal(await evaluate('window.uiCommandFixture.transport.ack().status'), "applied");
  await wait('document.body.textContent.includes("No messages yet.")');
  assert.deepEqual(await evaluate('window.uiCommandFixture.requests.filter(request => request.method !== "GET")'), []);
  const version = await call("Browser.getVersion");
  console.log(`PASS ${version.product}: command modal preserves URL/history; focus enters, traps Tab, Escape restores focus; router filter/cross-route navigation commit before ack; no writes; synthetic transport only.`);
  await call("Browser.close");
} finally {
  socket?.close();
  if (chrome && chrome.exitCode === null) {
    chrome.kill("SIGTERM");
    await Promise.race([new Promise((resolve) => chrome.once("exit", resolve)), delay(3000)]);
    if (chrome.exitCode === null) { chrome.kill("SIGKILL"); await new Promise((resolve) => chrome.once("exit", resolve)); }
  }
  await server?.close();
  await rm(scratch, { recursive: true, force: true });
}
