-- Durable presence for the relay's cooperative MCP inbox (CW-20260906-0066),
-- per the CW-20260907-0016 spike's item 3: "a presence fact the UI can show
-- honestly: 'a receive/await is open on this destination now' plus
-- last_seen_at, set server-side while the long-poll is open."
--
-- Only last_seen_at is durable, and only that half. "A receive/await is
-- open right now" is a fact about a currently-blocked HTTP request in this
-- process — it cannot survive a restart, because a restart is exactly the
-- boundary at which "right now" stops being knowable, and claiming
-- otherwise would be the false continuity ADR 0006 §6 rules out ("an agent
-- that is not awaiting is, honestly, awaiting-peer"). That half lives in
-- memory in internal/relay.Store and is deliberately not a column here.
-- last_seen_at is different: "when did we last hear from this destination"
-- is true independent of this process's own lifetime, and worth keeping
-- across a restart the way any other durable fact in this schema is.
--
-- A new table rather than a column on participants: this task does not
-- touch that table's shape, and last_seen_at is exactly the kind of
-- high-write-frequency field (touched on every receive and every ack) that
-- benefits from living apart from a row otherwise written only at identity
-- resolution.
--
-- Mutable by design — no immutable_update/immutable_delete guard here,
-- unlike this migration's neighbors. It is not audited event history; it is
-- one current fact per participant, upserted on every touch.
CREATE TABLE participant_presence (
  participant_id TEXT PRIMARY KEY REFERENCES participants(id) ON DELETE CASCADE,
  last_seen_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
