-- The relay journal and its transactional outbox (CW-20260906-0065),
-- implementing ARCHITECTURE.md §5's Exchange Contract and
-- docs/adr/0006-collaboration-surface-and-relay-boundary.md §4: "Tangent's
-- relay journal is authoritative for what Tangent accepted and delivered,
-- and for nothing else."
--
-- Four tables, one fact each, deliberately not conflated:
--
--   exchanges                  the message fact. Immutable once accepted —
--                               both triggers below refuse UPDATE and DELETE
--                               unconditionally, the same shape
--                               retention_operations uses, because nothing
--                               about an accepted exchange should ever change.
--   exchange_outbox            the delivery-workflow fact: one mutable row
--                               per exchange, a small lease-based state
--                               machine (pending -> leased -> delivered/
--                               failed, with an explicit requeue back to
--                               pending). This is the transactional-outbox
--                               half of acceptance: a caller that accepts an
--                               exchange without a delivery-work row queued
--                               for it is exactly the bug this pattern exists
--                               to make impossible, so the insert into this
--                               table happens in the same transaction as the
--                               insert into exchanges.
--   exchange_delivery_receipts the reconciliation fact: what actually
--                               happened on each delivery attempt, append-
--                               only, immutable, separate from the outbox's
--                               current-state pointer the way surface_events
--                               is separate from surfaces.
--   exchange_reads              the read fact: retrieval is not
--                               acknowledgement (the same line
--                               terminal_outcome_retrievals /
--                               terminal_outcome_acknowledgements already
--                               draw for interactions), so an agent
--                               `receive`-ing a message and an agent `ack`-ing
--                               it are two different, separately recorded
--                               events.
--
-- Draft and attention facts are deliberately not tables here. Attention is
-- explicitly a later task (CW-20260906-0067, "attention unification") and
-- ADR 0006 §3 defines it as a projection over existing facts, not a second
-- request record — building it now would be exactly the request-record
-- duplication the ADR warns against. No channel-draft feature has been
-- scoped yet by any task in this plan; inventing draft storage with no
-- consumer would be schema with no reader, which this repo's conventions
-- reject. "Separate draft/message/delivery/read/attention facts" is honored
-- here as a design discipline — these four tables never conflate message,
-- delivery, and read into one blob — not as a demand to build all five.
--
-- Every foreign key below was checked against internal/db.PurgeSurface
-- before this migration's tests were written, per the standing rule from
-- CW-20260906-0064's review: PurgeSurface only ever deletes rows reachable
-- from a surface (surfaces, interactions, rooms, and their existing
-- cascades), and none of channels, participants, channel_subjects, or
-- channel_participant_bindings are reachable from a surface — channel_id,
-- sender/recipient participant ids, and recipient_binding_id therefore never
-- see an FK action fire during a purge. The one route a purge does reach is
-- exchanges.subject_id -> channel_subjects(id), and channel_subjects itself
-- is never deleted by a purge (only its own interaction_id/surface_id go
-- NULL, per migration 0013) — so exchanges.subject_id is never touched
-- either. internal/db/retention_test.go carries the proof.

CREATE TABLE exchanges (
  id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  subject_id TEXT REFERENCES channel_subjects(id) ON DELETE SET NULL,
  sender_participant_id TEXT NOT NULL REFERENCES participants(id),
  recipient_participant_id TEXT NOT NULL REFERENCES participants(id),
  -- The exact runtime-binding generation live at accept time, frozen here
  -- (ADR 0006 §3, "explicit destination binding generation"). NULL means no
  -- live binding existed when the exchange was accepted — a legitimate
  -- "queued, awaiting-peer" state, not an error. channel_participant_bindings
  -- rows are themselves never deleted (0064), so SET NULL here is dormant
  -- today; it documents the same "may outlive its target" contract every
  -- other optional reference in this schema uses.
  recipient_binding_id TEXT REFERENCES channel_participant_bindings(id) ON DELETE SET NULL,
  reply_to_exchange_id TEXT REFERENCES exchanges(id) ON DELETE SET NULL,
  body TEXT NOT NULL,
  -- The replay cursor. Monotonic per recipient_participant_id, assigned
  -- inside the same transaction as the row's own insert. A caller's
  -- `receive(cursor, wait_ms)` resumes with `sequence > cursor`; the cursor
  -- itself is never persisted server-side per destination — the caller holds
  -- it, the same stateless-cursor shape the rest of this codebase uses.
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  created_at DATETIME NOT NULL,
  UNIQUE (sender_participant_id, idempotency_key),
  UNIQUE (recipient_participant_id, sequence)
);

