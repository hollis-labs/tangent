# Feedback e2e

End-to-end smoke for `tangent.feedback` driven by a real Claude Code
session. This mirrors the `tangent.triage` manual recipe, but exercises
the structured question form workflow.

## Prerequisites

- `make build` produced `./tangent`.
- Claude Code is installed and can connect to local MCP servers.
- Port `7842` is free, or `TANGENT_HTTP_PORT` is set consistently.

## 1. Boot Tangent

```bash
cd tangent  # your clone of the repo
./tangent
```

Watch for a log line like:

```text
level=INFO msg="feedback room created" room=<roomID> envelope=<id>
```

The room **URL is not logged** — the tool logs only the room id (ADR 0002 §8:
an assembled URL in a log line is a locator that outlives the log). Build it
yourself as `http://127.0.0.1:7842/r/<roomID>`, or read it from the tool
response, which carries the room URL back to the caller.

## 2. Register Tangent in Claude Code

```bash
claude mcp add --transport http tangent http://localhost:7842/mcp
```

Fallback for older clients:

```bash
claude mcp add --transport sse tangent http://localhost:7842/sse
```

## 3. Trigger a feedback call

In Claude Code, send a prompt like:

> Use `tangent.feedback` to ask me for a launch headline, a yes/no
> direction choice, and a set of release tags.

Claude should call the tool, and Tangent should print the room URL.

## 4. Fill the form in the browser

Open the `/r/<roomID>` URL in a browser. Confirm:

- Text, radio, and multiselect questions render.
- Submit is disabled until required questions are answered.
- Cancel is available at all times.

Fill the fields and submit.

## 5. Verify the response

Claude should receive a `data` response containing:

```json
{
  "v": 1,
  "envelopeId": "<id>",
  "kind": "data",
  "status": "submitted",
  "payload": {
    "answers": [
      {"questionId": "headline", "value": "..."},
      {"questionId": "direction", "value": "yes"},
      {"questionId": "tags", "value": ["ux"]}
    ]
  }
}
```

## Mock equivalent

Use the fully automated smoke when you want wire-level verification
without Claude Code:

```bash
node scripts/feedback-mock-call.mjs
```

Feedback also works as one step inside a longer-lived room via
`tangent.session_advance`; see
[`multi-envelope-session-e2e.md`](./multi-envelope-session-e2e.md).
