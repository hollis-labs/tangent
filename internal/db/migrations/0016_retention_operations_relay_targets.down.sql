-- Rolling back drops the two columns and their indexes. Every audit row
-- written before the rollback survives under target_surface_id and
-- target_interaction_id exactly as it did before this migration; only a
-- channel- or exchange-scoped redaction's target identifier is lost, and
-- only until this migration is reapplied — at which point it comes back as
-- the empty-string default, reading honestly as "not recorded" for rows
-- written in between, the same trade 0010's rollback makes.
DROP INDEX idx_retention_operations_exchange;
DROP INDEX idx_retention_operations_channel;
ALTER TABLE retention_operations DROP COLUMN target_exchange_id;
ALTER TABLE retention_operations DROP COLUMN target_channel_id;
