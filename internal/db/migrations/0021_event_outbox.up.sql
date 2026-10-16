-- Transactional outbox for typed events (CW-20261016-0143). A lifecycle
-- transition writes its event row in the same transaction as its journal row;
-- a relay later appends it to the durable log and stamps appended_at.
CREATE TABLE event_outbox (
  event_id TEXT PRIMARY KEY,
  stream TEXT NOT NULL DEFAULT 'tangent/approvals',
  kind TEXT NOT NULL,
  subject_type TEXT,
  subject_id TEXT,
  envelope_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  appended_at TEXT NULL
);
CREATE INDEX event_outbox_pending ON event_outbox (created_at) WHERE appended_at IS NULL;
CREATE INDEX event_outbox_subject ON event_outbox (subject_id, kind);
