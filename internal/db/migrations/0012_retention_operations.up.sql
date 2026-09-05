-- Retention, erasure, and operator maintenance (CW-20260825-0072),
-- implementing docs/adr/0002-retention-and-draft-custody.md §6 and §7.
--
-- Two things land here, and they are the two obstructions that made every
-- deletion story in ADR 0002 unimplementable.
--
-- 1. `retention_operations` — the audit row §6.3 requires.
--
-- Every removal of content, every refusal to remove, and every failure to
-- remove writes exactly one row here. Without it, "the payload is gone" is a
-- claim with no evidence behind it, and "the payload is gone from the live
-- database but not from the backup you took last Tuesday" cannot be said at
-- all.
--
-- The table has NO foreign keys, deliberately. An audit row that recorded the
-- erasure of a surface would cascade away with that surface, which is the one
-- thing it exists not to do. Target identifiers are plain text, and the row
-- outlives its subject by construction.
--
-- It also carries no content, by the same construction telemetry_events uses:
-- there is no column here that can hold a payload. `removed_digests` holds
-- SHA-256 digests of what was removed, which is what §2 keeps so that "was
-- this the payload that produced that resolution?" stays answerable after the
-- content is gone.
--
-- Unlike telemetry_events, this table refuses DELETE as well as UPDATE. The
-- telemetry table permits DELETE because its rows are observations that
-- accumulate without bound and carry nothing anyone said. These rows are the
-- record of an erasure, they are written only when an operator asks for one,
-- and there are as many of them as the operator made requests. Bounded growth
-- is not a reason to make an erasure log erasable.
--
-- 2. `surface_open_requests` moves to ON DELETE CASCADE — ADR 0002 §6, decided
--    in this work rather than deferred.
--
-- The column referenced `surfaces(id) ON DELETE RESTRICT` and the table
-- carried its own immutable-delete trigger, so `request_snapshot` was a
-- permanent second copy of the caller payload that no operation could reach
-- and that blocked deletion of every surface opened through the async path.
-- A request row that outlives its surface has no remaining referent; it is
-- duplicate caller payload and nothing else.
--
-- The trigger stays. Immutability during normal operation is unchanged: this
-- table still refuses UPDATE and still refuses direct DELETE. What changes is
-- that a cascade from a surface the maintenance path is erasing now reaches
-- it, because that path drops the trigger for the duration of one transaction.

-- ── retention_operations ───────────────────────────────────────────────

