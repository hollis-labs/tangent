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
  i.id AS interaction_id
FROM envelopes e
JOIN rooms r ON r.id = e.room_id
LEFT JOIN surfaces s ON s.legacy_room_id = r.id
LEFT JOIN interactions i
  ON i.legacy_room_id = e.room_id
  AND i.legacy_envelope_id = e.envelope_id;

DROP INDEX IF EXISTS idx_interactions_legacy_room;
DROP TRIGGER IF EXISTS terminal_outcome_acknowledgements_immutable_delete;
DROP TRIGGER IF EXISTS terminal_outcome_acknowledgements_immutable_update;
DROP TABLE IF EXISTS terminal_outcome_acknowledgements;
