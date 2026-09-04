# HITL inbox request shapes

These shapes target the strict `tangent.hitl-item` contract version `1.0`.
Unknown fields are rejected. For every field, variant, bound, result, and typed
error, use [`docs/contracts/hitl-inbox-v1.md`](../../../../docs/contracts/hitl-inbox-v1.md)
and the embedded
[`packages/tangent.hitl/hitl-item/request.schema.json`](../../../../internal/envelope/extensions/packages/tangent.hitl/hitl-item/request.schema.json).
The examples below are executable smoke fixtures. Replace their assertions and
evidence with facts you actually have; omit optional correlations or evidence
rather than fabricating an ID, revision, digest, label, or content.

## Agent request template

Copy this prompt and replace the bracketed values:

```text
Use `tangent.hitl_enqueue` to place one durable [approval|attention] item in
Tangent's `/hitl` inbox. Use a stable idempotency key for this logical request.
Title: [short operator label]
Summary: [what happened and why it matters]
Request: [the exact decision or acknowledgement needed]
Recommendation: [recommended choice and rationale, if useful]
Approve impact: [what the caller may do after approval]
Deny impact: [what the caller will do after denial]
Source assertion: application [stable application ID], agent [agent ID]
Correlations: project [authority/ID], task [authority/ID/revision], session [authority/ID]
Evidence: [only bounded markdown, text, unified diff, Tangent reference, or artifact metadata]

Return immediately after enqueue with the durable item ID and item URL. Retain
the handle and use `tangent.hitl_get` or a bounded `tangent.hitl_await` later.
Do not claim that source/correlation values are authenticated, do not wait on
the enqueue call for a human response, and do not perform any downstream action
until the calling workflow interprets the retrieved outcome.
```

For attention, omit both approval impacts and ask for acknowledgement, an
optional note, or a reply. Keep `source.application_id` stable: direct Tangent
uses it to derive caller scope, and Get/Await/Withdraw must repeat it under
`caller.application_id`.

## Direct Tangent MCP

Call `tangent.hitl_enqueue` directly with this complete approval request:

```json
{
  "contract_version": "1.0",
  "kind": "approval",
  "idempotency_key": "codex:tangent:CW-20260904-0016:docs-approval-v1",
  "title": "Approve the HITL launcher documentation",
  "summary": "The launcher examples and supported gates have passed.",
  "request": "Approve or deny publishing the HITL launcher documentation.",
  "source": {
    "application_id": "codex",
    "application_label": "Codex",
    "agent_id": "hitl-docs-worker",
    "agent_label": "HITL docs worker"
  },
  "recommendation": "Approve after confirming the direct and Tether smoke evidence.",
  "impact": {
    "approve": "The caller may publish the documentation after retrieving this outcome.",
    "deny": "The caller keeps the documentation unpublished and reviews the operator note."
  },
  "correlations": {
    "project": {
      "authority": "torque",
      "id": "PRJ-20260825-0002",
      "label": "Tangent"
    },
    "task": {
      "authority": "torque",
      "id": "CW-20260904-0016",
      "revision": "1",
      "label": "Publish HITL launcher"
    },
    "session": {
      "authority": "codex",
      "id": "session-hitl-docs"
    }
  },
  "evidence": [
    {
      "type": "markdown",
      "label": "Validation summary",
      "content": "## Checks\n\n- direct MCP\n- Tether native-flat gateway"
    },
    {
      "type": "text",
      "label": "Scope note",
      "content": "Tangent records the decision; the caller owns publishing."
    },
    {
      "type": "diff",
      "label": "Discovery update",
      "format": "unified",
      "base_label": "before",
      "head_label": "after",
      "content": "--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old discovery\n+HITL inbox discovery"
    },
    {
      "type": "tangent_reference",
      "label": "Durable inbox",
      "surface_id": "surface_hitl_default",
      "description": "Read-only reference to the retained operator surface."
    },
    {
      "type": "artifact_ref",
      "label": "Supported gate report",
      "authority": "torque",
      "artifact_id": "artifact-hitl-docs-0016",
      "digest": "sha256:docs0016",
      "media_type": "text/markdown",
      "logical_kind": "test-report",
      "sensitivity": "internal"
    }
  ]
}
```

The successful result is a handle, not the operator decision. Retain its
`item_id`, `item_url`, `queue_sequence`, and current `revision`.

Use these later-operation shapes with the returned item ID:

```json
{
  "contract_version": "1.0",
  "item_id": "REPLACE_WITH_ITEM_ID",
  "caller": { "application_id": "codex" }
}
```

The shape above is valid for `tangent.hitl_get`. It is also valid for
`tangent.hitl_await`; add `"wait_ms": 30000` when a bounded wait is useful. A
timeout still returns the latest pending projection. For
`tangent.hitl_withdraw`, the same shape is sufficient; optionally add the last
observed `expected_revision` and a non-empty `reason`. Never withdraw merely
because an Await request timed out.

## Tether native-flat gateway

Start Tether's native-flat proxy with the configured Tangent server selected:

```bash
mux mcp --proxy --only tangent
```

Then copy this request into the MCP client connected to that proxy:

```text
Call the native-flat tool `tangent.hitl_enqueue` through Tether with exactly
these arguments, without wrapping them in `mux_call`:
{"contract_version":"1.0","kind":"attention","idempotency_key":"tether:tangent:CW-20260904-0016:docs-attention-v1","title":"Review HITL documentation result","summary":"The direct and gateway examples use the same strict v1 schema.","request":"Acknowledge the documentation result, optionally leaving a note or reply.","source":{"application_id":"tether-docs-smoke","application_label":"Tether","agent_id":"hitl-docs-gateway"},"correlations":{"project":{"authority":"torque","id":"PRJ-20260825-0002"},"task":{"authority":"torque","id":"CW-20260904-0016"},"session":{"authority":"tether","id":"session-hitl-docs-gateway"}},"evidence":[{"type":"text","label":"Gateway mode","content":"Native-flat discovery preserves the object-root HITL tool schema."}]}
Return immediately with the durable handle. Later use native-flat
`tangent.hitl_get` or `tangent.hitl_await` with the returned item ID and
`{"application_id":"tether-docs-smoke"}` as `caller`. Use
`tangent.hitl_withdraw` only if this application no longer needs the pending
item.
```

Native-flat forwarding does not by itself authenticate the payload's
`source`. The current direct-loopback/Tether smoke retains asserted assurance;
only a separately verified gateway binding may establish authenticated
identity, and it cannot rewrite the source assertion.

Do not use the tested Tether adapter's `mux_call` compatibility wrapper for
this strict contract: its injected top-level tracing field is rejected. The
adapter's discovery schema can also omit top-level conditional unions, so
Tangent's upstream v1 validation remains authoritative.

The cross-repository test sets `HOLLIS_OTEL_DISABLED=1` only to avoid a known
startup logging recursion in the tested local Tether checkout. That is a test
harness workaround, not part of the native-flat protocol or normal launcher
configuration.
