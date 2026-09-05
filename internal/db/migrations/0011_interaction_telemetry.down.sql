-- Rolling back drops the correlation audit trail.
--
-- Nothing else depends on it: no foreign key points here, no lifecycle
-- decision reads it, and a build without the table degrades to metrics that
-- live only in process memory. That is a real loss of visibility and not a
-- loss of correctness, which is why this rollback is a plain DROP rather than
-- the archival dance the record tables would need.

DROP TRIGGER IF EXISTS telemetry_events_append_only;
DROP INDEX IF EXISTS idx_telemetry_events_occurred;
DROP INDEX IF EXISTS idx_telemetry_events_name;
DROP INDEX IF EXISTS idx_telemetry_events_interaction;
DROP INDEX IF EXISTS idx_telemetry_events_trace;
DROP TABLE IF EXISTS telemetry_events;
