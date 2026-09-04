CREATE TABLE surfaces (
  id TEXT PRIMARY KEY,
  owner_scope TEXT NOT NULL,
  lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('created', 'active', 'suspended', 'closed', 'expired')),
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  policy TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(policy)),
  next_interaction_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_interaction_sequence > 0),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  closed_at DATETIME,
  close_reason TEXT,
  legacy_room_id TEXT UNIQUE REFERENCES rooms(id) ON DELETE SET NULL
);

CREATE INDEX idx_surfaces_lifecycle ON surfaces(lifecycle_state, updated_at);

CREATE TABLE surface_events (
  event_id TEXT PRIMARY KEY,
  surface_id TEXT NOT NULL REFERENCES surfaces(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  actor_ref TEXT,
  authority TEXT,
  from_revision INTEGER,
  to_revision INTEGER NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  recorded_at DATETIME NOT NULL
);

CREATE INDEX idx_surface_events_surface ON surface_events(surface_id, recorded_at, event_id);

CREATE TABLE interactions (
  id TEXT PRIMARY KEY,
  surface_id TEXT NOT NULL REFERENCES surfaces(id) ON DELETE CASCADE,
  caller_scope TEXT NOT NULL,
  caller_principal_ref TEXT,
  caller_authority TEXT NOT NULL,
  caller_assurance TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  surface_sequence INTEGER NOT NULL CHECK (surface_sequence > 0),
  request_snapshot TEXT NOT NULL CHECK (json_valid(request_snapshot)),
  external_refs TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(external_refs)),
  policy TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(policy)),
  lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('submitted', 'validated', 'staged', 'presented', 'in_progress', 'resolved', 'canceled', 'expired', 'failed', 'superseded')),
  presented_projection_revision INTEGER CHECK (presented_projection_revision IS NULL OR presented_projection_revision > 0),
  participant_ref TEXT,
  connection_id TEXT,
  presented_at DATETIME,
  terminal_cause TEXT,
  terminal_reason TEXT,
  terminal_policy_ref TEXT,
  terminal_error_code TEXT,
  replacement_interaction_id TEXT REFERENCES interactions(id),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  terminal_at DATETIME,
  legacy_room_id TEXT,
  legacy_envelope_id TEXT,
  UNIQUE (caller_scope, idempotency_key),
  UNIQUE (surface_id, surface_sequence),
  UNIQUE (legacy_room_id, legacy_envelope_id),
  CHECK (
    (lifecycle_state IN ('submitted', 'validated', 'staged', 'presented', 'in_progress')
      AND terminal_cause IS NULL AND terminal_reason IS NULL
      AND terminal_policy_ref IS NULL AND terminal_error_code IS NULL
      AND replacement_interaction_id IS NULL AND terminal_at IS NULL)
    OR (lifecycle_state = 'resolved'
      AND terminal_cause IS NULL AND terminal_reason IS NULL
      AND terminal_policy_ref IS NULL AND terminal_error_code IS NULL
      AND replacement_interaction_id IS NULL AND terminal_at IS NOT NULL)
    OR (lifecycle_state = 'canceled'
      AND (
        terminal_cause IN ('caller_withdrawn', 'caller_canceled', 'participant_canceled', 'administrator_canceled', 'surface_policy')
        OR (legacy_envelope_id IS NOT NULL AND terminal_cause = 'legacy_unknown_cancel')
      )
      AND terminal_policy_ref IS NULL AND terminal_error_code IS NULL
      AND replacement_interaction_id IS NULL AND terminal_at IS NOT NULL)
    OR (lifecycle_state = 'expired'
      AND terminal_cause IS NULL AND terminal_policy_ref IS NOT NULL
      AND terminal_error_code IS NULL AND replacement_interaction_id IS NULL
      AND terminal_at IS NOT NULL)
    OR (lifecycle_state = 'failed'
      AND terminal_cause IS NULL AND terminal_policy_ref IS NULL
      AND terminal_error_code IS NOT NULL AND terminal_reason IS NOT NULL
      AND replacement_interaction_id IS NULL AND terminal_at IS NOT NULL)
    OR (lifecycle_state = 'superseded'
      AND terminal_cause IS NULL AND terminal_policy_ref IS NULL
      AND terminal_error_code IS NULL AND replacement_interaction_id IS NOT NULL
      AND terminal_at IS NOT NULL)
  )
);

