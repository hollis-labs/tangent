-- Versioned interaction-definition registry (CW-20260825-0065),
-- implementing docs/adr/0003-definition-and-package-ownership.md.
--
-- Two things happen here.
--
-- 1. definition_manifests retains the exact material a pinned interaction was
--    cut from, keyed on the binding digest. Without it, "validate this
--    interaction against the definition it pinned" only works while the
--    process is up and the current registry still matches: the in-memory
--    version index is rebuilt from the embedded package tree at boot, so a
--    release that bumps a kind's version leaves every interaction pinned to
--    the old one with nothing to validate against. ADR 0001 §3 and ADR 0003
--    §8 C1 both require the opposite — a pinned binding is never
--    reinterpreted, which presupposes the original bytes still exist.
--
-- 2. definition_bindings grows the rest of the manifest identity ADR 0003 §2.9
--    puts in binding_digest, so a stored binding can be inspected without
--    re-deriving it, and legacy `revision` values are normalized.

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

CREATE INDEX idx_definition_manifests_kind ON definition_manifests(kind, version, revision);

-- Retained material is immutable for the same reason definition_bindings is:
-- an interaction that can no longer be validated against the bytes it pinned
-- has lost the guarantee the pin exists to make.
CREATE TRIGGER definition_manifests_immutable_update
BEFORE UPDATE ON definition_manifests
BEGIN
  SELECT RAISE(ABORT, 'retained definition manifests are immutable');
END;

CREATE TRIGGER definition_manifests_immutable_delete
BEFORE DELETE ON definition_manifests
BEGIN
  SELECT RAISE(ABORT, 'retained definition manifests are immutable');
END;

-- Extend the pinned projection with the rest of ADR 0003 §2.9's identity.
-- Every column is nullable: rows written before this migration have no
-- authored manifest to project, and backfilling a value Tangent never derived
-- would be inventing history.
ALTER TABLE definition_bindings ADD COLUMN manifest_digest TEXT;
ALTER TABLE definition_bindings ADD COLUMN contract_digest TEXT;
ALTER TABLE definition_bindings ADD COLUMN response_schema_digest TEXT;
ALTER TABLE definition_bindings ADD COLUMN package_id TEXT;
ALTER TABLE definition_bindings ADD COLUMN package_version TEXT;
ALTER TABLE definition_bindings ADD COLUMN ownership_class TEXT;
ALTER TABLE definition_bindings ADD COLUMN compatibility_class TEXT;
ALTER TABLE definition_bindings ADD COLUMN renderer_id TEXT;
ALTER TABLE definition_bindings ADD COLUMN renderer_class TEXT;
ALTER TABLE definition_bindings ADD COLUMN renderer_trust_class TEXT;
ALTER TABLE definition_bindings ADD COLUMN required_capabilities TEXT;
ALTER TABLE definition_bindings ADD COLUMN granted_capabilities TEXT;
ALTER TABLE definition_bindings ADD COLUMN materialization_state TEXT;

-- Before this migration, catalog.go set `Revision: spec.Version`, so the
-- column held a copy of the version and carried no information. ADR 0003 §3
-- keeps revision as a genuinely separate monotonic field, and every shipped
-- manifest authors revision 1, so existing rows are normalized to '1' rather
-- than left holding a value that now means something else.
--
-- The immutability triggers are dropped around the rewrite and recreated
-- verbatim. That is deliberate and bounded: a schema migration is the one
-- audited context in which these rows may be touched, and leaving the triggers
-- in place would leave the column permanently mistyped instead.
DROP TRIGGER definition_bindings_immutable_update;

UPDATE definition_bindings SET revision = '1';

CREATE TRIGGER definition_bindings_immutable_update
BEFORE UPDATE ON definition_bindings
BEGIN
  SELECT RAISE(ABORT, 'definition bindings are immutable');
END;
