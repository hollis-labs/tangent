-- Tangent schema: tables and indexes. Equal to the schema the pre-Substrate binary ended at (its migrations 0001 to 0024).

-- +goose Up

-- +goose StatementBegin
CREATE TABLE rooms (
  id TEXT PRIMARY KEY,
  title TEXT,
  meta TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  closed_at DATETIME,
  closed_reason TEXT
, current_phase TEXT NOT NULL DEFAULT '', phases_visited TEXT NOT NULL DEFAULT '[]', phase_outputs TEXT NOT NULL DEFAULT '{}');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_rooms_active ON rooms(closed_at) WHERE closed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE envelopes (
  room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  envelope_id TEXT NOT NULL,
  type TEXT NOT NULL,
  request_payload TEXT NOT NULL,
  response_kind TEXT,
  response_payload TEXT,
  status TEXT NOT NULL,
  error_code TEXT,
  error_message TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at DATETIME,
  PRIMARY KEY (room_id, envelope_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_envelopes_room ON envelopes(room_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_surfaces_lifecycle ON surfaces(lifecycle_state, updated_at);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_surface_events_surface ON surface_events(surface_id, recorded_at, event_id);
-- +goose StatementEnd

-- +goose StatementBegin
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
  legacy_envelope_id TEXT, participant_scope TEXT, participant_authority TEXT, participant_assurance TEXT,
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_interactions_surface ON interactions(surface_id, created_at, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_interactions_lifecycle ON interactions(lifecycle_state, updated_at);
-- +goose StatementEnd

-- +goose StatementBegin
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
, manifest_digest TEXT, contract_digest TEXT, response_schema_digest TEXT, package_id TEXT, package_version TEXT, ownership_class TEXT, compatibility_class TEXT, renderer_id TEXT, renderer_class TEXT, renderer_trust_class TEXT, required_capabilities TEXT, granted_capabilities TEXT, materialization_state TEXT);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_interaction_events_interaction ON interaction_events(interaction_id, recorded_at, event_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE draft_revisions (
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK (revision > 0),
  interaction_revision INTEGER NOT NULL CHECK (interaction_revision > 0),
  participant_ref TEXT NOT NULL,
  definition_version TEXT NOT NULL,
  payload TEXT NOT NULL CHECK (json_valid(payload)),
  sensitivity TEXT,
  expires_at DATETIME,
  created_at DATETIME NOT NULL, participant_scope TEXT, participant_authority TEXT, participant_assurance TEXT,
  PRIMARY KEY (interaction_id, revision)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE draft_revision_tombstones (
  interaction_id TEXT NOT NULL,
  draft_revision INTEGER NOT NULL,
  reason TEXT NOT NULL,
  actor_ref TEXT,
  recorded_at DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, draft_revision),
  FOREIGN KEY (interaction_id, draft_revision) REFERENCES draft_revisions(interaction_id, revision) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
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
  recorded_at DATETIME NOT NULL, participant_scope TEXT,
  FOREIGN KEY (interaction_id, source_draft_revision) REFERENCES draft_revisions(interaction_id, revision)
);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_resolution_deliveries_state ON resolution_deliveries(lifecycle_state, next_eligible_at, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_terminal_notifications_state ON terminal_notifications(lifecycle_state, next_eligible_at, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE terminal_outcome_retrievals (
  id TEXT PRIMARY KEY,
  interaction_id TEXT NOT NULL REFERENCES interactions(id) ON DELETE CASCADE,
  resolution_id TEXT REFERENCES resolutions(id) ON DELETE CASCADE,
  requester_scope TEXT NOT NULL,
  transport_correlation TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(transport_correlation)),
  retrieved_at DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_terminal_outcome_retrievals_interaction ON terminal_outcome_retrievals(interaction_id, retrieved_at, id);
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_delivery_attempts_open_resolution
  ON delivery_attempts(resolution_delivery_id)
  WHERE resolution_delivery_id IS NOT NULL
    AND status = 'delivering'
    AND completed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_delivery_attempts_open_notification
  ON delivery_attempts(terminal_notification_id)
  WHERE terminal_notification_id IS NOT NULL
    AND status = 'delivering'
    AND completed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_delivery_events_resolution
  ON delivery_events(resolution_delivery_id, to_revision);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_delivery_events_notification
  ON delivery_events(terminal_notification_id, to_revision);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE terminal_outcome_acknowledgements (
  interaction_id TEXT PRIMARY KEY REFERENCES interactions(id) ON DELETE CASCADE,
  acknowledgement_id TEXT NOT NULL UNIQUE,
  resolution_id TEXT REFERENCES resolutions(id) ON DELETE CASCADE,
  requester_scope TEXT NOT NULL,
  transport_correlation TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(transport_correlation)),
  acknowledged_at DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_interactions_legacy_room
  ON interactions(legacy_room_id, lifecycle_state)
  WHERE legacy_room_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE definition_manifests (
  -- The composite digest computed by the interaction catalog. Content
  -- addressed: identical material always produces this same key, so
  -- retention is idempotent and a duplicate pin is a no-op.
  binding_digest TEXT PRIMARY KEY,

  publisher TEXT NOT NULL,
  kind TEXT NOT NULL,
  version TEXT NOT NULL,
  -- Monotonic non-semantic revision within a version (ADR 0003 §2.1). Stored
  -- as an integer here; definition_bindings.revision keeps its TEXT column so
  -- existing rows are not retyped.
  revision INTEGER NOT NULL CHECK (revision >= 1),

  manifest_digest TEXT NOT NULL,
  contract_digest TEXT NOT NULL,

  -- The exact authored bytes. These are what ValidateInteractionRequest and
  -- ValidateInteractionResponse compile against for a pinned interaction —
  -- never the live compiled schema, which is the behaviour
  -- TestEnvelopeDefinitionCatalogValidatesAgainstPinnedContent asserts.
  manifest_source BLOB NOT NULL,
  request_schema BLOB,
  response_schema BLOB,
  error_schema BLOB,

  response_kind TEXT NOT NULL,
  -- 'present' or 'absent' (ADR 0003 §8 C4). 'absent' preserves the
  -- response-kind-only check for kinds whose response schema is backfilled
  -- later, rather than silently rejecting responses production accepts.
  compatibility_response_schema TEXT NOT NULL
    CHECK (compatibility_response_schema IN ('present', 'absent')),

  schema_identity TEXT,
  request_schema_digest TEXT,
  response_schema_digest TEXT,

  package_id TEXT NOT NULL,
  package_version TEXT NOT NULL,
  ownership_class TEXT NOT NULL,
  compatibility_class TEXT NOT NULL,

  renderer_id TEXT NOT NULL DEFAULT '',
  renderer_class TEXT NOT NULL DEFAULT '',
  renderer_trust_class TEXT NOT NULL DEFAULT '',

  -- Host-mediated *effect* capabilities (ADR 0003 §2.5). Distinct from the
  -- object-access capabilities in ADR 0004 §2; the two never substitute for
  -- one another and must not be merged into one column.
  required_capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(required_capabilities)),
  granted_capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(granted_capabilities)),

  -- Tangent's derivation, never the manifest's request. 'unverified' is a
  -- reserved value with no v0.x producer (ADR 0003 §2.7) and is deliberately
  -- absent from this constraint, so a row carrying it cannot be written.
  trust_assurance TEXT NOT NULL
    CHECK (trust_assurance IN ('in-tree-build', 'content-addressed-registry', 'signed-package')),
  trust_source_locator TEXT NOT NULL DEFAULT '',

  host_version TEXT,
  -- Bumping envelopeDefinitionValidatorRevision must make old records fail
  -- closed rather than be reinterpreted by new validation code, so the value
  -- in force at pin time is retained beside the material.
  validator_revision TEXT NOT NULL,
  materialization_state TEXT NOT NULL,
  materialized_at DATETIME NOT NULL,

  UNIQUE (kind, version, revision, manifest_digest)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_definition_manifests_kind ON definition_manifests(kind, version, revision);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE participant_sessions (
  id TEXT PRIMARY KEY,
  cookie_sha256 TEXT NOT NULL UNIQUE,
  participant_scope TEXT NOT NULL,
  participant_ref TEXT NOT NULL,
  participant_authority TEXT NOT NULL,
  participant_assurance TEXT NOT NULL,
  capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities)),
  created_at DATETIME NOT NULL,
  last_seen_at DATETIME NOT NULL,
  expires_at DATETIME NOT NULL,
  revoked_at DATETIME,
  revoked_reason TEXT,
  rotated_to TEXT REFERENCES participant_sessions(id) ON DELETE SET NULL
, elevated_until DATETIME);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_participant_sessions_live
  ON participant_sessions(expires_at)
  WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE effect_handles (
  id TEXT PRIMARY KEY,
  class TEXT NOT NULL,
  root_id TEXT NOT NULL DEFAULT '',
  relative_path TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL,
  participant_scope TEXT NOT NULL,
  binding_digest TEXT NOT NULL DEFAULT '',
  capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities)),
  issued_at DATETIME NOT NULL,
  expires_at DATETIME NOT NULL,
  max_uses INTEGER NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
  used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
  revoked_at DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_effect_handles_live
  ON effect_handles(expires_at)
  WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_effect_handles_interaction
  ON effect_handles(interaction_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE effect_receipts (
  id TEXT PRIMARY KEY,
  capability TEXT NOT NULL,
  decision TEXT NOT NULL CHECK (decision IN ('granted', 'refused')),
  code TEXT NOT NULL DEFAULT '',
  mediation TEXT NOT NULL CHECK (mediation IN ('host', 'declared', 'unimplemented')),
  handle_id TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL,
  participant_scope TEXT NOT NULL,
  binding_digest TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL,
  request_digest TEXT NOT NULL,
  intent_control_id TEXT NOT NULL DEFAULT '',
  intent_revision INTEGER NOT NULL DEFAULT 0,
  issued_at DATETIME NOT NULL,
  byte_count INTEGER NOT NULL DEFAULT 0,
  content_sha256 TEXT NOT NULL DEFAULT '',
  media_type TEXT NOT NULL DEFAULT ''
, renderer_trust_class TEXT NOT NULL DEFAULT '', renderer_isolation TEXT NOT NULL DEFAULT '');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_effect_receipts_idempotency
  ON effect_receipts(idempotency_key, capability);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_effect_receipts_interaction
  ON effect_receipts(interaction_id, issued_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE telemetry_events (
  event_id TEXT PRIMARY KEY,

  -- W3C-shaped correlation. trace_id is 32 lowercase hex characters and
  -- span_id 16, so a row is exportable as an OpenTelemetry span without
  -- reformatting. parent_span_id is NULL for a root observation.
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  parent_span_id TEXT,

  -- event_name is a closed vocabulary owned by internal/telemetry. outcome is
  -- one of ok / refused / failed. code is a *typed* code — a refusal code, an
  -- error code, a state name — and never a message: ADR 0002 §8 classifies
  -- adapter freeform text as caller payload precisely so that an
  -- implementation reading this column does not reach for error_message.
  event_name TEXT NOT NULL,
  outcome TEXT NOT NULL,
  code TEXT NOT NULL DEFAULT '',

  occurred_at TIMESTAMP NOT NULL,
  -- duration_ms is NULL for an instantaneous observation. It is the only
  -- measurement stored per row; distributions are aggregated in-process.
  duration_ms INTEGER,

  -- The correlation dimensions, promoted to columns because they are what a
  -- query filters on. Every one of them is a Tangent identifier or a
  -- host-assigned label.
  surface_id TEXT NOT NULL DEFAULT '',
  interaction_id TEXT NOT NULL DEFAULT '',
  room_id TEXT NOT NULL DEFAULT '',
  envelope_id TEXT NOT NULL DEFAULT '',
  connection_id TEXT NOT NULL DEFAULT '',
  definition_kind TEXT NOT NULL DEFAULT '',
  definition_version TEXT NOT NULL DEFAULT '',
  caller_scope TEXT NOT NULL DEFAULT '',
  participant_scope TEXT NOT NULL DEFAULT '',

  -- attributes is a JSON object whose keys are drawn from the allowlist in
  -- internal/telemetry/attributes.go and whose values are bounded scalars.
  -- Anything else is dropped before it reaches this column.
  attributes TEXT NOT NULL DEFAULT '{}'
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_telemetry_events_trace ON telemetry_events(trace_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_telemetry_events_interaction ON telemetry_events(interaction_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_telemetry_events_name ON telemetry_events(event_name, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_telemetry_events_occurred ON telemetry_events(occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE retention_operations (
  operation_id TEXT PRIMARY KEY,

  -- The six kinds are deliberately not one `delete` verb. ADR 0002 and the
  -- acceptance for this work both require that an operator can tell a
  -- capability lapsing from a draft being dropped from a payload being
  -- redacted from a surface being closed from content Tangent never held.
  -- They have different authority and different consequences, and collapsing
  -- them is how a `close` becomes mistaken for an `erase`.
  --
  --   capability-expiry        A live grant is withdrawn or swept. Removes an
  --                            effect_handles row. No participant content.
  --   draft-deletion           A draft revision's payload is replaced by a
  --                            tombstone and draft_revision_tombstones gets
  --                            its first writer.
  --   payload-redaction        A caller request, a participant resolution, or
  --                            adapter freeform text is replaced by the typed
  --                            tombstone of ADR 0002 §2.
  --   surface-close            A lifecycle transition. Removes nothing. It is
  --                            recorded here precisely so that a reader of
  --                            this log can see that it removed nothing.
  --   surface-purge            The cascade delete. The only kind that destroys
  --                            record skeletons, and never automatic.
  --   external-source-deletion Tangent removes nothing and cannot: the content
  --                            was never here. Recorded so the refusal is
  --                            evidence rather than silence.
  kind TEXT NOT NULL CHECK (kind IN (
    'capability-expiry',
    'draft-deletion',
    'payload-redaction',
    'surface-close',
    'surface-purge',
    'external-source-deletion'
  )),

  -- What the operation was aimed at. Both may be empty for a sweep that names
  -- no single target (capability expiry across the whole store, for example).
  target_surface_id TEXT NOT NULL DEFAULT '',
  target_interaction_id TEXT NOT NULL DEFAULT '',

  -- Who asked, and under what authority. `local-user` is an explicit operator
  -- request; `host-policy` is a window expiry the host configured;
  -- `maintenance` is a repair or compaction that touched no content.
  actor_ref TEXT NOT NULL,
  authority TEXT NOT NULL CHECK (authority IN ('local-user', 'host-policy', 'maintenance')),
  policy_ref TEXT NOT NULL DEFAULT '',

  requested_at DATETIME NOT NULL,
  completed_at DATETIME,

  -- `refused` is a first-class outcome, not an error. An operation that
  -- correctly declined — because the target was not eligible, because content
  -- lives outside Tangent, because another process owned the database — is a
  -- fact worth keeping.
  outcome TEXT NOT NULL CHECK (outcome IN ('applied', 'refused', 'failed')),
  code TEXT NOT NULL DEFAULT '',
  affected_rows INTEGER NOT NULL DEFAULT 0 CHECK (affected_rows >= 0),

  -- A JSON array of {table, column, ref, sha256, bytes}. Digests only; the
  -- CHECK is the schema's half of that promise and internal/db's redaction
  -- path is the other half.
  removed_digests TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(removed_digests)),

  -- ADR 0002 §7: removing content from the live database does not remove it
  -- from a backup taken earlier, and the operation records which backups are
  -- known to be affected rather than claiming to have cleaned them.
  --
  -- `backup_survey` is the honest part. `not-surveyed` means nobody looked,
  -- which is the default and is not the same as "there are none".
  known_backups TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(known_backups)),
  backup_survey TEXT NOT NULL DEFAULT 'not-surveyed'
    CHECK (backup_survey IN ('not-surveyed', 'surveyed', 'survey-failed')),

  -- The schema the operation ran against, and whether the immutability guards
  -- were verified back in place when it finished. A zero here on a row whose
  -- kind needed the maintenance path is the signature of the failure mode ADR
  -- 0002 warns about: a database left without its guards.
  schema_version INTEGER NOT NULL DEFAULT 0,
  guards_restored INTEGER NOT NULL DEFAULT 0 CHECK (guards_restored IN (0, 1))
, target_channel_id TEXT NOT NULL DEFAULT '', target_exchange_id TEXT NOT NULL DEFAULT '');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_requested ON retention_operations(requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_kind ON retention_operations(kind, requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_surface ON retention_operations(target_surface_id, requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_interaction ON retention_operations(target_interaction_id, requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE "surface_open_requests" (
  caller_scope TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  surface_id TEXT NOT NULL UNIQUE REFERENCES surfaces(id) ON DELETE CASCADE,
  request_snapshot TEXT NOT NULL CHECK (json_valid(request_snapshot)),
  created_at DATETIME NOT NULL,
  PRIMARY KEY (caller_scope, idempotency_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  owner_scope TEXT NOT NULL,
  project_ref TEXT NOT NULL DEFAULT '',
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  archived_at DATETIME
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channels_owner_scope ON channels(owner_scope, updated_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channels_project_ref ON channels(project_ref) WHERE project_ref != '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channel_subjects (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  subject_type TEXT NOT NULL CHECK (subject_type IN ('interaction', 'surface', 'artifact', 'freeform')),
  interaction_id TEXT REFERENCES interactions(id) ON DELETE SET NULL,
  surface_id TEXT REFERENCES surfaces(id) ON DELETE SET NULL,
  external_ref TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'archived')),
  worker_provenance TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(worker_provenance)),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  -- This CHECK holds type exclusivity forever — an interaction subject never
  -- carries a surface_id and vice versa — but it deliberately does not
  -- require the referenced id to still be *present*. internal/db.PurgeSurface
  -- (ADR 0002) can delete the surface or interaction a subject correlates,
  -- and this row's FK is ON DELETE SET NULL specifically so the thread
  -- survives that as a tombstone rather than being cascaded away with the
  -- content it once pointed at. SQLite evaluates a CHECK on every row UPDATE,
  -- including one an FK action performs internally, so a CHECK requiring
  -- non-NULL here would abort the purge transaction the moment SET NULL
  -- tried to fire — this shape is what makes the two coexist. "Non-NULL at
  -- creation" is enforced one layer up, by internal/channel's AddSubject;
  -- Subject.ReferentPurged reports the tombstone state to a reader.
  CHECK (
    (subject_type = 'interaction' AND surface_id IS NULL)
    OR (subject_type = 'surface' AND interaction_id IS NULL)
    OR (subject_type IN ('artifact', 'freeform') AND interaction_id IS NULL AND surface_id IS NULL)
  )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channel_subjects_channel ON channel_subjects(channel_id, status, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channel_subjects_interaction ON channel_subjects(interaction_id) WHERE interaction_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channel_subjects_surface ON channel_subjects(surface_id) WHERE surface_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE participants (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('operator', 'agent')),
  external_authority TEXT NOT NULL DEFAULT '',
  external_ref TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_participants_external_identity
  ON participants(external_authority, external_ref)
  WHERE external_ref != '';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channel_participants (
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
  joined_at DATETIME NOT NULL,
  left_at DATETIME,
  PRIMARY KEY (channel_id, participant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_channel_participants_participant ON channel_participants(participant_id, joined_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channel_participant_bindings (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL,
  participant_id TEXT NOT NULL,
  generation INTEGER NOT NULL CHECK (generation > 0),
  runtime_authority TEXT NOT NULL,
  runtime_endpoint_ref TEXT NOT NULL DEFAULT '',
  adapter_capabilities TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(adapter_capabilities)),
  bound_at DATETIME NOT NULL,
  superseded_at DATETIME,
  FOREIGN KEY (channel_id, participant_id) REFERENCES channel_participants(channel_id, participant_id) ON DELETE CASCADE,
  UNIQUE (channel_id, participant_id, generation)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_channel_participant_bindings_current
  ON channel_participant_bindings(channel_id, participant_id)
  WHERE superseded_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channel_view_focus (
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  view_ref TEXT NOT NULL,
  focused_subject_id TEXT REFERENCES channel_subjects(id) ON DELETE SET NULL,
  focused_participant_id TEXT REFERENCES participants(id) ON DELETE SET NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (channel_id, view_ref)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE exchanges (
  id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  subject_id TEXT REFERENCES channel_subjects(id) ON DELETE SET NULL,
  sender_participant_id TEXT NOT NULL REFERENCES participants(id),
  recipient_participant_id TEXT NOT NULL REFERENCES participants(id),
  -- The exact runtime-binding generation live at accept time, frozen here
  -- (ADR 0006 §3, "explicit destination binding generation"). NULL means no
  -- live binding existed when the exchange was accepted — a legitimate
  -- "queued, awaiting-peer" state, not an error. channel_participant_bindings
  -- rows are themselves never deleted (0064), so SET NULL here is dormant
  -- today; it documents the same "may outlive its target" contract every
  -- other optional reference in this schema uses.
  recipient_binding_id TEXT REFERENCES channel_participant_bindings(id) ON DELETE SET NULL,
  reply_to_exchange_id TEXT REFERENCES exchanges(id) ON DELETE SET NULL,
  body TEXT NOT NULL,
  -- The replay cursor. Monotonic per recipient_participant_id, assigned
  -- inside the same transaction as the row's own insert. A caller's
  -- `receive(cursor, wait_ms)` resumes with `sequence > cursor`; the cursor
  -- itself is never persisted server-side per destination — the caller holds
  -- it, the same stateless-cursor shape the rest of this codebase uses.
  sequence INTEGER NOT NULL CHECK (sequence > 0),
  created_at DATETIME NOT NULL,
  UNIQUE (sender_participant_id, idempotency_key),
  UNIQUE (recipient_participant_id, sequence)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_exchanges_channel ON exchanges(channel_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_exchanges_subject ON exchanges(subject_id) WHERE subject_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE exchange_outbox (
  exchange_id TEXT PRIMARY KEY REFERENCES exchanges(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'leased', 'delivered', 'failed')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  leased_by TEXT NOT NULL DEFAULT '',
  leased_at DATETIME,
  lease_expires_at DATETIME,
  next_attempt_at DATETIME,
  last_error TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  -- A crash boundary is exactly "leased_at was set and nothing ever cleared
  -- it": this CHECK holds only the shape (leased carries both timestamps,
  -- everything else carries neither), so it can never conflict with an FK
  -- action the way 0064's CHECK did — no foreign key touches these columns.
  CHECK (
    (status = 'leased' AND leased_at IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status != 'leased' AND leased_at IS NULL AND lease_expires_at IS NULL)
  )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_exchange_outbox_claimable ON exchange_outbox(status, lease_expires_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE exchange_delivery_receipts (
  id TEXT PRIMARY KEY,
  exchange_id TEXT NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
  -- Three outcomes, not two: "uncertain" (a timeout or a transport error with
  -- no confirmation either way) is not the same fact as "failed" (the peer
  -- or transport gave a definite negative), and collapsing them is exactly
  -- what would let a timeout be silently treated as a permanent failure or a
  -- success. ARCHITECTURE.md §5: "UI states distinguish queued,
  -- awaiting-peer, accepted-by-peer, failed/unknown."
  outcome TEXT NOT NULL CHECK (outcome IN ('delivered', 'failed', 'uncertain')),
  runtime_authority TEXT NOT NULL DEFAULT '',
  runtime_endpoint_ref TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  attempted_at DATETIME NOT NULL,
  UNIQUE (exchange_id, attempt_number)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_exchange_delivery_receipts_exchange ON exchange_delivery_receipts(exchange_id, attempt_number);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE exchange_reads (
  exchange_id TEXT NOT NULL REFERENCES exchanges(id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL REFERENCES participants(id),
  acked_at DATETIME NOT NULL,
  PRIMARY KEY (exchange_id, participant_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE participant_presence (
  participant_id TEXT PRIMARY KEY REFERENCES participants(id) ON DELETE CASCADE,
  last_seen_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_channel ON retention_operations(target_channel_id, requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_retention_operations_exchange ON retention_operations(target_exchange_id, requested_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE docs_read_receipts (
  interaction_id TEXT PRIMARY KEY,
  read_at        DATETIME NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE inbox_order (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    interaction_id TEXT NOT NULL UNIQUE REFERENCES interactions(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE inbox_hidden_items (
  interaction_id TEXT PRIMARY KEY REFERENCES interactions(id) ON DELETE CASCADE,
  hidden_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE event_outbox (
  event_id TEXT PRIMARY KEY,
  stream TEXT NOT NULL DEFAULT 'tangent/approvals',
  kind TEXT NOT NULL,
  subject_type TEXT,
  subject_id TEXT,
  envelope_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  appended_at TEXT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX event_outbox_pending ON event_outbox (created_at) WHERE appended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX event_outbox_subject ON event_outbox (subject_id, kind);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE event_outbox_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  generation INTEGER NOT NULL CHECK (generation > 0),
  last_sequence INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE stream_log_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE stream_log_streams (
  stream         TEXT PRIMARY KEY,
  latest_seq     INTEGER NOT NULL DEFAULT 0 CHECK (latest_seq >= 0),
  pruned_through INTEGER NOT NULL DEFAULT 0 CHECK (pruned_through >= 0 AND pruned_through <= latest_seq),
  dropped        INTEGER NOT NULL DEFAULT 0 CHECK (dropped >= 0)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE stream_log_events (
  stream       TEXT NOT NULL REFERENCES stream_log_streams(stream),
  seq          INTEGER NOT NULL CHECK (seq >= 1),
  event_id     TEXT NOT NULL UNIQUE CHECK (event_id <> ''),
  kind         TEXT NOT NULL DEFAULT '',
  subject_type TEXT NOT NULL DEFAULT '',
  subject_id   TEXT NOT NULL DEFAULT '',
  recorded_at  TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  envelope     TEXT,
  PRIMARY KEY (stream, seq)
) WITHOUT ROWID;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_stream_log_events_recorded ON stream_log_events (stream, recorded_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_stream_log_events_subject ON stream_log_events (subject_type, subject_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE stream_log_gaps (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  stream       TEXT NOT NULL,
  reason       TEXT NOT NULL,
  from_seq     INTEGER NOT NULL,
  through_seq  INTEGER NOT NULL,
  dropped      INTEGER NOT NULL,
  recorded_at  TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_stream_log_gaps_stream ON stream_log_gaps (stream, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE event_outbox_skipped (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  item_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  error_class TEXT NOT NULL,
  created_at TEXT NOT NULL,
  repaired_at TEXT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX event_outbox_skipped_open ON event_outbox_skipped (id) WHERE repaired_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE artifact_revisions (
  interaction_id TEXT    NOT NULL,
  revision       INTEGER NOT NULL CHECK (revision > 0),
  filename       TEXT    NOT NULL,
  media_type     TEXT    NOT NULL,
  size_bytes     INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256         TEXT    NOT NULL,
  rel_path       TEXT    NOT NULL,
  note           TEXT    NOT NULL DEFAULT '',
  created_at     DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, revision)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE artifact_messages (
  interaction_id TEXT    NOT NULL,
  seq            INTEGER NOT NULL CHECK (seq > 0),
  author         TEXT    NOT NULL CHECK (author IN ('operator', 'agent')),
  kind           TEXT    NOT NULL CHECK (kind IN ('message', 'changes_requested', 'approved')),
  body           TEXT    NOT NULL,
  revision       INTEGER NOT NULL DEFAULT 0,
  created_at     DATETIME NOT NULL,
  PRIMARY KEY (interaction_id, seq)
);
-- +goose StatementEnd