CREATE INDEX idx_exchanges_channel ON exchanges(channel_id, created_at);
CREATE INDEX idx_exchanges_subject ON exchanges(subject_id) WHERE subject_id IS NOT NULL;

CREATE TRIGGER exchanges_immutable_update
BEFORE UPDATE ON exchanges
BEGIN
  SELECT RAISE(ABORT, 'exchanges are immutable once accepted');
END;

CREATE TRIGGER exchanges_immutable_delete
BEFORE DELETE ON exchanges
BEGIN
  SELECT RAISE(ABORT, 'exchanges are immutable once accepted');
END;

-- One outbox row per exchange, using the exchange's own id as this table's
-- primary key. That is what makes "insert the exchange and its delivery
-- work atomically" and "a retry can never duplicate the outbox row for the
-- same exchange" the same structural fact rather than two things a caller
-- has to get right independently.
CREATE TABLE exchange_outbox (
  exchange_id TEXT PRIMARY KEY REFERENCES exchanges(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'leased', 'delivered', 'failed')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  leased_by TEXT NOT NULL DEFAULT '',
  leased_at DATETIME,
  lease_expires_at DATETIME,
  next_attempt_at DATETIME,
  last_error TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  -- A crash boundary is exactly "leased_at was set and nothing ever cleared
  -- it": this CHECK holds only the shape (leased carries both timestamps,
  -- everything else carries neither), so it can never conflict with an FK
  -- action the way 0064's CHECK did — no foreign key touches these columns.
  CHECK (
    (status = 'leased' AND leased_at IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status != 'leased' AND leased_at IS NULL AND lease_expires_at IS NULL)
  )
);

CREATE INDEX idx_exchange_outbox_claimable ON exchange_outbox(status, lease_expires_at);

CREATE TABLE exchange_delivery_receipts (
  id TEXT PRIMARY KEY,
  exchange_id TEXT NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
  -- Three outcomes, not two: "uncertain" (a timeout or a transport error with
  -- no confirmation either way) is not the same fact as "failed" (the peer
  -- or transport gave a definite negative), and collapsing them is exactly
  -- what would let a timeout be silently treated as a permanent failure or a
  -- success. ARCHITECTURE.md §5: "UI states distinguish queued,
  -- awaiting-peer, accepted-by-peer, failed/unknown."
  outcome TEXT NOT NULL CHECK (outcome IN ('delivered', 'failed', 'uncertain')),
  runtime_authority TEXT NOT NULL DEFAULT '',
  runtime_endpoint_ref TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  attempted_at DATETIME NOT NULL,
  UNIQUE (exchange_id, attempt_number)
);

CREATE INDEX idx_exchange_delivery_receipts_exchange ON exchange_delivery_receipts(exchange_id, attempt_number);

CREATE TRIGGER exchange_delivery_receipts_immutable_update
BEFORE UPDATE ON exchange_delivery_receipts
BEGIN
  SELECT RAISE(ABORT, 'exchange delivery receipts are immutable');
END;

CREATE TRIGGER exchange_delivery_receipts_immutable_delete
BEFORE DELETE ON exchange_delivery_receipts
BEGIN
  SELECT RAISE(ABORT, 'exchange delivery receipts are immutable');
END;

-- The read fact. Recording it twice for the same (exchange, participant) is
-- a no-op, not an error — an agent that calls ack after a retry, or after
-- already acking, is not making a false claim the second time.
CREATE TABLE exchange_reads (
  exchange_id TEXT NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL REFERENCES participants(id),
  acked_at DATETIME NOT NULL,
  PRIMARY KEY (exchange_id, participant_id)
);

CREATE TRIGGER exchange_reads_immutable_update
BEFORE UPDATE ON exchange_reads
BEGIN
  SELECT RAISE(ABORT, 'exchange reads are immutable');
END;

CREATE TRIGGER exchange_reads_immutable_delete
BEFORE DELETE ON exchange_reads
BEGIN
  SELECT RAISE(ABORT, 'exchange reads are immutable');
END;
