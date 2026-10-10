# ADR 0013: Tangent chat agent plugin

**Status:** Accepted by Chrispian (DEC-061), 2026-10-09. Source reconciliation: CW-20261009-0083, 2026-10-10.
**Date:** 2026-10-09
**Amends:** ADR 0005 (product boundary) narrowly: a plugin may present an agent chat UI as a thin client.
**Related:** ADR 0007 (view state), 0008 (plugin model), 0012 (messaging consumer); ADR 0014 (UI commands and view descriptors); Torque epic EP-20261009-0003; existing host tasks CW-20261003-0064 (plugin UI loading, right region), -0062 (enable/disable, unload), -0063 (config/secrets), -0034 (inbox MCP read tools); umbrella CW-20261003-0071.
**Evidence base:** source review of Tangent `43dea0cc`, Nanite `d73cc220` and design-kit `dbcf4fa7`, 2026-10-10. Acceptance is PM message `01a1231d-8439-7c02-8dac-e032671dc013`; earlier workspace drafts were still marked proposed. These are source observations, not deployment or end-to-end acceptance. [R] read; [U] unverified.

## Context
The owner wants an opt-in chat agent on every Tangent page, in a right rail, that sees Tangent data and what the user is viewing, uses MCP tools to act (update remote sources, change what is on screen), and benefits from Nanite's memory, continuity and tool use. It must not be core: users who do not want an agent do not get one. It unlocks other plugins (the Portfolio Manager first).

Facts:
- Tangent's plugin backend is solid [R]: subprocess plugins (protocol 2, manifest v2), `tangent.<name>` MCP tools, browser routes under `/api/plugins/<id>/…`, surfaces/rooms through `tangent.session_*`. Plugin UI is not: `ui/src/lib/plugin-loader.ts` is a minimal proof, the registry is empty, there is no slot rendering and no enable/disable flag [R, 43dea0cc]. All are already planned (CW-20261003-0064, -0062, -0063).
- ADR 0005 says Tangent does not own agent sessions, conversations or chat multiplexing [R]. Nanite owns agent definitions, sessions and conversations.
- Nanite already implements native `/api/agent/v1` cognitive views and turns, advertising `chatstream/v1`. It accepts pinned `definition_ref`, supports per-turn SSE, live deltas, cursor replay and in-band once approvals. The native turn DTO has no `client_context` or `ui.command` field and rejects unknown fields. A cognitive view does not enroll an actor or boot a Cairn bundle. Operator bearer authentication is implemented; the old survey's basic-auth-only description is incomplete. Current deployed availability is unverified by this review and remains CW-20261009-0084's acceptance obligation [R/U].
- Cairn builds provider-native boot directories (instructions, skills, MCP config) from a profile; it does not supervise agents [R, per survey]. Memory/continuity come from Nanite plus Tesseract.
- Tether channel chat is a second possible transport: whole-turn replies only, no token streaming; routing deployment unverified.

## Options considered
**A. Plugin as thin client of Nanite (chosen).** Tangent holds only opaque references; Nanite owns the conversation. Fits ADR 0005. Costs: depends on Nanite's new chat stream; Nanite's single-user/auth posture constrains deployment.
**B. Tether channel chat only.** Reuses Tether identity and budgets; whole-turn only; poor fit for an interactive rail; kept as a second backend.
**C. Tangent-owned agent loop over the Tether LLM gateway.** Full control and budgets, but re-implements the agent loop, approvals and truncation, and requires a larger ADR 0005 change.
**D. Built into the host as a core feature.** Fastest, but the opposite of opt-in and of the plugin story.

