-- Reverse 0012: put the RESTRICT reference back and drop the audit table.
--
-- Rolling this back restores the state ADR 0002 §6 calls a defect: a surface
-- opened through the async path becomes undeletable again, and its
-- `request_snapshot` becomes an unreachable second copy of the caller payload.
-- The rollback exists because every migration here has one, not because
-- running it is advisable.

DROP TRIGGER surface_open_requests_immutable_update;
DROP TRIGGER surface_open_requests_immutable_delete;

CREATE TABLE surface_open_requests_v1 (
  caller_scope TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  surface_id TEXT NOT NULL UNIQUE REFERENCES surfaces(id) ON DELETE RESTRICT,
  request_snapshot TEXT NOT NULL CHECK (json_valid(request_snapshot)),
  created_at DATETIME NOT NULL,
  PRIMARY KEY (caller_scope, idempotency_key)
);

INSERT INTO surface_open_requests_v1
  (caller_scope, idempotency_key, surface_id, request_snapshot, created_at)
SELECT caller_scope, idempotency_key, surface_id, request_snapshot, created_at
FROM surface_open_requests;

DROP TABLE surface_open_requests;

ALTER TABLE surface_open_requests_v1 RENAME TO surface_open_requests;

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

DROP TRIGGER retention_operations_immutable_update;
DROP TRIGGER retention_operations_immutable_delete;
DROP TABLE retention_operations;
