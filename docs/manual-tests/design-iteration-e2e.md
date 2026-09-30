# Design-iteration e2e

Manual smoke for `tangent.design-iteration` with a real Claude Code
session. This validates the sandboxed iframe workflow and the
multi-envelope-per-room iteration loop.

## 1. Boot Tangent

```bash
cd tangent  # your clone of the repo
./tangent
```

Watch for a log line like:

```text
level=INFO msg="design-iteration room created" room=<roomID> envelope=<id>
```

The room **URL is not logged** — the tool logs only the room id (ADR 0002 §8:
an assembled URL in a log line is a locator that outlives the log). Build it
yourself as `http://127.0.0.1:7842/r/<roomID>`, or read it from the tool
response, which carries the room URL back to the caller.

## 2. Register Tangent in Claude Code

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

Fallback:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

## 3. Trigger an iteration

Ask Claude Code to call `tangent.design-iteration` with a small HTML
preview that includes clickable regions identified by CSS selectors.

Example prompt:

> Use `tangent.design-iteration` to show me two hero-card concepts and
> let me click the stronger one.

## 4. Interact in the browser

Open the `/r/<roomID>` URL. Confirm:

- The preview renders inside an iframe.
- The iframe has interaction, but no external navigation/forms/popups.
- Clicking an annotated region returns a response to the agent.
- If Claude sends another iteration to the same room, a new variant tab
  appears and the current preview updates.

## 5. Verify the response

Claude should receive a `data` response shaped like:

```json
{
  "v": 1,
  "envelopeId": "<id>",
  "kind": "data",
  "status": "submitted",
  "payload": {
    "variant_id": "variant-2",
    "action_id": "hero-b",
    "action_kind": "click-region",
    "value": "Hero B"
  }
}
```

## Mock equivalent

```bash
node scripts/design-iteration-mock-call.mjs
```

That script drives three iterations through one room and asserts all
three responses plus room history. For a curl-first version of the same
room reuse pattern, see
[`multi-envelope-session-e2e.md`](./multi-envelope-session-e2e.md).