## Decision
1. **Opt-in plugin.** A first-party plugin (in `tangent-plugins`) contributes the right-rail panel, its routes and its MCP tools. It is enabled by the operator through the host enable mechanism (CW-20261003-0062); disabled means nothing renders and no agent runs. Tangent core gains the host UI foundation (existing tasks) and the UI-command channel (ADR 0014), not an agent.
2. **Thin client; Tangent owns no conversations.** The plugin stores only opaque references (Nanite session id, selected backend, UI preferences). History lives with the agent runtime.
3. **Agent backend interface.** The plugin talks to an `AgentBackend` (create/resume session, send turn, stream events, approve, cancel, list history refs). MVP backend: Nanite via the new chat-stream standard. Second backend later: Tether channel chat. The browser never holds backend credentials; calls go through the plugin's server side.
4. **Nanite + Cairn profile.** The integration must explicitly map the Cairn-built profile to Nanite's supported runtime/definition and tool-grant contracts; a boot bundle is not a native `definition_ref` or an authority issuer. Current native creation refuses `boot_profile_id` and `runtime_kind` overrides, and its definition mapper refuses unapplied tool/skill/capability and continuity requirements. This mapping remains coordinated with CW-20261009-0084/-0085. A Cairn-built agent profile ("Tangent assistant") defines instructions, skills and MCP servers; memory and continuity come from Nanite and Tesseract. Tool allowlist is explicit and least-privilege: read tools by default; writes (including updating remote sources such as Torque/Tesseract) require operator confirmation until trust rules are set.
5. **Capabilities.** The assistant's tools are plugin-defined MCP tools (`tangent.*`) with explicit least-privilege grants; CW-20261003-0067 must supply enforcement rather than treating declarations or MCP annotations as grants. Current participant route capabilities and Nanite's tool roster/permission engine are existing boundaries, not proof of an assistant-to-participant grant. Read/view access, participant-targeted UI control and remote-source writes need distinct scoped authority; writes retain operator confirmation. UI commands are tools (ADR 0014). Real shipped tools belong in the build's documentation gate; proposed tool names are not availability claims.
6. **Prerequisite coordination.** Use the native canonical chat stream, not the legacy retained-message SSE dialect. Per-turn cursor replay, keepalive comments, live deltas and bound approval flow already exist in source. CW-20261009-0084 must complete the embedded-client binding, bounded per-turn client context, assistant tool/participant association and deployment/conformance proof. Session metadata is not a substitute for client context; global MCP server registration is not a verified session-specific attach. No `ui.command` event is presently advertised. Tangent's current plugin HTTP DTO buffers complete responses, so a host streaming bridge or a separately versioned streaming contract is required before the rail can consume Nanite SSE; this ADR does not authorize a proxy implementation.
7. **Surfaces beyond chat** (document viewer with highlighting, slides) are separate follow-on work; the MVP agent can already open existing rooms/surfaces.

## Consequences
- The chat rail ships only after the host UI foundation (CW-20261003-0064 and -0062). Parallel work: backend, backend interface, Nanite coordination, view descriptors.
- Nanite's single-operator model bounds the MVP. Its source supports `NANITE_AUTH_TOKEN` bearer admission (required when configured, including loopback, and for off-box bind); this does not enroll an actor, map a participant or establish multi-user isolation. The actual backend credential and participant binding remain explicit integration prerequisites.
- LLM calls bypass Tether budgets unless Nanite gains a gateway-backed provider; recorded as a known gap.
- An agent with Tangent tools acts with the operator's authority; confirmations and the tool allowlist are the control.
- ADR 0005 needs a short amendment note; no change to its boundary for other plugins.

## Open questions
Deployed native endpoint/conformance transcript; dedicated client-context and participant binding; supported Cairn/agentdef mapping and verified session-specific MCP grants (direct transport or Tether gateway); proxy/stream lifetime contract; per-user vs per-install profile; provider budget/rate limits; how approvals surface while the rail is closed. Nanite persists committed outcomes but its event log is bounded and process-local: observer EOF is not completion, and replay gaps recover through the authoritative turn snapshot.

## Slice 0 source evidence and remaining boundaries

- Nanite: `internal/api/agent_v1.go` (create/turn fields and capabilities),
  `agent_v1_turns.go` (authorized per-turn events/status/cancel),
  `agent_v1_approvals.go`, `agent_v1_pages.go`,
  `internal/service/definition_resolver.go` and `cognitive_views.go`.
  The released `substrate/agent@v0.3.0/transport/httpstream` writer handles
  strict `Last-Event-ID`, canonical events, keepalive comments and gap recovery.
  Session-wide event projection remains absent; signed list/history cursors
  are distinct from event cursors.
- Auth: `internal/server/agent_auth.go` and `auth.go` bind operator bearer
  admission. No running endpoint, credential or provider was probed.
- MCP: `internal/service/mcp_servers.go`, `internal/mcpconfig/mcpconfig.go`
  and `transport.go`, `internal/mcp/manager.go` and `remote_transport.go`
  support global stdio/streamable configuration. `internal/service/tool.go`
  applies the actual tool roster; `chat_tool_executor.go` applies permission
  posture. Transport support does not prove assistant-specific attachment,
  identity or granted tools. The native definition mapper refuses requirements
  it does not apply.
- Rail: design-kit's `packages/kit-chat/src/components/chat-stream.tsx`
  exposes controlled items, streaming status, custom rendering and history
  callbacks with flexible minimum-width/height layout. `chat-input.tsx`
  exposes controlled text, submit/busy/cancel and toolbar slots. Source supports
  composition in a constrained rail; it owns no transport, authority or
  persistence. This review did not render a Tangent rail or verify visual fit.
- Tangent: `internal/server/plugin_routes.go` and
  `internal/pluginhost/http.go` implement buffered GET/POST only.
  `internal/server/plugin_registry.go` returns an empty UI registry and
  `ui/src/lib/plugin-loader.ts` is a minimal loading proof, not slot rendering.
  Host foundation tasks -0062/-0063/-0064 and capability task -0067 remain
  explicit dependencies.
