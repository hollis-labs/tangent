ALTER TABLE interactions ADD COLUMN participant_scope TEXT;
ALTER TABLE interactions ADD COLUMN participant_authority TEXT;
ALTER TABLE interactions ADD COLUMN participant_assurance TEXT;
ALTER TABLE draft_revisions ADD COLUMN participant_scope TEXT;
ALTER TABLE draft_revisions ADD COLUMN participant_authority TEXT;
ALTER TABLE draft_revisions ADD COLUMN participant_assurance TEXT;
ALTER TABLE resolutions ADD COLUMN participant_scope TEXT;

CREATE TABLE surface_open_requests (
  caller_scope TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  surface_id TEXT NOT NULL UNIQUE REFERENCES surfaces(id) ON DELETE RESTRICT,
  request_snapshot TEXT NOT NULL CHECK (json_valid(request_snapshot)),
  created_at DATETIME NOT NULL,
  PRIMARY KEY (caller_scope, idempotency_key)
);

CREATE TRIGGER surface_open_requests_immutable_update
BEFORE UPDATE ON surface_open_requests
BEGIN
  SELECT RAISE(ABORT, 'surface open requests are immutable');
END;

CREATE TRIGGER surface_open_requests_immutable_delete
BEFORE DELETE ON surface_open_requests
BEGIN
  SELECT RAISE(ABORT, 'surface open requests are immutable');
END;
