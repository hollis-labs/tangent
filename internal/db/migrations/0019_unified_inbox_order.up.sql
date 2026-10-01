-- One arrival order across all interaction surfaces. This is an index of
-- canonical interactions, never a second interaction or resolution record.
CREATE TABLE inbox_order (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    interaction_id TEXT NOT NULL UNIQUE REFERENCES interactions(id) ON DELETE CASCADE
);
INSERT INTO inbox_order (interaction_id)
SELECT id FROM interactions ORDER BY created_at, rowid;
CREATE TRIGGER interaction_inbox_arrival AFTER INSERT ON interactions
BEGIN
    INSERT INTO inbox_order (interaction_id) VALUES (NEW.id);
END;
