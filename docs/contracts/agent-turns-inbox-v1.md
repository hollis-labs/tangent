# Tangent Agent Turns FIFO Inbox contract v1.0

**Status:** Proposed / Active Slice

**Contract version:** `1.0`

**Definition kind:** `tangent.agent-turn`

**Decision basis:** [ADR 0001](../adr/0001-lifecycle-boundaries.md), [ADR 0005](../adr/0005-product-boundary-and-portfolio-composition.md), Torque task `CW-20260913-0019`

## 1. Status and Intent

This contract defines the second durable FIFO operator inbox in Tangent. It presents running agents' actual turns/responses to the human operator in arrival order. Turns are routed from Tether into Tangent's inbox flow so that operator replies have a durable, recoverable delivery path back to the originating agent session.

Tangent owns the durable presentation surface and strict arrival ordering. Tether owns runtime session identity, turn production, and outbound/inbound delivery to agent processes. Torque owns task tracking. Tesseract provides context references.

---

## 2. Canonical Identity & Mappings

Per Tesseract decision `preferred_item_identity`, identities across systems remain strictly decoupled:

| Concept | Owning System | Invariant |
|---|---|---|
| **`session_id`** | Tether | Canonical agent runtime session. |
| **`turn_id`** / **`turn_sequence`** | Tether | Sequence of the turn within the originating agent session. |
| **`item_id`** / **`interaction_id`** | Tangent | Tangent's durable interaction UUID for this turn. |
| **`queue_sequence`** / **`surface_sequence`** | Tangent | Transactionally monotonic arrival sequence on `surface_turns_default`. |
| **`reply_id`** | Tangent | Immutable operator resolution record UUID. |
| **`task_id`** | Torque | Optional correlation reference (e.g. `CW-20260913-0019`). Stored as an opaque correlation. |

---

## 3. Ingestion & Event Scope

For the initial slice, the inbox admits **attention-required turns and milestones** to protect the operator and database from runaway volume:

1. `question`: The agent is blocked waiting for human input or choice.
2. `approval`: The agent requires operator confirmation before a high-impact action.
3. `checkpoint`: The agent reached a planned pause / inspection gate.
4. `failure`: An unrecoverable tool or workflow error requiring human direction.
5. `terminal`: An agent concluded its task run and published final deliverables.

Intermediate thoughts, internal scratchpad iterations, and automatic tool telemetry do not enter this inbox unless flagged with `requires_operator: true`.

---

## 4. Item Request Contract (`AgentTurnRequestV1`)

Required fields when enqueuing a turn into Tangent. The MCP tool `tangent.turns_enqueue`
takes this request as its arguments, and `POST /api/turns/enqueue` takes it as the body; both
validate against the same schema.

| Field | Type | Description |
|---|---|---|
| `contract_version` | string | Exactly `"1.0"`. |
| `turn_id` | string | Originating Tether turn identifier. |
| `session_id` | string | Originating Tether session identifier. |
| `idempotency_key` | string | Caller-supplied deduplication key (e.g. `tether:<session_id>:<turn_id>`). |
| `kind` | string | One of: `"question"`, `"approval"`, `"checkpoint"`, `"failure"`, `"terminal"`. |
| `source` | object | Contains `agent_id` (required), `application_id`, optional `agent_label`. |
| `title` | string | Short operator-facing summary (1–160 chars). |
| `content` | string | The message, question, or error text (Markdown supported). |

Optional fields:
* `options`: Array of strings or `{ label, value, description }` objects for selectable choices.
* `correlations`: Object containing `task_id`, `project_id`, `runtime_ref`.
* `expires_at`: RFC 3339 timestamp when the waiting turn is considered abandoned or timed out by the runtime.

---

## 5. Tether Delivery, Reply & Recovery Contract

When the operator responds to a turn in Tangent:

1. **Resolution Record:** Tangent commits an immutable `ResolutionRecord` in SQLite:
   * `action`: `"respond"`, `"approve"`, `"reject"`, or `"dismiss"`.
   * `response_text`: Operator's answer, guidance, or selected option value.
   * `resolved_at`: Timestamp.
   * `resolved_by`: Operator identity (`local-operator`).
2. **Delivery State:** The turn interaction's `delivery_state` moves from `queued` to `delivering`.
3. **Delivery Channels:**
   * **Push / Callback:** If Tether registered an active callback or streaming subscriber, Tangent immediately dispatches the reply event.
   * **Durable Pull / Resume:** If the agent disconnected, restarted, or the HTTP connection expired, the agent/Tether polls or resumes via `tangent.turn_await` or `GET /api/turns/sessions/{session_id}/replies`.
     * `tangent.turn_await` takes a `session_id` and an optional `wait_ms` (0–50,000; 30,000 by default) and returns the session's replies that are not yet acknowledged, oldest answer first, as `{ wait_status: "replies" | "timeout", replies: [...] }`. A timeout is a result, not an error, and changes no state. A reply is returned again until it is acknowledged, so delivery is at-least-once.
     * `GET /api/turns/sessions/{session_id}/replies` is the history read: every reply for the session, acknowledged or not.
     * A dismissed turn has no reply and appears in neither.
4. **Acknowledgement:** Once Tether delivers the reply to the agent session, Tether submits `tangent.turn_ack` (or `POST /api/turns/items/{item_id}/ack`) with the turn's `item_id` and, optionally, `reply_id`. Tangent marks `delivery_state = acknowledged`. A `reply_id` that is not that turn's reply is refused, a turn nobody has answered cannot be acknowledged, and repeating an acknowledgement changes nothing.
5. **Session Disappearance:** If Tether confirms the target session has permanently exited before delivery, Tether posts an explicit notification. Tangent records `delivery_state = terminal_failure` with cause `session_offline`. The operator's response is preserved in history and never silently lost.

---

## 6. Ordering, Presentation, and Retention

1. **Strict FIFO Invariant:**
   * SQLite transactionally assigns `surface_sequence` on `surface_turns_default`.
   * The queue is strictly ordered by `surface_sequence ASC` (arrival order).
2. **Presentation Filters (Client-Side / Read-Only):**
   * *Needs Attention:* Pending items where operator action is awaited (`state: presented`).
   * *By Session:* Grouped visual threads per `session_id`, while retaining the global sequence indicator.
   * *Milestones / All:* View toggle between attention-only and complete milestone history.
   * Filtering and priority flags never mutate queue position or arrival sequence.
3. **Mute & Dismiss:**
   * An operator can dismiss a turn without replying. Tangent marks `state = canceled` or `superseded` with cause `participant_dismissed`. History is preserved.
4. **Retention:**
   * Items remain in the active view while unresolved.
   * Resolved items remain accessible in history. Default retention aligns with Tangent host policy (redacted 30 days after terminal).
