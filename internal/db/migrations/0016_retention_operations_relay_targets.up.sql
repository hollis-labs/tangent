-- Retention for the relay journal (CW-20260907-0043).
--
-- `retention_operations` has named its target with `target_surface_id` and
-- `target_interaction_id` since migration 0012, because those were the only
-- two scopes payload redaction had. CW-20260907-0043 adds two more: an
-- exchange (the relay journal's leaf, content-bearing record — the exact
-- role an interaction plays for surfaces) and a channel (the parent scope a
-- redaction can name all of, the role a surface plays for interactions).
--
-- Two new nullable-by-default text columns, not a rename or a generic
-- `target_id` column: `target_surface_id` holding a channel id, or
-- `target_interaction_id` holding an exchange id, would be indistinguishable
-- from an actual surface or interaction id in every existing query and
-- index over this table — both id spaces are plain uuid.NewString() with no
-- distinguishing prefix. An audit log that cannot say which kind of id a
-- column holds is not the honest record ADR 0002 §6.3 asks for, so this
-- follows 0010's own precedent (ALTER TABLE ADD COLUMN ... DEFAULT '') for
-- widening an audit table without disturbing a row it already wrote.
--
-- No guard is touched here. The redaction path this table's new columns
-- serve suspends only exchanges_immutable_update and
-- exchange_delivery_receipts_immutable_update, and migration 0014 already
-- created both.
ALTER TABLE retention_operations ADD COLUMN target_channel_id TEXT NOT NULL DEFAULT '';
ALTER TABLE retention_operations ADD COLUMN target_exchange_id TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_retention_operations_channel ON retention_operations(target_channel_id, requested_at);
CREATE INDEX idx_retention_operations_exchange ON retention_operations(target_exchange_id, requested_at);
