-- Rolling back drops every participant session. That is the correct behavior:
-- a session is capability material, and a rollback that left orphaned grants
-- behind would leave authority in a schema that no longer enforces it. Every
-- browser simply mints a fresh session on its next page load.

DROP INDEX IF EXISTS idx_participant_sessions_live;
DROP TABLE IF EXISTS participant_sessions;
