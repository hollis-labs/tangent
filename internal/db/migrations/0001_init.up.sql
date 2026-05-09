CREATE TABLE rooms (
  id TEXT PRIMARY KEY,
  title TEXT,
  meta TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  closed_at DATETIME,
  closed_reason TEXT
);

CREATE INDEX idx_rooms_active ON rooms(closed_at) WHERE closed_at IS NULL;

CREATE TABLE envelopes (
  room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  envelope_id TEXT NOT NULL,
  type TEXT NOT NULL,
  request_payload TEXT NOT NULL,
  response_kind TEXT,
  response_payload TEXT,
  status TEXT NOT NULL,
  error_code TEXT,
  error_message TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at DATETIME,
  PRIMARY KEY (room_id, envelope_id)
);

CREATE INDEX idx_envelopes_room ON envelopes(room_id, created_at);
