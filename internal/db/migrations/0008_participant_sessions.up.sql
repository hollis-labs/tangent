-- Authenticated browser participant sessions (CW-20260825-0075),
-- implementing docs/adr/0004-caller-participant-and-room-access-authority.md §4.
--
-- Before this table, a room UUID was the only room authority: `/ws?roomID=`
-- plus a known id was sufficient to view, answer, and cancel. The URL is
-- deliberately published — it is returned by session_create, logged when a
-- workflow opens a room, and pasted into agent transcripts by design — so a
-- credential the product's own ergonomics require it to broadcast is not a
-- credential. This table moves authority off the URL and onto a server-held
-- session named by an HttpOnly cookie.
--
-- What is stored, and what deliberately is not:
--
--   * cookie_sha256 is the SHA-256 of the cookie value, hex encoded. The
--     cookie value itself is never stored. It is the only capability material
--     in the system (ADR 0004 §6) and exists in exactly two places: the
--     Set-Cookie / Cookie header, and this column.
--   * No headers, no payloads, no request bodies, no URLs. A participant
--     session records who may act, never what they looked at.
--
-- Lifetime is ADR 0004 §4: no idle expiry — a local single-user tool must not
-- log its user out mid-decision — with an absolute maximum, rotation on
-- assurance change, and revocation through an explicit CLI flag. rotated_to
-- links a revoked session to its successor so an audit trail survives the
-- rotation that an identity authority triggers when it binds a verified
-- principal.
--
-- Retention follows ADR 0002 like any other durable record.

CREATE TABLE participant_sessions (
  id TEXT PRIMARY KEY,
  cookie_sha256 TEXT NOT NULL UNIQUE,
  participant_scope TEXT NOT NULL,
  participant_ref TEXT NOT NULL,
  participant_authority TEXT NOT NULL,
  participant_assurance TEXT NOT NULL,
  capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities)),
  created_at DATETIME NOT NULL,
  last_seen_at DATETIME NOT NULL,
  expires_at DATETIME NOT NULL,
  revoked_at DATETIME,
  revoked_reason TEXT,
  rotated_to TEXT REFERENCES participant_sessions(id) ON DELETE SET NULL
);

-- Lookup is always by cookie hash; the unique index above already serves it.
-- This index serves expiry sweeps and the revocation CLI, which walk live
-- sessions in creation order.
CREATE INDEX idx_participant_sessions_live
  ON participant_sessions(expires_at)
  WHERE revoked_at IS NULL;
