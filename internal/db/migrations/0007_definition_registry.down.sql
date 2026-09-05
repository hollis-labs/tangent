-- Reverse of 0007_definition_registry.up.sql.
--
-- The added definition_bindings columns are dropped and the retained material
-- table is removed. The `revision` normalization is NOT reversed: the previous
-- value was a copy of `version` that carried no information, and recomputing it
-- would write a value this schema no longer means. A rollback therefore leaves
-- revision = '1', which is the honest state.

DROP TRIGGER IF EXISTS definition_manifests_immutable_delete;
DROP TRIGGER IF EXISTS definition_manifests_immutable_update;
DROP INDEX IF EXISTS idx_definition_manifests_kind;
DROP TABLE IF EXISTS definition_manifests;

ALTER TABLE definition_bindings DROP COLUMN materialization_state;
ALTER TABLE definition_bindings DROP COLUMN granted_capabilities;
ALTER TABLE definition_bindings DROP COLUMN required_capabilities;
ALTER TABLE definition_bindings DROP COLUMN renderer_trust_class;
ALTER TABLE definition_bindings DROP COLUMN renderer_class;
ALTER TABLE definition_bindings DROP COLUMN renderer_id;
ALTER TABLE definition_bindings DROP COLUMN compatibility_class;
ALTER TABLE definition_bindings DROP COLUMN ownership_class;
ALTER TABLE definition_bindings DROP COLUMN package_version;
ALTER TABLE definition_bindings DROP COLUMN package_id;
ALTER TABLE definition_bindings DROP COLUMN response_schema_digest;
ALTER TABLE definition_bindings DROP COLUMN contract_digest;
ALTER TABLE definition_bindings DROP COLUMN manifest_digest;
