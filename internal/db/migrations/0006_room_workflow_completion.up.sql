-- Caller acknowledgement is its own durable fact. Retrieval
-- (terminal_outcome_retrievals) proves a caller read an outcome, and delivery
-- state proves Tangent handed one to a destination; neither is a statement
-- that the caller took responsibility for the result. Acknowledgement is that
-- statement, it is idempotent, and it is recorded exactly once per
-- interaction.
CREATE TABLE terminal_outcome_acknowledgements (
  interaction_id TEXT PRIMARY KEY REFERENCES interactions(id) ON DELETE CASCADE,
  acknowledgement_id TEXT NOT NULL UNIQUE,
  resolution_id TEXT REFERENCES resolutions(id) ON DELETE CASCADE,
  requester_scope TEXT NOT NULL,
  transport_correlation TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(transport_correlation)),
  acknowledged_at DATETIME NOT NULL
);

CREATE TRIGGER terminal_outcome_acknowledgements_immutable_update
BEFORE UPDATE ON terminal_outcome_acknowledgements
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome acknowledgements are immutable');
END;

CREATE TRIGGER terminal_outcome_acknowledgements_immutable_delete
BEFORE DELETE ON terminal_outcome_acknowledgements
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome acknowledgements are immutable');
END;

-- Restart reconstruction reads every still-open interaction that belongs to a
-- legacy room so the room UI can be rebuilt from canonical records rather than
-- from process-local channels.
CREATE INDEX idx_interactions_legacy_room
  ON interactions(legacy_room_id, lifecycle_state)
  WHERE legacy_room_id IS NOT NULL;

-- The legacy compatibility projection now also carries the canonical lifecycle
-- state and the acknowledgement fact, so a reader can see that a legacy
-- "pending" row is merely an un-updated projection of a resolved interaction
-- rather than an independent claim about the outcome.
DROP VIEW IF EXISTS legacy_room_history_v12;
CREATE VIEW legacy_room_history_v12 AS
SELECT
  e.room_id,
  e.envelope_id,
  e.type,
  e.request_payload,
  e.response_kind,
  e.response_payload,
  e.status,
  e.error_code,
  e.error_message,
  e.created_at,
  e.resolved_at,
  s.id AS surface_id,
  i.id AS interaction_id,
  i.lifecycle_state AS interaction_state,
  a.acknowledged_at AS caller_acknowledged_at
FROM envelopes e
JOIN rooms r ON r.id = e.room_id
LEFT JOIN surfaces s ON s.legacy_room_id = r.id
LEFT JOIN interactions i
  ON i.legacy_room_id = e.room_id
  AND i.legacy_envelope_id = e.envelope_id
LEFT JOIN terminal_outcome_acknowledgements a
  ON a.interaction_id = i.id;