CREATE INDEX idx_interactions_surface ON interactions(surface_id, created_at, id);
CREATE INDEX idx_interactions_lifecycle ON interactions(lifecycle_state, updated_at);

CREATE TABLE definition_bindings (
  interaction_id TEXT PRIMARY KEY REFERENCES interactions(id) ON DELETE CASCADE,
  publisher TEXT NOT NULL,
  kind TEXT NOT NULL,
  version TEXT NOT NULL,
  revision TEXT NOT NULL,
  digest TEXT,
  source TEXT NOT NULL,
  schema_identity TEXT,
  schema_digest TEXT,
  host_version TEXT,
  assurance TEXT NOT NULL,
  bound_at DATETIME NOT NULL
);

CREATE TABLE interaction_events (
  event_id TEXT PRIMARY KEY,
  surface_id TEXT NOT NULL REFERENCES surfaces(id) ON DELETE CASCADE,
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  actor_ref TEXT,
  authority TEXT,
  from_revision INTEGER,
  to_revision INTEGER NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  recorded_at DATETIME NOT NULL
);

CREATE INDEX idx_interaction_events_interaction ON interaction_events(interaction_id, recorded_at, event_id);

CREATE TABLE draft_revisions (
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK (revision > 0),
  interaction_revision INTEGER NOT NULL CHECK (interaction_revision > 0),
  participant_ref TEXT NOT NULL,
  definition_version TEXT NOT NULL,
  payload TEXT NOT NULL CHECK (json_valid(payload)),
  sensitivity TEXT,
  expires_at DATETIME,
  created_at DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, revision)
);

CREATE TABLE draft_revision_tombstones (
  interaction_id TEXT NOT NULL,
  draft_revision INTEGER NOT NULL,
  reason TEXT NOT NULL,
  actor_ref TEXT,
  recorded_at DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, draft_revision),
  FOREIGN KEY (interaction_id, draft_revision) REFERENCES draft_revisions(interaction_id, revision) ON DELETE CASCADE
);

CREATE TABLE resolutions (
  id TEXT PRIMARY KEY,
  interaction_id TEXT NOT NULL UNIQUE REFERENCES interactions(id) ON DELETE CASCADE,
  expected_interaction_revision INTEGER NOT NULL CHECK (expected_interaction_revision > 0),
  presented_projection_revision INTEGER NOT NULL CHECK (presented_projection_revision > 0),
  participant_ref TEXT NOT NULL,
  participant_authority TEXT NOT NULL,
  participant_assurance TEXT NOT NULL,
  response_kind TEXT NOT NULL,
  response_payload TEXT NOT NULL CHECK (json_valid(response_payload)),
  source_draft_revision INTEGER,
  integrity_digest TEXT NOT NULL,
  submitted_at DATETIME NOT NULL,
  validated_at DATETIME NOT NULL,
  recorded_at DATETIME NOT NULL,
  FOREIGN KEY (interaction_id, source_draft_revision) REFERENCES draft_revisions(interaction_id, revision)
);

CREATE TABLE resolution_deliveries (
  id TEXT PRIMARY KEY,
  resolution_id TEXT NOT NULL REFERENCES resolutions(id) ON DELETE CASCADE,
  destination_binding TEXT NOT NULL CHECK (json_valid(destination_binding)),
  idempotency_key TEXT NOT NULL,
  policy TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(policy)),
  lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('queued', 'delivering', 'delivered', 'acknowledged', 'retryable_failure', 'terminal_failure')),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  lease_owner TEXT,
  lease_expires_at DATETIME,
  receipt TEXT CHECK (receipt IS NULL OR json_valid(receipt)),
  terminal_reason TEXT,
  next_eligible_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  delivered_at DATETIME,
  acknowledged_at DATETIME,
  UNIQUE (resolution_id, idempotency_key)
);

CREATE INDEX idx_resolution_deliveries_state ON resolution_deliveries(lifecycle_state, next_eligible_at, created_at);

