-- Rolling back drops the whole channel/participant substrate. Nothing here
-- is referenced by an existing table: interactions and surfaces only ever
-- gain a nullable back-reference from channel_subjects, never the reverse,
-- so this migration's tables are the only ones in scope for the rollback.

DROP TABLE IF EXISTS channel_view_focus;

DROP INDEX IF EXISTS idx_channel_participant_bindings_current;
DROP TABLE IF EXISTS channel_participant_bindings;

DROP INDEX IF EXISTS idx_channel_participants_participant;
DROP TABLE IF EXISTS channel_participants;

DROP INDEX IF EXISTS idx_participants_external_identity;
DROP TABLE IF EXISTS participants;

DROP INDEX IF EXISTS idx_channel_subjects_surface;
DROP INDEX IF EXISTS idx_channel_subjects_interaction;
DROP INDEX IF EXISTS idx_channel_subjects_channel;
DROP TABLE IF EXISTS channel_subjects;

DROP INDEX IF EXISTS idx_channels_project_ref;
DROP INDEX IF EXISTS idx_channels_owner_scope;
DROP TABLE IF EXISTS channels;
