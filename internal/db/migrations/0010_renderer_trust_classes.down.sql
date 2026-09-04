-- Rolling back drops the two columns that made `mediation` interpretable.
--
-- The receipts themselves survive, which is the right trade for an audit
-- table: losing the isolation beside a decision is a loss of context, and
-- losing the decision would be a loss of the record. A re-migration cannot
-- reconstruct these values — the definition registry may have moved on — so
-- rows written before the rollback come back with the empty-string default
-- and read as "not recorded", which is what they will honestly be.

ALTER TABLE effect_receipts DROP COLUMN renderer_isolation;
ALTER TABLE effect_receipts DROP COLUMN renderer_trust_class;