CREATE TABLE terminal_notifications (
  id TEXT PRIMARY KEY,
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  terminal_state TEXT NOT NULL CHECK (terminal_state IN ('canceled', 'expired', 'failed', 'superseded')),
  terminal_cause TEXT,
  destination_binding TEXT NOT NULL CHECK (json_valid(destination_binding)),
  idempotency_key TEXT NOT NULL,
  policy TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(policy)),
  lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('queued', 'delivering', 'delivered', 'acknowledged', 'retryable_failure', 'terminal_failure')),
  revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
  lease_owner TEXT,
  lease_expires_at DATETIME,
  receipt TEXT CHECK (receipt IS NULL OR json_valid(receipt)),
  terminal_reason TEXT,
  next_eligible_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  delivered_at DATETIME,
  acknowledged_at DATETIME,
  UNIQUE (interaction_id, idempotency_key),
  CHECK (
    (terminal_state = 'canceled'
      AND terminal_cause IN ('caller_withdrawn', 'caller_canceled', 'participant_canceled', 'administrator_canceled', 'surface_policy'))
    OR (terminal_state IN ('expired', 'failed', 'superseded') AND terminal_cause IS NULL)
  )
);

CREATE INDEX idx_terminal_notifications_state ON terminal_notifications(lifecycle_state, next_eligible_at, created_at);

CREATE TABLE terminal_outcome_retrievals (
  id TEXT PRIMARY KEY,
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  resolution_id TEXT REFERENCES resolutions(id) ON DELETE CASCADE,
  requester_scope TEXT NOT NULL,
  transport_correlation TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(transport_correlation)),
  retrieved_at DATETIME NOT NULL
);

CREATE INDEX idx_terminal_outcome_retrievals_interaction ON terminal_outcome_retrievals(interaction_id, retrieved_at, id);

CREATE TABLE delivery_attempts (
  id TEXT PRIMARY KEY,
  resolution_delivery_id TEXT REFERENCES resolution_deliveries(id) ON DELETE CASCADE,
  terminal_notification_id TEXT REFERENCES terminal_notifications(id) ON DELETE CASCADE,
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
  status TEXT NOT NULL CHECK (status IN ('delivering', 'delivered', 'retryable_failure', 'terminal_failure')),
  receipt TEXT CHECK (receipt IS NULL OR json_valid(receipt)),
  error_code TEXT,
  error_message TEXT,
  started_at DATETIME NOT NULL,
  completed_at DATETIME,
  CHECK ((resolution_delivery_id IS NOT NULL) != (terminal_notification_id IS NOT NULL)),
  UNIQUE (resolution_delivery_id, attempt_number),
  UNIQUE (terminal_notification_id, attempt_number)
);

CREATE TRIGGER surface_events_immutable_update
BEFORE UPDATE ON surface_events
BEGIN
  SELECT RAISE(ABORT, 'surface events are immutable');
END;

CREATE TRIGGER surface_events_immutable_delete
BEFORE DELETE ON surface_events
BEGIN
  SELECT RAISE(ABORT, 'surface events are immutable');
END;

CREATE TRIGGER surfaces_revision_must_advance
BEFORE UPDATE ON surfaces
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'surface revision must advance by one');
END;

CREATE TRIGGER surfaces_terminal_immutable
BEFORE UPDATE ON surfaces
WHEN OLD.lifecycle_state IN ('closed', 'expired')
BEGIN
  SELECT RAISE(ABORT, 'terminal surface is immutable');
END;

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

CREATE TRIGGER interactions_revision_must_advance
BEFORE UPDATE ON interactions
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'interaction revision must advance by one');
END;

CREATE TRIGGER interactions_terminal_immutable
BEFORE UPDATE ON interactions
WHEN OLD.lifecycle_state IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')
BEGIN
  SELECT RAISE(ABORT, 'terminal interaction is immutable');
END;

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

CREATE TRIGGER definition_bindings_immutable_update
BEFORE UPDATE ON definition_bindings
BEGIN
  SELECT RAISE(ABORT, 'definition bindings are immutable');
END;

CREATE TRIGGER definition_bindings_immutable_delete
BEFORE DELETE ON definition_bindings
BEGIN
  SELECT RAISE(ABORT, 'definition bindings are immutable');
END;

CREATE TRIGGER interaction_events_immutable_update
BEFORE UPDATE ON interaction_events
BEGIN
  SELECT RAISE(ABORT, 'interaction events are immutable');
END;

CREATE TRIGGER interaction_events_immutable_delete
BEFORE DELETE ON interaction_events
BEGIN
  SELECT RAISE(ABORT, 'interaction events are immutable');
END;

