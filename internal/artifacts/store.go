package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store holds the mutable half of an artifact: revision rows and message rows
// in SQLite, and revision files on disk under dir. The interaction record is
// immutable identity; nothing here touches it (ADR 0015 §3).
type Store struct {
	db  *sql.DB
	dir string
	now func() time.Time
}

// NewStore returns a store writing revision files under dir. dir is created on
// first write.
func NewStore(db *sql.DB, dir string) *Store {
	return &Store{db: db, dir: dir, now: func() time.Time { return time.Now().UTC() }}
}

// Dir is the directory revision files live under.
func (s *Store) Dir() string { return s.dir }

// safeID admits the interaction ids Tangent generates and nothing that could
// climb out of the artifact directory.
func safeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// AddRevision stores file as the next revision of interactionID and returns it.
// The row is written after the file, so a crash leaves at worst an orphan file
// and never a row pointing at nothing.
func (s *Store) AddRevision(ctx context.Context, interactionID string, file FileInput) (Revision, error) {
	rev, _, err := s.addRevision(ctx, interactionID, file, false)
	return rev, err
}

// AddFirstRevision stores file as revision 1 only when the artifact has no
// revision yet, and reports whether it did. An idempotent replay of a create
// therefore never adds content twice, even when two replays race.
func (s *Store) AddFirstRevision(ctx context.Context, interactionID string, file FileInput) (Revision, bool, error) {
	return s.addRevision(ctx, interactionID, file, true)
}

