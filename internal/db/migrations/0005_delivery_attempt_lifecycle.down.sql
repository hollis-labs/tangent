DROP TRIGGER IF EXISTS delivery_events_immutable_delete;
DROP TRIGGER IF EXISTS delivery_events_immutable_update;
DROP INDEX IF EXISTS idx_delivery_events_notification;
DROP INDEX IF EXISTS idx_delivery_events_resolution;
DROP TABLE IF EXISTS delivery_events;

DROP INDEX IF EXISTS idx_delivery_attempts_open_notification;
DROP INDEX IF EXISTS idx_delivery_attempts_open_resolution;
DROP TRIGGER IF EXISTS delivery_attempts_valid_seal_update;
CREATE TRIGGER delivery_attempts_immutable_update
BEFORE UPDATE ON delivery_attempts
BEGIN
  SELECT RAISE(ABORT, 'delivery attempts are immutable');
END;