CREATE TRIGGER draft_revisions_immutable_update
BEFORE UPDATE ON draft_revisions
BEGIN
  SELECT RAISE(ABORT, 'draft revisions are immutable');
END;

CREATE TRIGGER draft_revisions_immutable_delete
BEFORE DELETE ON draft_revisions
BEGIN
  SELECT RAISE(ABORT, 'draft revisions are immutable');
END;

CREATE TRIGGER resolutions_immutable_update
BEFORE UPDATE ON resolutions
BEGIN
  SELECT RAISE(ABORT, 'resolutions are immutable');
END;

CREATE TRIGGER resolutions_immutable_delete
BEFORE DELETE ON resolutions
BEGIN
  SELECT RAISE(ABORT, 'resolutions are immutable');
END;

CREATE TRIGGER resolution_deliveries_revision_must_advance
BEFORE UPDATE ON resolution_deliveries
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'resolution delivery revision must advance by one');
END;

CREATE TRIGGER resolution_deliveries_identity_immutable
BEFORE UPDATE ON resolution_deliveries
WHEN NEW.resolution_id IS NOT OLD.resolution_id
  OR NEW.destination_binding IS NOT OLD.destination_binding
  OR NEW.idempotency_key IS NOT OLD.idempotency_key
  OR NEW.policy IS NOT OLD.policy
BEGIN
  SELECT RAISE(ABORT, 'resolution delivery identity is immutable');
END;

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

CREATE TRIGGER resolution_deliveries_terminal_immutable
BEFORE UPDATE ON resolution_deliveries
WHEN OLD.lifecycle_state IN ('acknowledged', 'terminal_failure')
BEGIN
  SELECT RAISE(ABORT, 'terminal resolution delivery is immutable');
END;

CREATE TRIGGER terminal_notifications_revision_must_advance
BEFORE UPDATE ON terminal_notifications
WHEN NEW.revision != OLD.revision + 1
BEGIN
  SELECT RAISE(ABORT, 'terminal notification revision must advance by one');
END;

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

CREATE TRIGGER terminal_notifications_terminal_immutable
BEFORE UPDATE ON terminal_notifications
WHEN OLD.lifecycle_state IN ('acknowledged', 'terminal_failure')
BEGIN
  SELECT RAISE(ABORT, 'terminal notification is immutable');
END;

CREATE TRIGGER terminal_outcome_retrievals_immutable_update
BEFORE UPDATE ON terminal_outcome_retrievals
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome retrievals are immutable');
END;

CREATE TRIGGER terminal_outcome_retrievals_immutable_delete
BEFORE DELETE ON terminal_outcome_retrievals
BEGIN
  SELECT RAISE(ABORT, 'terminal outcome retrievals are immutable');
END;

CREATE TRIGGER delivery_attempts_immutable_update
BEFORE UPDATE ON delivery_attempts
BEGIN
  SELECT RAISE(ABORT, 'delivery attempts are immutable');
END;

CREATE TRIGGER delivery_attempts_immutable_delete
BEFORE DELETE ON delivery_attempts
BEGIN
  SELECT RAISE(ABORT, 'delivery attempts are immutable');
END;

INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision,
  created_at, updated_at, closed_at, close_reason, legacy_room_id
)
SELECT
  id,
  'standalone-local',
  CASE WHEN closed_at IS NULL THEN 'active' ELSE 'closed' END,
  meta,
  '{}',
  (SELECT COUNT(*) + 1 FROM envelopes e WHERE e.room_id = rooms.id),
  1,
  created_at,
  updated_at,
  closed_at,
  closed_reason,
  id
FROM rooms;

INSERT INTO surface_events (
  event_id, surface_id, event_type, authority, to_revision, metadata, recorded_at
)
SELECT
  'legacy:surface:' || id,
  id,
  'surface.legacy_imported',
  'legacy-unknown',
  1,
  json_object('legacy_room_id', id, 'legacy_state', CASE WHEN closed_at IS NULL THEN 'active' ELSE 'closed' END),
  created_at
FROM rooms;

