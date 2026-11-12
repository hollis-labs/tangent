-- Tangent schema: the view and the immutability and ordering triggers (created after the tables so a one-way data copy runs without them).

-- +goose Up

-- +goose StatementBegin
CREATE TRIGGER surface_events_immutable_update
BEFORE UPDATE ON surface_events
BEGIN
  SELECT RAISE(ABORT, 'surface events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surface_events_immutable_delete
BEFORE DELETE ON surface_events
BEGIN
  SELECT RAISE(ABORT, 'surface events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surfaces_revision_must_advance
BEFORE UPDATE ON surfaces
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'surface revision must advance by one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surfaces_terminal_immutable
BEFORE UPDATE ON surfaces
WHEN OLD.lifecycle_state IN ('closed', 'expired')
BEGIN
  SELECT RAISE(ABORT, 'terminal surface is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surfaces_valid_transition
BEFORE UPDATE OF lifecycle_state ON surfaces
WHEN NEW.lifecycle_state != OLD.lifecycle_state
  AND NOT (
    (OLD.lifecycle_state = 'created' AND NEW.lifecycle_state IN ('active', 'closed', 'expired'))
    OR (OLD.lifecycle_state = 'active' AND NEW.lifecycle_state IN ('suspended', 'closed', 'expired'))
    OR (OLD.lifecycle_state = 'suspended' AND NEW.lifecycle_state IN ('active', 'closed', 'expired'))
  )
BEGIN
  SELECT RAISE(ABORT, 'invalid surface lifecycle transition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interactions_identity_immutable
BEFORE UPDATE ON interactions
WHEN NEW.surface_id IS NOT OLD.surface_id
  OR NEW.caller_scope IS NOT OLD.caller_scope
  OR NEW.caller_principal_ref IS NOT OLD.caller_principal_ref
  OR NEW.caller_authority IS NOT OLD.caller_authority
  OR NEW.caller_assurance IS NOT OLD.caller_assurance
  OR NEW.idempotency_key IS NOT OLD.idempotency_key
  OR NEW.surface_sequence IS NOT OLD.surface_sequence
  OR NEW.request_snapshot IS NOT OLD.request_snapshot
  OR NEW.external_refs IS NOT OLD.external_refs
  OR NEW.policy IS NOT OLD.policy
  OR NEW.legacy_room_id IS NOT OLD.legacy_room_id
  OR NEW.legacy_envelope_id IS NOT OLD.legacy_envelope_id
BEGIN
  SELECT RAISE(ABORT, 'interaction identity and request are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interactions_revision_must_advance
BEFORE UPDATE ON interactions
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'interaction revision must advance by one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interactions_terminal_immutable
BEFORE UPDATE ON interactions
WHEN OLD.lifecycle_state IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')
BEGIN
  SELECT RAISE(ABORT, 'terminal interaction is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interactions_valid_transition
BEFORE UPDATE OF lifecycle_state ON interactions
WHEN NEW.lifecycle_state != OLD.lifecycle_state
  AND NOT (
    (OLD.lifecycle_state = 'submitted' AND NEW.lifecycle_state IN ('validated', 'canceled', 'expired', 'failed'))
    OR (OLD.lifecycle_state = 'validated' AND NEW.lifecycle_state IN ('staged', 'canceled', 'expired', 'failed'))
    OR (OLD.lifecycle_state = 'staged' AND NEW.lifecycle_state IN ('presented', 'canceled', 'expired', 'failed', 'superseded'))
    OR (OLD.lifecycle_state = 'presented' AND NEW.lifecycle_state IN ('in_progress', 'resolved', 'canceled', 'expired', 'superseded'))
    OR (OLD.lifecycle_state = 'in_progress' AND NEW.lifecycle_state IN ('resolved', 'canceled', 'expired', 'superseded'))
  )
BEGIN
  SELECT RAISE(ABORT, 'invalid interaction lifecycle transition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_bindings_immutable_delete
BEFORE DELETE ON definition_bindings
BEGIN
  SELECT RAISE(ABORT, 'definition bindings are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interaction_events_immutable_update
BEFORE UPDATE ON interaction_events
BEGIN
  SELECT RAISE(ABORT, 'interaction events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interaction_events_immutable_delete
BEFORE DELETE ON interaction_events
BEGIN
  SELECT RAISE(ABORT, 'interaction events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER draft_revisions_immutable_update
BEFORE UPDATE ON draft_revisions
BEGIN
  SELECT RAISE(ABORT, 'draft revisions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER draft_revisions_immutable_delete
BEFORE DELETE ON draft_revisions
BEGIN
  SELECT RAISE(ABORT, 'draft revisions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolutions_immutable_update
BEFORE UPDATE ON resolutions
BEGIN
  SELECT RAISE(ABORT, 'resolutions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolutions_immutable_delete
BEFORE DELETE ON resolutions
BEGIN
  SELECT RAISE(ABORT, 'resolutions are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolution_deliveries_revision_must_advance
BEFORE UPDATE ON resolution_deliveries
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'resolution delivery revision must advance by one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolution_deliveries_identity_immutable
BEFORE UPDATE ON resolution_deliveries
WHEN NEW.resolution_id IS NOT OLD.resolution_id
  OR NEW.destination_binding IS NOT OLD.destination_binding
  OR NEW.idempotency_key IS NOT OLD.idempotency_key
  OR NEW.policy IS NOT OLD.policy
BEGIN
  SELECT RAISE(ABORT, 'resolution delivery identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolution_deliveries_valid_transition
BEFORE UPDATE OF lifecycle_state ON resolution_deliveries
WHEN NEW.lifecycle_state != OLD.lifecycle_state
  AND NOT (
    (OLD.lifecycle_state = 'queued' AND NEW.lifecycle_state IN ('delivering', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'delivering' AND NEW.lifecycle_state IN ('delivered', 'retryable_failure', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'retryable_failure' AND NEW.lifecycle_state IN ('delivering', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'delivered' AND NEW.lifecycle_state = 'acknowledged')
  )
BEGIN
  SELECT RAISE(ABORT, 'invalid resolution delivery lifecycle transition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER resolution_deliveries_terminal_immutable
BEFORE UPDATE ON resolution_deliveries
WHEN OLD.lifecycle_state IN ('acknowledged', 'terminal_failure')
BEGIN
  SELECT RAISE(ABORT, 'terminal resolution delivery is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_notifications_revision_must_advance
BEFORE UPDATE ON terminal_notifications
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'terminal notification revision must advance by one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_notifications_identity_immutable
BEFORE UPDATE ON terminal_notifications
WHEN NEW.interaction_id IS NOT OLD.interaction_id
  OR NEW.terminal_state IS NOT OLD.terminal_state
  OR NEW.terminal_cause IS NOT OLD.terminal_cause
  OR NEW.destination_binding IS NOT OLD.destination_binding
  OR NEW.idempotency_key IS NOT OLD.idempotency_key
  OR NEW.policy IS NOT OLD.policy
BEGIN
  SELECT RAISE(ABORT, 'terminal notification identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_notifications_valid_transition
BEFORE UPDATE OF lifecycle_state ON terminal_notifications
WHEN NEW.lifecycle_state != OLD.lifecycle_state
  AND NOT (
    (OLD.lifecycle_state = 'queued' AND NEW.lifecycle_state IN ('delivering', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'delivering' AND NEW.lifecycle_state IN ('delivered', 'retryable_failure', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'retryable_failure' AND NEW.lifecycle_state IN ('delivering', 'terminal_failure'))
    OR (OLD.lifecycle_state = 'delivered' AND NEW.lifecycle_state = 'acknowledged')
  )
BEGIN
  SELECT RAISE(ABORT, 'invalid terminal notification lifecycle transition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_notifications_terminal_immutable
BEFORE UPDATE ON terminal_notifications
WHEN OLD.lifecycle_state IN ('acknowledged', 'terminal_failure')
BEGIN
  SELECT RAISE(ABORT, 'terminal notification is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_outcome_retrievals_immutable_update
BEFORE UPDATE ON terminal_outcome_retrievals
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome retrievals are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_outcome_retrievals_immutable_delete
BEFORE DELETE ON terminal_outcome_retrievals
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome retrievals are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER delivery_attempts_immutable_delete
BEFORE DELETE ON delivery_attempts
BEGIN
  SELECT RAISE(ABORT, 'delivery attempts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER delivery_events_immutable_update
BEFORE UPDATE ON delivery_events
BEGIN
  SELECT RAISE(ABORT, 'delivery events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER delivery_events_immutable_delete
BEFORE DELETE ON delivery_events
BEGIN
  SELECT RAISE(ABORT, 'delivery events are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_outcome_acknowledgements_immutable_update
BEFORE UPDATE ON terminal_outcome_acknowledgements
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome acknowledgements are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER terminal_outcome_acknowledgements_immutable_delete
BEFORE DELETE ON terminal_outcome_acknowledgements
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome acknowledgements are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIEW legacy_room_history_v12 AS
SELECT
  e.room_id,
  e.envelope_id,
  e.type,
  e.request_payload,
  e.response_kind,
  e.response_payload,
  e.status,
  e.error_code,
  e.error_message,
  e.created_at,
  e.resolved_at,
  s.id AS surface_id,
  i.id AS interaction_id,
  i.lifecycle_state AS interaction_state,
  a.acknowledged_at AS caller_acknowledged_at
FROM envelopes e
JOIN rooms r ON r.id = e.room_id
LEFT JOIN surfaces s ON s.legacy_room_id = r.id
LEFT JOIN interactions i
  ON i.legacy_room_id = e.room_id
  AND i.legacy_envelope_id = e.envelope_id
LEFT JOIN terminal_outcome_acknowledgements a
  ON a.interaction_id = i.id;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_manifests_immutable_update
BEFORE UPDATE ON definition_manifests
BEGIN
  SELECT RAISE(ABORT, 'retained definition manifests are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_manifests_immutable_delete
BEFORE DELETE ON definition_manifests
BEGIN
  SELECT RAISE(ABORT, 'retained definition manifests are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_bindings_immutable_update
BEFORE UPDATE ON definition_bindings
BEGIN
  SELECT RAISE(ABORT, 'definition bindings are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER effect_receipts_immutable_update
BEFORE UPDATE ON effect_receipts
BEGIN
  SELECT RAISE(ABORT, 'effect_receipts rows are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER telemetry_events_append_only
BEFORE UPDATE ON telemetry_events
BEGIN
  SELECT RAISE(ABORT, 'telemetry events are append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER retention_operations_immutable_update
BEFORE UPDATE ON retention_operations
BEGIN
  SELECT RAISE(ABORT, 'retention operations are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER retention_operations_immutable_delete
BEFORE DELETE ON retention_operations
BEGIN
  SELECT RAISE(ABORT, 'retention operations are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surface_open_requests_immutable_update
BEFORE UPDATE ON surface_open_requests
BEGIN
  SELECT RAISE(ABORT, 'surface open requests are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER surface_open_requests_immutable_delete
BEFORE DELETE ON surface_open_requests
BEGIN
  SELECT RAISE(ABORT, 'surface open requests are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchanges_immutable_update
BEFORE UPDATE ON exchanges
BEGIN
  SELECT RAISE(ABORT, 'exchanges are immutable once accepted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchanges_immutable_delete
BEFORE DELETE ON exchanges
BEGIN
  SELECT RAISE(ABORT, 'exchanges are immutable once accepted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchange_delivery_receipts_immutable_update
BEFORE UPDATE ON exchange_delivery_receipts
BEGIN
  SELECT RAISE(ABORT, 'exchange delivery receipts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchange_delivery_receipts_immutable_delete
BEFORE DELETE ON exchange_delivery_receipts
BEGIN
  SELECT RAISE(ABORT, 'exchange delivery receipts are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchange_reads_immutable_update
BEFORE UPDATE ON exchange_reads
BEGIN
  SELECT RAISE(ABORT, 'exchange reads are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER exchange_reads_immutable_delete
BEFORE DELETE ON exchange_reads
BEGIN
  SELECT RAISE(ABORT, 'exchange reads are immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interaction_inbox_arrival AFTER INSERT ON interactions
BEGIN
    INSERT INTO inbox_order (interaction_id) VALUES (NEW.id);
END;
-- +goose StatementEnd

-- The one row the event outbox counts on. A copied database already has its own (the copy keeps
-- the generation and the last sequence); a new one gets a random generation, as the original did.
-- +goose StatementBegin
INSERT OR IGNORE INTO event_outbox_state (id, generation) VALUES (1, (random() & 9007199254740991) + 1);
-- +goose StatementEnd
