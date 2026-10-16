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

-- One row: the producer's restart counter (generation, random and nonzero,
-- minted here so a restored or recreated database gets a different one) and the
-- last envelope sequence handed out. A counter, not MAX(rowid), so a sequence is
-- never reused even if appended rows are purged.
CREATE TABLE event_outbox_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  generation INTEGER NOT NULL CHECK (generation > 0),
  last_sequence INTEGER NOT NULL DEFAULT 0
);
INSERT INTO event_outbox_state (id, generation) VALUES (1, (abs(random()) % 9007199254740990) + 1);
