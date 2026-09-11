-- US-English cancellation vocabulary for the persisted protocol error code
-- (CW-20260904-0168).
--
-- go-envelopes moved the protocol token to the US spelling: the error code
-- "user-cancelled" is now "user-canceled". v0.4.x keeps the British value
-- wire-stable and readable; v0.5.0 REMOVES it. This migration rewrites the
-- rows Tangent already persisted so the read paths are not carrying a
-- compatibility shim forever. Nanite's 148_us_english_canceled_status.sql is
-- the precedent for the shape.
--
-- WHAT MOVES — the three places a protocol error code can be persisted:
--
--   1. envelopes.error_code, written by persistCancelledEnvelope and by
--      persistTerminalEnvelopeError's context.Canceled arm.
--
--   2. envelopes.response_payload, which carries its own copy of the code
--      inside the error body that same context.Canceled arm marshals. It is
--      rewritten as a replace() of the exact machine-written token rather
--      than a json_set(), for the reason 148 records about message content:
--      a narrow rewrite of a token this code wrote is a migration, and a
--      general sweep of a free-form column is silent corruption of somebody
--      else's text. The response_kind = 'error' guard keeps it to the rows
--      that path produces.
--
-- Two columns, not three. interactions.terminal_error_code can also hold the
-- legacy value — migration 0003's backfill copies envelopes.error_code forward
-- for every row whose status is not submitted/partial/cancelled, and the
-- context.Canceled arm writes status 'error' with code 'user-cancelled'. It is
-- deliberately NOT rewritten here; see below.
--
-- WHAT DELIBERATELY DOES NOT MOVE:
--
--   * envelopes.status = 'cancelled'. That is Tangent's OWN
--     envelopeStatusCancelled constant (internal/room/store.go), not the
--     protocol's ResponseStatus, and the two are only coincidentally spelled
--     alike. Rewriting it would be a change to a vocabulary go-envelopes does
--     not own, and it would break historyResponseStatus, which reads that
--     column against Tangent's constant on purpose. The read path maps both
--     that value and the protocol's current 'canceled' onto a canonical
--     status, so nothing downstream can tell the difference.
--
--   * interactions.policy's legacy_error_code member. That JSON blob is 0003's
--     archival record of what a v0.12 row's error code WAS at the moment it
--     was migrated. Rewriting a provenance record to say something the row
--     never said destroys the record rather than migrating it.
--
--   * interactions.terminal_error_code. A terminal interaction is immutable by
--     design — the interactions_terminal_immutable guard 0003 installs aborts
--     any update to a resolved/canceled/expired/failed/superseded row, and it
--     aborted the first draft of this migration. Migrations here do suspend
--     guards when they must (0005, 0007, 0012 all do), so this is a choice
--     rather than an obstacle: suspending an audit record's immutability to
--     correct a spelling is a bad trade. Nothing live writes this value, the
--     rows that hold it were written by 0003's backfill, and what a terminal
--     interaction recorded is exactly the thing that is supposed to stay put.
--     roomflow canonicalizes it on the way out instead, which is what
--     CanonicalErrorCode is for: presenting a legacy value canonically without
--     rewriting the record that earned it.
--
-- Idempotent by construction: every statement is guarded on the old value, so
-- re-running finds nothing to do.

UPDATE envelopes
   SET error_code = 'user-canceled'
 WHERE error_code = 'user-cancelled';

UPDATE envelopes
   SET response_payload = replace(response_payload, '"code":"user-cancelled"', '"code":"user-canceled"')
 WHERE response_kind = 'error'
   AND response_payload LIKE '%"code":"user-cancelled"%';
