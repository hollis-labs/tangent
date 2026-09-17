-- Read-receipt tracking for the Docs inbox (CW-20260917-0009).
--
-- The generic interaction substrate has no notion of "read" distinct from
-- "resolved" — resolution is a terminal decision (approve, reject, dismiss),
-- while a document's read state is an orthogonal, non-terminal fact the
-- operator sets deliberately. Rather than teach the shared interactions
-- table a concept only the Docs package needs, this is a small side table
-- scoped to that package alone: presence of a row means read, absence means
-- unread. It carries no foreign key enforcement because interaction rows are
-- never deleted, so there is nothing to cascade.

CREATE TABLE docs_read_receipts (
  interaction_id TEXT PRIMARY KEY,
  read_at        DATETIME NOT NULL
);
