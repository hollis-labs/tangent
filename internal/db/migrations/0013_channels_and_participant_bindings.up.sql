-- Channels, threads (subjects), participant references and explicit runtime
-- bindings (CW-20260906-0064), implementing
-- docs/adr/0006-collaboration-surface-and-relay-boundary.md §3 (the channel /
-- thread / participant reference / runtime binding vocabulary).
--
-- A channel is a persistent collaboration context, independent of any
-- runtime session. It is not an agent session, a room, a project, or a task.
-- A room stays a compatibility projection of a surface (0001, 0003) and is
-- not touched or referenced here: a channel associates with the existing
-- surface/interaction substrate, it does not replace or rename it.
--
-- Worker provenance is deliberately not a participant. ADR 0006 §5 refuses a
-- worker topology API: a conductor's workers may appear as opaque provenance
-- on a subject, and never become channel members or recipients
-- automatically. channel_subjects.worker_provenance is a JSON blob for
-- exactly that reason — it carries no foreign key to participants.
--
-- Rebinding a participant's runtime destination is explicit and advances a
-- generation counter (ADR 0006 §3, "Runtime binding"); there is no automatic
-- choice of the newest session sharing a name. The partial unique index on
-- channel_participant_bindings(channel_id, participant_id) WHERE
-- superseded_at IS NULL makes "the current binding" a fact the schema
-- enforces rather than a convention callers must maintain.
--
-- Project and channel groupings are navigation metadata, not isolation
-- boundaries (ADR 0006 §3; internal/authz's AdvisoryPartitionNotice says the
-- same about caller partitions). Nothing in this migration enforces
-- owner_scope or project_ref as access control — that stays advisory.

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

CREATE INDEX idx_channels_owner_scope ON channels(owner_scope, updated_at);
CREATE INDEX idx_channels_project_ref ON channels(project_ref) WHERE project_ref != '';

-- A thread/subject correlates one discussion, artifact, or interaction
-- inside a channel. It is not a task graph or a workflow phase. Several
-- subjects may be pending in the same channel at once — unlike a room, which
-- admits one pending envelope — so there is deliberately no uniqueness
-- constraint limiting a channel to a single pending subject.
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

CREATE INDEX idx_channel_subjects_channel ON channel_subjects(channel_id, status, created_at);
CREATE INDEX idx_channel_subjects_interaction ON channel_subjects(interaction_id) WHERE interaction_id IS NOT NULL;
CREATE INDEX idx_channel_subjects_surface ON channel_subjects(surface_id) WHERE surface_id IS NOT NULL;

-- A participant reference is an explicit user-facing communication identity.
-- External identity remains authoritative: external_authority/external_ref
-- are self-asserted by the peer (the cooperative-loop spike found every
-- session improvised these differently), never verified here. The partial
-- unique index lets a repeated registration of the same asserted identity
-- resolve to the same participant row, which is what makes "one participant
-- across channels" possible.
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

CREATE UNIQUE INDEX idx_participants_external_identity
  ON participants(external_authority, external_ref)
  WHERE external_ref != '';

-- Channel membership: many-to-many. One participant may belong to several
-- channels; a channel may hold several participants. left_at ends membership
-- without deleting the row, so binding history (below) keeps its parent.
CREATE TABLE channel_participants (
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
  joined_at DATETIME NOT NULL,
  left_at DATETIME,
  PRIMARY KEY (channel_id, participant_id)
);

CREATE INDEX idx_channel_participants_participant ON channel_participants(participant_id, joined_at);

-- Runtime binding: the exact external authority, endpoint/session, adapter
-- capabilities, and generation a channel participant's destination resolves
-- to. It is not the agent — it is append-only history of what the agent
-- resolved to at each explicit rebind. Exactly one row per (channel_id,
-- participant_id) may have superseded_at IS NULL at a time; the partial
-- unique index below is the enforcement, not just a convention.
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

CREATE UNIQUE INDEX idx_channel_participant_bindings_current
  ON channel_participant_bindings(channel_id, participant_id)
  WHERE superseded_at IS NULL;

-- View state: one concrete window/pane has its own focus, independent of
-- every other view on the same channel. view_ref is an opaque,
-- caller-supplied identity for the window/pane; Tangent does not interpret
-- it. There is deliberately no single "current subject" column on channels
-- itself — that would force one global focus across every view, which is
-- exactly the property multi-view focus independence must not have.
CREATE TABLE channel_view_focus (
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  view_ref TEXT NOT NULL,
  focused_subject_id TEXT REFERENCES channel_subjects(id) ON DELETE SET NULL,
  focused_participant_id TEXT REFERENCES participants(id) ON DELETE SET NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (channel_id, view_ref)
);
