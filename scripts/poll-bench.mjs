// Repeatable idle-traffic benchmark for the Tangent UI.
//
//   node scripts/poll-bench.mjs [--url http://127.0.0.1:7999] [--idle 20] [--routes /,/chat,/channels]
//
// Point it at a LOCAL tangent built from this tree and run against a COPY of a real
// database (sqlite3 -readonly src ".backup copy.db"); never at a live deployment.
// For each route it reports: requests until the first render settles, time until the
// inbox shows its pending count (for "/"), then requests/minute by path over an idle
// window in which nothing changes, plus long tasks (>50ms) seen during that window.
// Playwright is resolved from TANGENT_BENCH_PLAYWRIGHT (a path to playwright's index.mjs)
// or the `playwright` package; TANGENT_BENCH_CHROMIUM overrides the browser binary.
import { pathToFileURL } from "node:url";

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
const base = arg("url", "http://127.0.0.1:7999");
const idleSeconds = Number(arg("idle", "20"));
const routes = arg("routes", "/,/chat,/channels").split(",");
const pw = process.env.TANGENT_BENCH_PLAYWRIGHT
  ? await import(pathToFileURL(process.env.TANGENT_BENCH_PLAYWRIGHT).href)
  : await import("playwright");
const browser = await pw.chromium.launch({ executablePath: process.env.TANGENT_BENCH_CHROMIUM || undefined });

const tally = (list) => {
  const out = {};
  for (const path of list) out[path] = (out[path] ?? 0) + 1;
  return out;
};

const results = [];
for (const route of routes) {
  const page = await browser.newPage();
  const seen = [];
  const bytes = {};
  const statuses = {};
  let t0 = 0;
  page.on("request", (r) => seen.push({ at: Date.now() - t0, path: new URL(r.url()).pathname }));
  page.on("response", async (r) => {
    const path = new URL(r.url()).pathname;
    if (!path.startsWith("/api/")) return;
    const key = `${path} ${r.status()}`;
    statuses[key] = (statuses[key] ?? 0) + 1;
    try { bytes[path] = (bytes[path] ?? 0) + (await r.request().sizes()).responseBodySize; } catch { /* stream */ }
  });
  await page.addInitScript(() => {
    window.__longTasks = 0;
    new PerformanceObserver((l) => { window.__longTasks += l.getEntries().length; }).observe({ entryTypes: ["longtask"] });
  });
  t0 = Date.now();
  await page.goto(base + route, { waitUntil: "domcontentloaded" });
  let inboxMs = null;
  if (route === "/") {
    await page.waitForFunction(() => /[1-9]\d* pending/.test(document.body.innerText), null, { timeout: 30000 }).catch(() => {});
    inboxMs = Date.now() - t0;
  }
  await page.waitForTimeout(3000); // let startup requests settle
  const startup = seen.filter((r) => !r.path.startsWith("/assets/") && !r.path.endsWith("/events"));
  const idleFrom = Date.now() - t0;
  const longBefore = await page.evaluate(() => window.__longTasks);
  await page.waitForTimeout(idleSeconds * 1000);
  const idle = seen.filter((r) => r.at >= idleFrom && !r.path.startsWith("/assets/"));
  const longTasks = (await page.evaluate(() => window.__longTasks)) - longBefore;
  const perMin = (n) => Math.round((n * 60) / idleSeconds);
  results.push({
    route,
    timeToInboxRenderedMs: inboxMs,
    startupRequests: tally(startup.map((r) => r.path)),
    idleRequestsPerMin: Object.fromEntries(Object.entries(tally(idle.map((r) => r.path))).map(([k, v]) => [k, perMin(v)])),
    idleTotalPerMin: perMin(idle.length),
    apiResponses: statuses,
    apiBodyBytes: bytes,
    idleLongTasks: longTasks,
  });
  await page.close();
}
await browser.close();
console.log(JSON.stringify({ base, idleSeconds, results }, null, 2));
