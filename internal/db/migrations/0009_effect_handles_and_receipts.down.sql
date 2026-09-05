-- Rolling back drops every live handle and the whole effect audit trail.
--
-- Dropping the handles is correct: a handle is a grant, and a grant that
-- outlives the schema enforcing it is authority nobody is checking. Dropping
-- the receipts is a real loss of history, which is what a rollback of an audit
-- table always is; it is preferable to leaving rows behind that a later
-- re-migration would silently reinterpret.

DROP TRIGGER IF EXISTS effect_receipts_immutable_update;
DROP INDEX IF EXISTS idx_effect_receipts_interaction;
DROP INDEX IF EXISTS idx_effect_receipts_idempotency;
DROP TABLE IF EXISTS effect_receipts;
DROP INDEX IF EXISTS idx_effect_handles_interaction;
DROP INDEX IF EXISTS idx_effect_handles_live;
DROP TABLE IF EXISTS effect_handles;
