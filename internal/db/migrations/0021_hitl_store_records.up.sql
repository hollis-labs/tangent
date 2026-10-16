-- Durable records for the go-hitl Store (libs/mesh/core/hitl). One row is one
-- hitl.Record: the lib's compare-and-set unit. Kept apart from `interactions`
-- because the lib's revision and lifecycle model (created presented at
-- revision 1, advancing by exactly one) is not the interaction table's.
CREATE TABLE hitl_store_records (
  item_id TEXT PRIMARY KEY,
  caller_scope TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  digest TEXT NOT NULL,
  kind TEXT NOT NULL,
  request TEXT NOT NULL CHECK (json_valid(request)),
  state TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK (revision > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  expires_at_ns INTEGER,
  outcome TEXT CHECK (outcome IS NULL OR json_valid(outcome)),
  UNIQUE (caller_scope, idempotency_key),
  CHECK ((outcome IS NULL) = (state NOT IN ('resolved', 'canceled', 'expired', 'failed', 'superseded')))
);

CREATE INDEX idx_hitl_store_records_due ON hitl_store_records(expires_at_ns, item_id)
  WHERE outcome IS NULL AND expires_at_ns IS NOT NULL;
