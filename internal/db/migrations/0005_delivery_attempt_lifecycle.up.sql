DROP TRIGGER IF EXISTS delivery_attempts_immutable_update;

-- A delivery attempt is inserted before destination I/O starts. The only
-- permitted update seals that open fact exactly once with the strongest
-- observed outcome; sealed attempts remain immutable.
CREATE TRIGGER delivery_attempts_valid_seal_update
BEFORE UPDATE ON delivery_attempts
WHEN NOT (
  OLD.status = 'delivering'
  AND OLD.completed_at IS NULL
  AND NEW.id IS OLD.id
  AND NEW.resolution_delivery_id IS OLD.resolution_delivery_id
  AND NEW.terminal_notification_id IS OLD.terminal_notification_id
  AND NEW.attempt_number IS OLD.attempt_number
  AND NEW.started_at IS OLD.started_at
  AND NEW.status IN ('delivered', 'retryable_failure', 'terminal_failure')
  AND NEW.completed_at IS NOT NULL
  AND (
    (NEW.status = 'delivered'
      AND NEW.receipt IS NOT NULL
      AND NEW.error_code IS NULL
      AND NEW.error_message IS NULL)
    OR (NEW.status IN ('retryable_failure', 'terminal_failure')
      AND NEW.receipt IS NULL
      AND NEW.error_code IS NOT NULL
      AND NEW.error_message IS NOT NULL)
  )
)
BEGIN
  SELECT RAISE(ABORT, 'delivery attempt may only be sealed once');
END;

-- Older code could leave a delivery lease without its corresponding open
-- attempt. Backfill the durable pre-effect fact before startup recovery runs.
INSERT INTO delivery_attempts (
  id, resolution_delivery_id, terminal_notification_id,
  attempt_number, status, started_at
)
SELECT
  'migration:0005:resolution:' || d.id,
  d.id,
  NULL,
  (SELECT COALESCE(MAX(a.attempt_number), 0) + 1
     FROM delivery_attempts a
     WHERE a.resolution_delivery_id = d.id),
  'delivering',
  d.updated_at
FROM resolution_deliveries d
WHERE d.lifecycle_state = 'delivering'
  AND NOT EXISTS (
    SELECT 1 FROM delivery_attempts a
    WHERE a.resolution_delivery_id = d.id
      AND a.status = 'delivering'
      AND a.completed_at IS NULL
  );

INSERT INTO delivery_attempts (
  id, resolution_delivery_id, terminal_notification_id,
  attempt_number, status, started_at
)
SELECT
  'migration:0005:notification:' || n.id,
  NULL,
  n.id,
  (SELECT COALESCE(MAX(a.attempt_number), 0) + 1
     FROM delivery_attempts a
     WHERE a.terminal_notification_id = n.id),
  'delivering',
  n.updated_at
FROM terminal_notifications n
WHERE n.lifecycle_state = 'delivering'
  AND NOT EXISTS (
    SELECT 1 FROM delivery_attempts a
    WHERE a.terminal_notification_id = n.id
      AND a.status = 'delivering'
      AND a.completed_at IS NULL
  );

CREATE UNIQUE INDEX idx_delivery_attempts_open_resolution
  ON delivery_attempts(resolution_delivery_id)
  WHERE resolution_delivery_id IS NOT NULL
    AND status = 'delivering'
    AND completed_at IS NULL;
CREATE UNIQUE INDEX idx_delivery_attempts_open_notification
  ON delivery_attempts(terminal_notification_id)
  WHERE terminal_notification_id IS NOT NULL
    AND status = 'delivering'
    AND completed_at IS NULL;

CREATE TABLE delivery_events (
  event_id TEXT PRIMARY KEY,
  resolution_delivery_id TEXT REFERENCES resolution_deliveries(id) ON DELETE CASCADE,
  terminal_notification_id TEXT REFERENCES terminal_notifications(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL CHECK (event_type IN (
    'delivery.queued',
    'delivery.started',
    'delivery.delivered',
    'delivery.retryable_failed',
    'delivery.terminal_failed',
    'delivery.acknowledged'
  )),
  from_revision INTEGER NOT NULL CHECK (from_revision >= 0),
  to_revision INTEGER NOT NULL CHECK (to_revision > from_revision),
  attempt_number INTEGER,
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  recorded_at DATETIME NOT NULL,
  CHECK ((resolution_delivery_id IS NOT NULL) != (terminal_notification_id IS NOT NULL)),
  UNIQUE (resolution_delivery_id, to_revision),
  UNIQUE (terminal_notification_id, to_revision)
);

CREATE INDEX idx_delivery_events_resolution
  ON delivery_events(resolution_delivery_id, to_revision);
CREATE INDEX idx_delivery_events_notification
  ON delivery_events(terminal_notification_id, to_revision);

CREATE TRIGGER delivery_events_immutable_update
BEFORE UPDATE ON delivery_events
BEGIN
  SELECT RAISE(ABORT, 'delivery events are immutable');
END;

CREATE TRIGGER delivery_events_immutable_delete
BEFORE DELETE ON delivery_events
BEGIN
  SELECT RAISE(ABORT, 'delivery events are immutable');
END;

-- Existing obligations receive their provable initial event. For an active
-- legacy lease, also record the claim represented by the backfilled attempt.
INSERT INTO delivery_events (
  event_id, resolution_delivery_id, terminal_notification_id,
  event_type, from_revision, to_revision, metadata, recorded_at
)
SELECT
  'migration:0005:queued:resolution:' || id,
  id,
  NULL,
  'delivery.queued',
  0,
  1,
  '{"source":"migration-0005"}',
  created_at
FROM resolution_deliveries;

INSERT INTO delivery_events (
  event_id, resolution_delivery_id, terminal_notification_id,
  event_type, from_revision, to_revision, metadata, recorded_at
)
SELECT
  'migration:0005:queued:notification:' || id,
  NULL,
  id,
  'delivery.queued',
  0,
  1,
  '{"source":"migration-0005"}',
  created_at
FROM terminal_notifications;

INSERT INTO delivery_events (
  event_id, resolution_delivery_id, terminal_notification_id,
  event_type, from_revision, to_revision, attempt_number, metadata, recorded_at
)
SELECT
  'migration:0005:started:resolution:' || d.id,
  d.id,
  NULL,
  'delivery.started',
  d.revision - 1,
  d.revision,
  a.attempt_number,
  '{"source":"migration-0005"}',
  a.started_at
FROM resolution_deliveries d
JOIN delivery_attempts a ON a.resolution_delivery_id = d.id
WHERE d.lifecycle_state = 'delivering'
  AND a.status = 'delivering'
  AND a.completed_at IS NULL;

INSERT INTO delivery_events (
  event_id, resolution_delivery_id, terminal_notification_id,
  event_type, from_revision, to_revision, attempt_number, metadata, recorded_at
)
SELECT
  'migration:0005:started:notification:' || n.id,
  NULL,
  n.id,
  'delivery.started',
  n.revision - 1,
  n.revision,
  a.attempt_number,
  '{"source":"migration-0005"}',
  a.started_at
FROM terminal_notifications n
JOIN delivery_attempts a ON a.terminal_notification_id = n.id
WHERE n.lifecycle_state = 'delivering'
  AND a.status = 'delivering'
  AND a.completed_at IS NULL;
