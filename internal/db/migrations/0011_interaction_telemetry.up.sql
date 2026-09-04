-- End-to-end interaction correlation and payload-safe telemetry
-- (CW-20260825-0078), implementing docs/adr/0002-retention-and-draft-custody.md
-- §8.
--
-- Why a new table rather than a query path over the existing `*_events`
-- journals.
--
-- `surface_events`, `interaction_events`, and `delivery_events` are lifecycle
-- journals. Each row is written inside the same transaction as the state change
-- it describes, its shape is the record's shape, and it is protected by an
-- immutability trigger. Three properties make them the wrong home for
-- telemetry:
--
--   1. A telemetry write must never be able to fail the operation it observes.
--      Writing into a lifecycle journal means writing inside that journal's
--      transaction, which is exactly that failure mode.
--   2. Half of what criterion 2 asks about has no lifecycle row at all. A
--      resolver-lease refusal, a failed presentation compare-and-set, a
--      capability denial, and a renderer that is unavailable are all real
--      operational facts and none of them advances a record's revision, so
--      none of them can be represented as a transition event.
--   3. A lifecycle journal has no trace identity and cannot acquire one
--      without altering an immutable audit table.
--
-- So this table is separate, append-only, and deliberately best-effort: a row
-- that fails to be written costs an observation, never an interaction.
--
-- What it may contain is governed by ADR 0002 §8 and enforced in Go by
-- internal/telemetry's attribute allowlist, not by this schema. The schema
-- carries the shape; the allowlist is what makes the shape safe. There is no
-- column here that can hold a payload, a participant's words, a filesystem
-- path, a session, an effect handle, or an assembled URL — the absence is the
-- design, and `attributes` is bounded to allowlisted scalar keys rather than
-- being a general JSON escape hatch.
--
-- `trace_id` is derived, not carried. internal/telemetry computes it from the
-- caller scope plus the idempotency key, both of which are columns on
-- `interactions`, so any process at any later time can recompute the same
-- identity for the same logical request without anything having propagated it.

CREATE TABLE telemetry_events (
  event_id TEXT PRIMARY KEY,

  -- W3C-shaped correlation. trace_id is 32 lowercase hex characters and
  -- span_id 16, so a row is exportable as an OpenTelemetry span without
  -- reformatting. parent_span_id is NULL for a root observation.
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  parent_span_id TEXT,

  -- event_name is a closed vocabulary owned by internal/telemetry. outcome is
  -- one of ok / refused / failed. code is a *typed* code — a refusal code, an
  -- error code, a state name — and never a message: ADR 0002 §8 classifies
  -- adapter freeform text as caller payload precisely so that an
  -- implementation reading this column does not reach for error_message.
  event_name TEXT NOT NULL,
  outcome TEXT NOT NULL,
  code TEXT NOT NULL DEFAULT '',

  occurred_at TIMESTAMP NOT NULL,
  -- duration_ms is NULL for an instantaneous observation. It is the only
  -- measurement stored per row; distributions are aggregated in-process.
  duration_ms INTEGER,

  -- The correlation dimensions, promoted to columns because they are what a
  -- query filters on. Every one of them is a Tangent identifier or a
  -- host-assigned label.
  surface_id TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL DEFAULT '',
  room_id TEXT NOT NULL DEFAULT '',
  envelope_id TEXT NOT NULL DEFAULT '',
  connection_id TEXT NOT NULL DEFAULT '',
  definition_kind TEXT NOT NULL DEFAULT '',
  definition_version TEXT NOT NULL DEFAULT '',
  caller_scope TEXT NOT NULL DEFAULT '',
  participant_scope TEXT NOT NULL DEFAULT '',

  -- attributes is a JSON object whose keys are drawn from the allowlist in
  -- internal/telemetry/attributes.go and whose values are bounded scalars.
  -- Anything else is dropped before it reaches this column.
  attributes TEXT NOT NULL DEFAULT '{}'
);

-- The trace index is first because the whole point of the table is answering
-- "show me everything that happened to this one invocation".
CREATE INDEX idx_telemetry_events_trace ON telemetry_events(trace_id, occurred_at);
CREATE INDEX idx_telemetry_events_interaction ON telemetry_events(interaction_id, occurred_at);
CREATE INDEX idx_telemetry_events_name ON telemetry_events(event_name, occurred_at);
CREATE INDEX idx_telemetry_events_occurred ON telemetry_events(occurred_at);

-- Append-only, but not undeletable.
--
-- The other audit tables in this schema abort DELETE as well as UPDATE,
-- because deleting a resolution destroys the answer a human gave. Nothing here
-- is anyone's answer: these rows are observations about operations, they carry
-- no content by construction, and they accumulate for as long as the process
-- runs. Forbidding DELETE would make unbounded growth a schema-level
-- guarantee on a single-user machine. UPDATE stays forbidden, which is what
-- append-only actually means, and retention is bounded by
-- internal/telemetry's prune.
CREATE TRIGGER telemetry_events_append_only
BEFORE UPDATE ON telemetry_events
BEGIN
  SELECT RAISE(ABORT, 'telemetry events are append-only');
END;
