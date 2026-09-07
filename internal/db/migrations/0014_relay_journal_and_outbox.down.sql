-- Rolling back drops the whole relay journal and outbox. Nothing outside
-- this migration references any of these four tables — the reverse would be
-- true (exchanges reaches into channels/participants/channel_subjects/
-- channel_participant_bindings, never the other way) — so this is the only
-- migration in scope for the rollback.

DROP TRIGGER IF EXISTS exchange_reads_immutable_delete;
DROP TRIGGER IF EXISTS exchange_reads_immutable_update;
DROP TABLE IF EXISTS exchange_reads;

DROP TRIGGER IF EXISTS exchange_delivery_receipts_immutable_delete;
DROP TRIGGER IF EXISTS exchange_delivery_receipts_immutable_update;
DROP INDEX IF EXISTS idx_exchange_delivery_receipts_exchange;
DROP TABLE IF EXISTS exchange_delivery_receipts;

DROP INDEX IF EXISTS idx_exchange_outbox_claimable;
DROP TABLE IF EXISTS exchange_outbox;

DROP TRIGGER IF EXISTS exchanges_immutable_delete;
DROP TRIGGER IF EXISTS exchanges_immutable_update;
DROP INDEX IF EXISTS idx_exchanges_subject;
DROP INDEX IF EXISTS idx_exchanges_channel;
DROP TABLE IF EXISTS exchanges;