CREATE TABLE retention_operations (
  operation_id TEXT PRIMARY KEY,

  -- The six kinds are deliberately not one `delete` verb. ADR 0002 and the
  -- acceptance for this work both require that an operator can tell a
  -- capability lapsing from a draft being dropped from a payload being
  -- redacted from a surface being closed from content Tangent never held.
  -- They have different authority and different consequences, and collapsing
  -- them is how a `close` becomes mistaken for an `erase`.
  --
  --   capability-expiry        A live grant is withdrawn or swept. Removes an
  --                            effect_handles row. No participant content.
  --   draft-deletion           A draft revision's payload is replaced by a
  --                            tombstone and draft_revision_tombstones gets
  --                            its first writer.
  --   payload-redaction        A caller request, a participant resolution, or
  --                            adapter freeform text is replaced by the typed
  --                            tombstone of ADR 0002 §2.
  --   surface-close            A lifecycle transition. Removes nothing. It is
  --                            recorded here precisely so that a reader of
  --                            this log can see that it removed nothing.
  --   surface-purge            The cascade delete. The only kind that destroys
  --                            record skeletons, and never automatic.
  --   external-source-deletion Tangent removes nothing and cannot: the content
  --                            was never here. Recorded so the refusal is
  --                            evidence rather than silence.
  kind TEXT NOT NULL CHECK (kind IN (
    'capability-expiry',
    'draft-deletion',
    'payload-redaction',
    'surface-close',
    'surface-purge',
    'external-source-deletion'
  )),

  -- What the operation was aimed at. Both may be empty for a sweep that names
  -- no single target (capability expiry across the whole store, for example).
  target_surface_id TEXT NOT NULL DEFAULT '',
  target_interaction_id TEXT NOT NULL DEFAULT '',

  -- Who asked, and under what authority. `local-user` is an explicit operator
  -- request; `host-policy` is a window expiry the host configured;
  -- `maintenance` is a repair or compaction that touched no content.
  actor_ref TEXT NOT NULL,
  authority TEXT NOT NULL CHECK (authority IN ('local-user', 'host-policy', 'maintenance')),
  policy_ref TEXT NOT NULL DEFAULT '',

  requested_at DATETIME NOT NULL,
  completed_at DATETIME,

  -- `refused` is a first-class outcome, not an error. An operation that
  -- correctly declined — because the target was not eligible, because content
  -- lives outside Tangent, because another process owned the database — is a
  -- fact worth keeping.
  outcome TEXT NOT NULL CHECK (outcome IN ('applied', 'refused', 'failed')),
  code TEXT NOT NULL DEFAULT '',
  affected_rows INTEGER NOT NULL DEFAULT 0 CHECK (affected_rows >= 0),

  -- A JSON array of {table, column, ref, sha256, bytes}. Digests only; the
  -- CHECK is the schema's half of that promise and internal/db's redaction
  -- path is the other half.
  removed_digests TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(removed_digests)),

  -- ADR 0002 §7: removing content from the live database does not remove it
  -- from a backup taken earlier, and the operation records which backups are
  -- known to be affected rather than claiming to have cleaned them.
  --
  -- `backup_survey` is the honest part. `not-surveyed` means nobody looked,
  -- which is the default and is not the same as "there are none".
  known_backups TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(known_backups)),
  backup_survey TEXT NOT NULL DEFAULT 'not-surveyed'
    CHECK (backup_survey IN ('not-surveyed', 'surveyed', 'survey-failed')),

  -- The schema the operation ran against, and whether the immutability guards
  -- were verified back in place when it finished. A zero here on a row whose
  -- kind needed the maintenance path is the signature of the failure mode ADR
  -- 0002 warns about: a database left without its guards.
  schema_version INTEGER NOT NULL DEFAULT 0,
  guards_restored INTEGER NOT NULL DEFAULT 0 CHECK (guards_restored IN (0, 1))
);

CREATE INDEX idx_retention_operations_requested ON retention_operations(requested_at);
CREATE INDEX idx_retention_operations_kind ON retention_operations(kind, requested_at);
CREATE INDEX idx_retention_operations_surface ON retention_operations(target_surface_id, requested_at);
CREATE INDEX idx_retention_operations_interaction ON retention_operations(target_interaction_id, requested_at);

CREATE TRIGGER retention_operations_immutable_update
BEFORE UPDATE ON retention_operations
BEGIN
  SELECT RAISE(ABORT, 'retention operations are immutable');
END;

CREATE TRIGGER retention_operations_immutable_delete
BEFORE DELETE ON retention_operations
BEGIN
  SELECT RAISE(ABORT, 'retention operations are immutable');
END;

-- ── surface_open_requests: RESTRICT -> CASCADE ─────────────────────────
--
-- SQLite cannot alter a foreign key action in place, so this is the standard
-- rebuild. The triggers are dropped first because they belong to the table
-- being dropped; the implicit DELETE that DROP TABLE performs under foreign
-- key enforcement does not fire triggers, and nothing references this table,
-- so the rebuild is contained.

DROP TRIGGER surface_open_requests_immutable_update;
DROP TRIGGER surface_open_requests_immutable_delete;

CREATE TABLE surface_open_requests_v2 (
  caller_scope TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  surface_id TEXT NOT NULL UNIQUE REFERENCES surfaces(id) ON DELETE CASCADE,
  request_snapshot TEXT NOT NULL CHECK (json_valid(request_snapshot)),
  created_at DATETIME NOT NULL,
  PRIMARY KEY (caller_scope, idempotency_key)
);

INSERT INTO surface_open_requests_v2
  (caller_scope, idempotency_key, surface_id, request_snapshot, created_at)
SELECT caller_scope, idempotency_key, surface_id, request_snapshot, created_at
FROM surface_open_requests;

DROP TABLE surface_open_requests;

ALTER TABLE surface_open_requests_v2 RENAME TO surface_open_requests;

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
