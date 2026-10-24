-- Artifact inbox side tables (ADR 0015, CW-20261024-0061).
--
-- An artifact is an interaction of kind tangent.artifact-item whose request
-- snapshot is immutable identity. Everything that changes after it is sent
-- lives here: one row per revision (the file itself is on disk, referenced by
-- a path relative to the artifact directory) and one row per message in the
-- operator/agent thread. Like docs_read_receipts these carry no foreign key
-- because interaction rows are never deleted.

CREATE TABLE artifact_revisions (
  interaction_id TEXT    NOT NULL,
  revision       INTEGER NOT NULL CHECK (revision > 0),
  filename       TEXT    NOT NULL,
  media_type     TEXT    NOT NULL,
  size_bytes     INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256         TEXT    NOT NULL,
  rel_path       TEXT    NOT NULL,
  note           TEXT    NOT NULL DEFAULT '',
  created_at     DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, revision)
);

CREATE TABLE artifact_messages (
  interaction_id TEXT    NOT NULL,
  seq            INTEGER NOT NULL CHECK (seq > 0),
  author         TEXT    NOT NULL CHECK (author IN ('operator', 'agent')),
  kind           TEXT    NOT NULL CHECK (kind IN ('message', 'changes_requested', 'approved')),
  body           TEXT    NOT NULL,
  revision       INTEGER NOT NULL DEFAULT 0,
  created_at     DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, seq)
);