func (s *Store) addRevision(ctx context.Context, interactionID string, file FileInput, onlyIfEmpty bool) (Revision, bool, error) {
	if !safeID(interactionID) {
		return Revision{}, false, fmt.Errorf("%w: bad item id", ErrInvalidRequest)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Revision{}, false, fmt.Errorf("artifacts store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var latest sql.NullInt64
	if err = tx.QueryRowContext(ctx,
		`SELECT MAX(revision) FROM artifact_revisions WHERE interaction_id = ?`, interactionID,
	).Scan(&latest); err != nil {
		return Revision{}, false, fmt.Errorf("artifacts store: next revision: %w", err)
	}
	if onlyIfEmpty && latest.Valid {
		return Revision{}, false, nil
	}
	next := int(latest.Int64) + 1
	rel := filepath.Join(interactionID, fmt.Sprintf("%d", next), file.Filename)
	abs := filepath.Join(s.dir, rel)
	if err = os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return Revision{}, false, fmt.Errorf("artifacts store: create directory: %w", err)
	}
	if err = writeFileAtomic(abs, file.Data); err != nil {
		return Revision{}, false, fmt.Errorf("artifacts store: write file: %w", err)
	}
	if len(file.Rendered) > 0 {
		if err = writeFileAtomic(filepath.Join(filepath.Dir(abs), renderedFilename), file.Rendered); err != nil {
			_ = os.Remove(abs)
			return Revision{}, false, fmt.Errorf("artifacts store: write rendered picture: %w", err)
		}
	}
	sum := sha256.Sum256(file.Data)
	rev := Revision{
		Revision:  next,
		Filename:  file.Filename,
		MediaType: file.MediaType,
		SizeBytes: int64(len(file.Data)),
		SHA256:    hex.EncodeToString(sum[:]),
		Note:      file.Note,
		CreatedAt: s.now(),
		Rendered:  len(file.Rendered) > 0,
		relPath:   rel,
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO artifact_revisions (interaction_id, revision, filename, media_type, size_bytes, sha256, rel_path, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		interactionID, rev.Revision, rev.Filename, rev.MediaType, rev.SizeBytes, rev.SHA256, rel, rev.Note, rev.CreatedAt,
	); err != nil {
		_ = os.Remove(abs)
		return Revision{}, false, fmt.Errorf("artifacts store: insert revision: %w", err)
	}
	if err = tx.Commit(); err != nil {
		_ = os.Remove(abs)
		return Revision{}, false, fmt.Errorf("artifacts store: commit revision: %w", err)
	}
	return rev, true, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err = os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// RevisionsBulk returns the revisions of every id, oldest first.
func (s *Store) RevisionsBulk(ctx context.Context, ids []string) (map[string][]Revision, error) {
	out := make(map[string][]Revision, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	// placeholders is a run of `?,` sized to len(ids); values are bound.
	query := `SELECT interaction_id, revision, filename, media_type, size_bytes, sha256, rel_path, note, created_at
		FROM artifact_revisions WHERE interaction_id IN (` + placeholders + `) ORDER BY interaction_id, revision` // #nosec G202
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("artifacts store: query revisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var r Revision
		if err = rows.Scan(&id, &r.Revision, &r.Filename, &r.MediaType, &r.SizeBytes, &r.SHA256, &r.relPath, &r.Note, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("artifacts store: scan revision: %w", err)
		}
		r.Rendered = s.hasRendered(r.relPath)
		out[id] = append(out[id], r)
	}
	return out, rows.Err()
}

// hasRendered reports whether a revision has a server-drawn picture beside its source.
func (s *Store) hasRendered(rel string) bool {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(s.dir, filepath.Dir(clean), renderedFilename)) //nolint:gosec // clean is checked above
	return err == nil && info.Mode().IsRegular()
}

// OpenRendered opens a revision's server-drawn picture. The caller closes it.
func (s *Store) OpenRendered(ctx context.Context, id string, revision int) (Revision, *os.File, error) {
	rev, source, err := s.OpenRevision(ctx, id, revision)
	if err != nil {
		return Revision{}, nil, err
	}
	_ = source.Close()
	if !s.hasRendered(rev.relPath) {
		return Revision{}, nil, ErrNotFound
	}
	file, err := os.Open(filepath.Join(s.dir, filepath.Dir(filepath.Clean(rev.relPath)), renderedFilename)) //nolint:gosec // path derived from a validated stored path
	if err != nil {
		return Revision{}, nil, ErrNotFound
	}
	rev.MediaType = "image/svg+xml"
	rev.Filename = renderedFilename
	return rev, file, nil
}

// Revisions returns one artifact's revisions, oldest first.
func (s *Store) Revisions(ctx context.Context, id string) ([]Revision, error) {
	all, err := s.RevisionsBulk(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	return all[id], nil
}

// OpenRevision opens the file of one revision. The caller closes it.
func (s *Store) OpenRevision(ctx context.Context, id string, revision int) (Revision, *os.File, error) {
	if !safeID(id) {
		return Revision{}, nil, ErrNotFound
	}
	var r Revision
	err := s.db.QueryRowContext(ctx,
		`SELECT revision, filename, media_type, size_bytes, sha256, rel_path, note, created_at
		 FROM artifact_revisions WHERE interaction_id = ? AND revision = ?`, id, revision,
	).Scan(&r.Revision, &r.Filename, &r.MediaType, &r.SizeBytes, &r.SHA256, &r.relPath, &r.Note, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, nil, ErrNotFound
	}
	if err != nil {
		return Revision{}, nil, fmt.Errorf("artifacts store: load revision: %w", err)
	}
	clean := filepath.Clean(r.relPath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return Revision{}, nil, ErrNotFound
	}
	// clean is a relative path read back from our own table and checked above to
	// stay inside s.dir; callers never supply it.
	file, err := os.Open(filepath.Join(s.dir, clean)) //nolint:gosec // see comment above
	if errors.Is(err, os.ErrNotExist) {
		return Revision{}, nil, ErrNotFound
	}
	if err != nil {
		return Revision{}, nil, fmt.Errorf("artifacts store: open file: %w", err)
	}
	return r, file, nil
}

// ReadRevision reads a whole revision file; used for small text types.
func (s *Store) ReadRevision(ctx context.Context, id string, revision int) (Revision, []byte, error) {
	r, file, err := s.OpenRevision(ctx, id, revision)
	if err != nil {
		return Revision{}, nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, MaxBinaryBytes+1))
	if err != nil {
		return Revision{}, nil, fmt.Errorf("artifacts store: read file: %w", err)
	}
	return r, data, nil
}

// AddMessage appends one message to interactionID's thread and returns it.
func (s *Store) AddMessage(ctx context.Context, interactionID, author, kind, body string, revision int) (Message, error) {
	if !safeID(interactionID) {
		return Message{}, fmt.Errorf("%w: bad item id", ErrInvalidRequest)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("artifacts store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var latest sql.NullInt64
	if err = tx.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM artifact_messages WHERE interaction_id = ?`, interactionID,
	).Scan(&latest); err != nil {
		return Message{}, fmt.Errorf("artifacts store: next seq: %w", err)
	}
	m := Message{Seq: latest.Int64 + 1, Author: author, Kind: kind, Body: body, Revision: revision, CreatedAt: s.now()}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO artifact_messages (interaction_id, seq, author, kind, body, revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		interactionID, m.Seq, m.Author, m.Kind, m.Body, m.Revision, m.CreatedAt,
	); err != nil {
		return Message{}, fmt.Errorf("artifacts store: insert message: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("artifacts store: commit message: %w", err)
	}
	return m, nil
}

// MessagesBulk returns every id's messages with seq > after, oldest first.
func (s *Store) MessagesBulk(ctx context.Context, ids []string, after int64) (map[string][]Message, error) {
	out := make(map[string][]Message, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, after)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT interaction_id, seq, author, kind, body, revision, created_at FROM artifact_messages
		WHERE interaction_id IN (` + placeholders + `) AND seq > ? ORDER BY interaction_id, seq` // #nosec G202
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("artifacts store: query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var m Message
		if err = rows.Scan(&id, &m.Seq, &m.Author, &m.Kind, &m.Body, &m.Revision, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("artifacts store: scan message: %w", err)
		}
		out[id] = append(out[id], m)
	}
	return out, rows.Err()
}

// Messages returns one artifact's messages after seq.
func (s *Store) Messages(ctx context.Context, id string, after int64) ([]Message, error) {
	all, err := s.MessagesBulk(ctx, []string{id}, after)
	if err != nil {
		return nil, err
	}
	return all[id], nil
}

// Progress is the per-artifact change counters the inbox digest folds in: the
// latest revision number and the message count.
type Progress struct {
	LatestRevision int
	Messages       int64
}

// ProgressAll returns the change counters of every artifact that has any.
func (s *Store) ProgressAll(ctx context.Context) (map[string]Progress, error) {
	out := map[string]Progress{}
	rows, err := s.db.QueryContext(ctx, `SELECT interaction_id, MAX(revision) FROM artifact_revisions GROUP BY interaction_id`)
	if err != nil {
		return nil, fmt.Errorf("artifacts store: revision progress: %w", err)
	}
	for rows.Next() {
		var id string
		var latest int
		if err = rows.Scan(&id, &latest); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("artifacts store: scan revision progress: %w", err)
		}
		p := out[id]
		p.LatestRevision = latest
		out[id] = p
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	msgRows, err := s.db.QueryContext(ctx, `SELECT interaction_id, COUNT(*) FROM artifact_messages GROUP BY interaction_id`)
	if err != nil {
		return nil, fmt.Errorf("artifacts store: message progress: %w", err)
	}
	defer func() { _ = msgRows.Close() }()
	for msgRows.Next() {
		var id string
		var n int64
		if err = msgRows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("artifacts store: scan message progress: %w", err)
		}
		p := out[id]
		p.Messages = n
		out[id] = p
	}
	return out, msgRows.Err()
}