INSERT INTO interactions (
  id, surface_id, caller_scope, caller_principal_ref, caller_authority,
  caller_assurance, idempotency_key, surface_sequence, request_snapshot,
  external_refs, policy, lifecycle_state, terminal_cause, terminal_reason,
  terminal_policy_ref, terminal_error_code,
  revision, created_at, updated_at, terminal_at, legacy_room_id, legacy_envelope_id
)
SELECT
  'legacy:interaction:' || room_id || ':' || envelope_id,
  room_id,
  'standalone-local',
  NULL,
  'legacy-unknown',
  'legacy-uncertain',
  'legacy:tangent.session_advance:' || room_id || ':' || envelope_id,
  ROW_NUMBER() OVER (PARTITION BY room_id ORDER BY created_at, envelope_id),
  request_payload,
  json_object('legacy_room_id', room_id, 'legacy_envelope_id', envelope_id),
  json_object(
    'legacy_status', status,
    'legacy_error_code', error_code,
    'legacy_recovery', CASE WHEN status = 'pending' THEN 'unavailable' ELSE NULL END
  ),
  CASE
    -- v0.12 did not persist an exact definition revision, so a pending row is
    -- not provably safe to re-present during a schema-only migration.
    WHEN status = 'pending' THEN 'failed'
    WHEN status IN ('submitted', 'partial') THEN 'resolved'
    WHEN status = 'cancelled' THEN 'canceled'
    ELSE 'failed'
  END,
  CASE
    WHEN status = 'cancelled' THEN 'legacy_unknown_cancel'
    ELSE NULL
  END,
  CASE
    WHEN status = 'pending' THEN 'Legacy pending request lacks an exact definition revision and cannot be safely re-presented'
    WHEN status = 'cancelled' THEN error_message
    WHEN status NOT IN ('submitted', 'partial') THEN COALESCE(error_message, 'Legacy interaction failed without a recorded message')
    ELSE NULL
  END,
  NULL,
  CASE
    WHEN status = 'pending' THEN 'legacy_recovery_unavailable'
    WHEN status NOT IN ('submitted', 'partial', 'cancelled') THEN COALESCE(error_code, 'legacy_unknown_failure')
    ELSE NULL
  END,
  1,
  created_at,
  COALESCE(resolved_at, created_at),
  COALESCE(resolved_at, created_at),
  room_id,
  envelope_id
FROM envelopes;

INSERT INTO definition_bindings (
  interaction_id, publisher, kind, version, revision, source,
  assurance, bound_at
)
SELECT
  'legacy:interaction:' || room_id || ':' || envelope_id,
  'legacy-unknown',
  type,
  '',
  'unknown',
  'v0.12-envelope',
  'legacy-uncertain',
  created_at
FROM envelopes;

INSERT INTO interaction_events (
  event_id, surface_id, interaction_id, event_type, authority,
  to_revision, metadata, recorded_at
)
SELECT
  'legacy:interaction-event:' || room_id || ':' || envelope_id,
  room_id,
  'legacy:interaction:' || room_id || ':' || envelope_id,
  CASE WHEN status = 'pending' THEN 'interaction.legacy_recovery_failed' ELSE 'interaction.legacy_imported' END,
  'legacy-unknown',
  1,
  json_object(
    'legacy_status', status,
    'legacy_error_code', error_code,
    'recovery_error_code', CASE WHEN status = 'pending' THEN 'legacy_recovery_unavailable' ELSE NULL END
  ),
  created_at
FROM envelopes;

INSERT INTO resolutions (
  id, interaction_id, expected_interaction_revision,
  presented_projection_revision, participant_ref, participant_authority,
  participant_assurance, response_kind, response_payload,
  integrity_digest, submitted_at, validated_at, recorded_at
)
SELECT
  'legacy:resolution:' || room_id || ':' || envelope_id,
  'legacy:interaction:' || room_id || ':' || envelope_id,
  1,
  1,
  'legacy-unknown',
  'legacy-unknown',
  'legacy-uncertain',
  COALESCE(response_kind, 'legacy-unknown'),
  COALESCE(response_payload, '{}'),
  'legacy-uncertain',
  COALESCE(resolved_at, created_at),
  COALESCE(resolved_at, created_at),
  COALESCE(resolved_at, created_at)
FROM envelopes
WHERE status IN ('submitted', 'partial');

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
  i.id AS interaction_id
FROM envelopes e
JOIN rooms r ON r.id = e.room_id
LEFT JOIN surfaces s ON s.legacy_room_id = r.id
LEFT JOIN interactions i
  ON i.legacy_room_id = e.room_id
  AND i.legacy_envelope_id = e.envelope_id;
