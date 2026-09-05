-- Host-mediated effect handles and receipts (CW-20260825-0077), implementing
-- docs/adr/0003-definition-and-package-ownership.md §2.5 at runtime.
--
-- ADR 0003 §2.5 made `required_capabilities` and `granted_capabilities` part
-- of the definition manifest and said "a path string is not a capability". It
-- stopped short of saying what a renderer names instead. These two tables are
-- the answer: a *handle* replaces the path string, and a *receipt* replaces
-- the silence that followed an effect.
--
-- Why two tables and not one:
--
--   * A handle is a live grant. It expires, it is spent, it is revoked. It is
--     mutable by design, and its row is deleted by retention like any other
--     short-lived record.
--   * A receipt is history. It is written on every request, granted and
--     refused alike — an audit trail that records only successes cannot answer
--     the question anyone asks it — and it is immutable, guarded by the same
--     trigger pattern definition_bindings has used since 0003.
--
-- What is deliberately NOT stored:
--
--   * No content, in either table. A receipt carries a byte count and a
--     SHA-256; a digest is an identity, not a payload (ADR 0002 §8).
--   * No participant session id, anywhere. The session is the only capability
--     material in the system and lives in exactly two places (ADR 0004 §6.1);
--     a receipt records a *scope*.
--   * No path on a receipt. effect_handles.relative_path exists because it is
--     what a handle *is*; a receipt names the handle instead, so the densest
--     and most-read table in the process holds no filesystem material at all.
--
-- The handle id is a locator, not a credential — the same shape ADR 0004 §5
-- chose for room URLs. Possessing one grants nothing: every use re-checks the
-- participant session, the interaction, and the pinned binding digest. That is
-- what lets a handle travel in a renderer payload without breaking ADR 0002's
-- rule that no short-lived grant reaches a renderer or browser storage.

CREATE TABLE effect_handles (
  id TEXT PRIMARY KEY,
  class TEXT NOT NULL,
  root_id TEXT NOT NULL DEFAULT '',
  relative_path TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL,
  participant_scope TEXT NOT NULL,
  binding_digest TEXT NOT NULL DEFAULT '',
  capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities)),
  issued_at DATETIME NOT NULL,
  expires_at DATETIME NOT NULL,
  max_uses INTEGER NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
  used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
  revoked_at DATETIME
);

-- Expiry sweeps and per-interaction revocation are the only two scans.
CREATE INDEX idx_effect_handles_live
  ON effect_handles(expires_at)
  WHERE revoked_at IS NULL;
CREATE INDEX idx_effect_handles_interaction
  ON effect_handles(interaction_id);

CREATE TABLE effect_receipts (
  id TEXT PRIMARY KEY,
  capability TEXT NOT NULL,
  decision TEXT NOT NULL CHECK (decision IN ('granted', 'refused')),
  code TEXT NOT NULL DEFAULT '',
  mediation TEXT NOT NULL CHECK (mediation IN ('host', 'declared', 'unimplemented')),
  handle_id TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL,
  participant_scope TEXT NOT NULL,
  binding_digest TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  intent_control_id TEXT NOT NULL DEFAULT '',
  intent_revision INTEGER NOT NULL DEFAULT 0,
  issued_at DATETIME NOT NULL,
  byte_count INTEGER NOT NULL DEFAULT 0,
  content_sha256 TEXT NOT NULL DEFAULT '',
  media_type TEXT NOT NULL DEFAULT ''
);

-- One decision per (key, capability). The uniqueness is the idempotency: a
-- replay finds the row rather than acting twice, and a key reused for a
-- materially different request is refused by comparing request_digest.
CREATE UNIQUE INDEX idx_effect_receipts_idempotency
  ON effect_receipts(idempotency_key, capability);
CREATE INDEX idx_effect_receipts_interaction
  ON effect_receipts(interaction_id, issued_at);

-- Receipts are immutable, for the same reason resolutions and definition
-- bindings are: a record of what a host was made to do is worth nothing if the
-- host can edit it afterwards. Retention deletes whole rows under ADR 0002 §6;
-- nothing rewrites one.
CREATE TRIGGER effect_receipts_immutable_update
BEFORE UPDATE ON effect_receipts
BEGIN
  SELECT RAISE(ABORT, 'effect_receipts rows are immutable');
END;
