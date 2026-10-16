-- Durable event journal behind streamhub.Log (CW-20261016-0143).
--
-- stream_log_meta holds the journal epoch. It is minted by the application the
-- first time the journal is opened and again whenever the journal is reset, so
-- a cursor issued before a restore or reset is recognisable as foreign.
CREATE TABLE stream_log_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- One row per stream: the highest seq ever assigned and the highest seq
-- removed by retention (records with seq <= pruned_through are gone).
CREATE TABLE stream_log_streams (
  stream         TEXT PRIMARY KEY,
  latest_seq     INTEGER NOT NULL DEFAULT 0 CHECK (latest_seq >= 0),
  pruned_through INTEGER NOT NULL DEFAULT 0 CHECK (pruned_through >= 0 AND pruned_through <= latest_seq),
  dropped        INTEGER NOT NULL DEFAULT 0 CHECK (dropped >= 0)
);

-- The journal. seq is dense per stream; event_id is the producer-minted
-- evt_ id and the global dedupe key. recorded_at is assigned by the log in a
-- fixed-width UTC layout so it sorts lexically. envelope is the verbatim
-- payload handed to Append.
CREATE TABLE stream_log_events (
  stream       TEXT NOT NULL REFERENCES stream_log_streams(stream),
  seq          INTEGER NOT NULL CHECK (seq >= 1),
  event_id     TEXT NOT NULL UNIQUE CHECK (event_id <> ''),
  kind         TEXT NOT NULL DEFAULT '',
  subject_type TEXT NOT NULL DEFAULT '',
  subject_id   TEXT NOT NULL DEFAULT '',
  recorded_at  TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  envelope     TEXT,
  PRIMARY KEY (stream, seq)
) WITHOUT ROWID;

CREATE INDEX idx_stream_log_events_recorded ON stream_log_events (stream, recorded_at);
CREATE INDEX idx_stream_log_events_subject ON stream_log_events (subject_type, subject_id);

-- Durable gap records: one row each time retention removes history, so an
-- operator can see what a reconnecting client lost and when.
CREATE TABLE stream_log_gaps (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  stream       TEXT NOT NULL,
  reason       TEXT NOT NULL,
  from_seq     INTEGER NOT NULL,
  through_seq  INTEGER NOT NULL,
  dropped      INTEGER NOT NULL,
  recorded_at  TEXT NOT NULL
);
CREATE INDEX idx_stream_log_gaps_stream ON stream_log_gaps (stream, id);

-- Repair work list for events the emitter could not map (CW-20261016-0143).
-- A transition whose event could not be mapped still commits; this row, written
-- in the same transaction, makes the gap repairable. Error class only, never
-- raw text.
CREATE TABLE event_outbox_skipped (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  item_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  error_class TEXT NOT NULL,
  created_at TEXT NOT NULL,
  repaired_at TEXT NULL
);
CREATE INDEX event_outbox_skipped_open ON event_outbox_skipped (id) WHERE repaired_at IS NULL;
