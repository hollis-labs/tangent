package pluginconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/google/uuid"
	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	_ "modernc.org/sqlite" // Register the private SQLite config database driver.
)

type Store struct {
	db      *sql.DB
	secrets Secrets
	scopes  []Scope
	gate    chan struct{}
	schemas map[string]Schema
}
type row struct {
	Key, Value, SecretRef string
	Scope                 Scope
}

// Open creates a private settings database. It neither reads credentials nor
// contacts the keychain. Scope IDs are supplied by host composition, not a plugin.
func Open(ctx context.Context, dir string, secrets Secrets, scopes []Scope) (*Store, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !filepath.IsAbs(dir) {
		return nil, ErrRefused
	}
	if secrets == nil || len(scopes) == 0 || len(scopes) > 3 {
		return nil, ErrRefused
	}
	rank := map[string]int{"client": 0, "environment": 1, "project": 2}
	prev := -1
	for _, scope := range scopes {
		if !scope.valid() || rank[scope.Kind] <= prev {
			return nil, ErrRefused
		}
		prev = rank[scope.Kind]
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("plugin config directory unavailable")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, ErrRefused
	}
	path, err := prepareDatabaseFile(dir)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, ErrRefused
	}
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS plugin_config_revision(plugin TEXT PRIMARY KEY, revision TEXT NOT NULL, applied TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS plugin_config_override(plugin TEXT NOT NULL, kind TEXT NOT NULL, scope_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, secret_ref TEXT NOT NULL, PRIMARY KEY(plugin,kind,scope_id,key));`)
	if err != nil {
		_ = db.Close()
		return nil, ErrRefused
	}
	return &Store{db: db, secrets: secrets, scopes: slices.Clone(scopes), schemas: map[string]Schema{}, gate: make(chan struct{}, 1)}, nil
}
func (s *Store) Close() error    { return s.db.Close() }
func (s *Store) Scopes() []Scope { return slices.Clone(s.scopes) }
func (s *Store) Register(ctx context.Context, id string, config sdk.Config) error {
	if !validID(id) {
		return ErrRefused
	}
	schema, err := review(config)
	if err != nil {
		return err
	}
	if err = s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if _, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO plugin_config_revision(plugin,revision) VALUES(?,?)", id, uuid.NewString()); err != nil {
		return ErrRefused
	}
	s.schemas[id] = schema
	return nil
}
func (s *Store) Schema(ctx context.Context, id string) (Schema, error) {
	if err := s.lock(ctx); err != nil {
		return Schema{}, err
	}
	defer s.unlock()
	schema, ok := s.schemas[id]
	if !ok {
		return Schema{}, ErrRefused
	}
	// Never lend the mutable registry to a transport or plugin.
	detached, err := review(sdk.Config{Fields: schema.Fields, Secrets: schema.Secrets})
	return detached, err
}
func (s *Store) PluginIDs(ctx context.Context) ([]string, error) {
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	ids := make([]string, 0, len(s.schemas))
	for id := range s.schemas {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
func (s *Store) scopeIndex(scope Scope) int { return slices.Index(s.scopes, scope) }
func (s *Store) revision(ctx context.Context, id string) (string, string, error) {
	var revision, applied string
	err := s.db.QueryRowContext(ctx, "SELECT revision,applied FROM plugin_config_revision WHERE plugin=?", id).Scan(&revision, &applied)
	if err != nil {
		return "", "", ErrRefused
	}
	return revision, applied, nil
}
func (s *Store) rows(ctx context.Context, id string) (collected []row, failure error) {
	result, err := s.db.QueryContext(ctx, "SELECT kind,scope_id,key,value,secret_ref FROM plugin_config_override WHERE plugin=?", id)
	if err != nil {
		return nil, ErrRefused
	}
	defer func() { failure = errors.Join(failure, result.Close()) }()
	var rows []row
	for result.Next() {
		var r row
		if err = result.Scan(&r.Scope.Kind, &r.Scope.ID, &r.Key, &r.Value, &r.SecretRef); err != nil {
			return nil, ErrRefused
		}
		rows = append(rows, r)
	}
	if result.Err() != nil {
		return nil, ErrRefused
	}
	return rows, nil
}
func (s *Store) effective(schema Schema, rows []row, upto int) map[string]row {
	selected := map[string]row{}
	for key, f := range schema.Fields {
		if f.Default != "" {
			selected[key] = row{Key: key, Value: f.Default, Scope: Scope{Kind: "default"}}
		}
	}
	for _, scope := range s.scopes[:upto+1] {
		for _, r := range rows {
			if r.Scope == scope {
				if _, field := schema.Fields[r.Key]; field {
					if r.SecretRef == "" {
						selected[r.Key] = r
					}
				} else if _, secret := schema.Secrets[r.Key]; secret && r.SecretRef != "" {
					selected[r.Key] = r
				}
			}
		}
	}
	return selected
}
func (s *Store) read(ctx context.Context, id string, scope Scope) (Snapshot, error) {
	schema, ok := s.schemas[id]
	index := s.scopeIndex(scope)
	if !ok || index < 0 {
		return Snapshot{}, ErrRefused
	}
	revision, applied, err := s.revision(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.rows(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	values := map[string]Value{}
	selected := s.effective(schema, rows, index)
	for key := range schema.Fields {
		r, present := selected[key]
		v := Value{Present: present, Editable: true, HasOverride: present && r.Scope == scope}
		if present {
			v.Value, err = parseScalar(schema.Fields[key], r.Value)
			if err != nil {
				return Snapshot{}, ErrRefused
			}
			v.Source = r.Scope.Kind
		}
		values[key] = v
	}
	for key := range schema.Secrets {
		r, present := selected[key]
		v := Value{Present: present, SecretPresent: &present, Editable: true, HasOverride: present && r.Scope == scope}
		if present {
			v.Source = r.Scope.Kind
		}
		values[key] = v
	}
	token := schema.Digest + ":" + revision
	return Snapshot{PluginID: id, Scope: scope, Revision: token, SchemaDigest: schema.Digest, Values: values, PendingRestart: applied != token}, nil
}
func (s *Store) Read(ctx context.Context, id string, scope Scope) (Snapshot, error) {
	if err := s.lock(ctx); err != nil {
		return Snapshot{}, err
	}
	defer s.unlock()
	return s.read(ctx, id, scope)
}

func (s *Store) validate(schema Schema, rows []row, scope Scope, changes Changes) (Validation, map[string]row) {
	errs := []FieldError{}
	updates := map[string]row{}
	seen := map[string]bool{}
	if len(changes.Set)+len(changes.Unset) > 256 {
		return Validation{Errors: []FieldError{fieldError("config", "too_large")}}, nil
	}
	for key, v := range changes.Set {
		seen[key] = true
		r := row{Key: key, Scope: scope}
		if f, known := schema.Fields[key]; known {
			text, err := encodeScalar(f, v)
			if err != nil {
				errs = append(errs, fieldError(key, "invalid"))
				continue
			}
			r.Value = text
		} else if _, known := schema.Secrets[key]; known {
			text, ok := v.(string)
			if !ok || len(text) == 0 || len(text) > 2048 {
				errs = append(errs, fieldError(key, "invalid"))
				continue
			}
			r.SecretRef = "staged"
			r.Value = text
		} else {
			errs = append(errs, fieldError(key, "undeclared"))
			continue
		}
		updates[key] = r
	}
	for _, key := range changes.Unset {
		if seen[key] {
			errs = append(errs, fieldError(key, "duplicated"))
			continue
		}
		seen[key] = true
		if _, f := schema.Fields[key]; !f {
			if _, secret := schema.Secrets[key]; !secret {
				errs = append(errs, fieldError(key, "undeclared"))
				continue
			}
		}
		updates[key] = row{Key: key, Scope: scope}
	}
	candidate := make([]row, 0, len(rows)+len(updates))
	for _, r := range rows {
		if r.Scope == scope && seen[r.Key] {
			continue
		}
		candidate = append(candidate, r)
	}
	for _, r := range updates {
		if r.Value != "" || r.SecretRef != "" {
			candidate = append(candidate, r)
		} else if _, set := changes.Set[r.Key]; set {
			candidate = append(candidate, r)
		}
	}
	effective := s.effective(schema, candidate, len(s.scopes)-1)
	for key, f := range schema.Fields {
		if _, present := effective[key]; f.Required && !present {
			errs = append(errs, fieldError(key, "required"))
		}
	}
	for key, f := range schema.Secrets {
		if _, present := effective[key]; f.Required && !present {
			errs = append(errs, fieldError(key, "required"))
		}
	}
	slices.SortFunc(errs, func(a, b FieldError) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	return Validation{Valid: len(errs) == 0, Errors: errs}, updates
}
func (s *Store) Validate(ctx context.Context, id string, scope Scope, revision string, changes Changes) (Validation, error) {
	if err := s.lock(ctx); err != nil {
		return Validation{}, err
	}
	defer s.unlock()
	snapshot, err := s.read(ctx, id, scope)
	if err != nil {
		return Validation{}, err
	}
	if snapshot.Revision != revision {
		return Validation{}, ErrConflict
	}
	rows, err := s.rows(ctx, id)
	if err != nil {
		return Validation{}, err
	}
	v, _ := s.validate(s.schemas[id], rows, scope, changes)
	return v, nil
}
func (s *Store) Save(ctx context.Context, id string, scope Scope, revision string, changes Changes) (Snapshot, Validation, error) {
	if err := s.lock(ctx); err != nil {
		return Snapshot{}, Validation{}, err
	}
	defer s.unlock()
	snapshot, err := s.read(ctx, id, scope)
	if err != nil {
		return Snapshot{}, Validation{}, err
	}
	if snapshot.Revision != revision {
		return Snapshot{}, Validation{}, ErrConflict
	}
	rows, err := s.rows(ctx, id)
	if err != nil {
		return Snapshot{}, Validation{}, err
	}
	validation, updates := s.validate(s.schemas[id], rows, scope, changes)
	if !validation.Valid || len(updates) == 0 {
		return snapshot, validation, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, Validation{}, ErrRefused
	}
	defer func() { _ = tx.Rollback() }() // A committed transaction returns ErrTxDone.
	// A second process may have changed the database since Read.
	revisionPart := snapshot.Revision[len(snapshot.SchemaDigest)+1:]
	next := uuid.NewString()
	result, err := tx.ExecContext(ctx, "UPDATE plugin_config_revision SET revision=? WHERE plugin=? AND revision=?", next, id, revisionPart)
	if err != nil {
		return Snapshot{}, Validation{}, ErrRefused
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return Snapshot{}, Validation{}, ErrConflict
	}
	var staged []string
	rollbackSecrets := func() {
		for _, account := range staged {
			_ = s.secrets.Delete(context.WithoutCancel(ctx), account)
		}
	}
	for key, r := range updates {
		if _, unset := changes.Set[key]; !unset {
			_, err = tx.ExecContext(ctx, "DELETE FROM plugin_config_override WHERE plugin=? AND kind=? AND scope_id=? AND key=?", id, scope.Kind, scope.ID, key)
		} else {
			if r.SecretRef != "" {
				account := uuid.NewString()
				staged = append(staged, account)
				if err = s.secrets.Set(ctx, account, r.Value); err != nil {
					rollbackSecrets()
					return Snapshot{}, Validation{}, ErrSecret
				}
				r.SecretRef = account
				r.Value = ""
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO plugin_config_override(plugin,kind,scope_id,key,value,secret_ref) VALUES(?,?,?,?,?,?) ON CONFLICT(plugin,kind,scope_id,key) DO UPDATE SET value=excluded.value,secret_ref=excluded.secret_ref`, id, scope.Kind, scope.ID, key, r.Value, r.SecretRef)
		}
		if err != nil {
			rollbackSecrets()
			return Snapshot{}, Validation{}, ErrRefused
		}
	}
	if err = tx.Commit(); err != nil {
		rollbackSecrets()
		return Snapshot{}, Validation{}, ErrRefused
	}
	// Superseded references are no longer reachable by new resolutions. Existing
	// incarnations already own detached Init strings. Failed cleanup is retained,
	// never a reason to pretend the committed update did not happen.
	for _, r := range rows {
		if r.Scope == scope && r.SecretRef != "" {
			if _, changed := updates[r.Key]; changed {
				_ = s.secrets.Delete(context.WithoutCancel(ctx), r.SecretRef)
			}
		}
	}
	updated, err := s.read(ctx, id, scope)
	return updated, validation, err
}

// Resolve snapshots exactly the host-configured scope chain before each spawn.
// It reads only declared secrets and never consults ambient environment variables.
func (s *Store) Resolve(ctx context.Context, id string) (map[string]string, string, error) {
	runtime, err := s.Runtime(ctx, id, "")
	return runtime.Values, runtime.Revision, err
}

// Runtime is an incarnation-owned handoff. All fields are excluded from JSON;
// no browser endpoint or status projection may serialize this value.
type Runtime struct {
	Values   map[string]string `json:"-"`
	Secrets  []string          `json:"-"`
	Revision string            `json:"-"`
}

func (s *Store) Runtime(ctx context.Context, id, expected string) (Runtime, error) {
	if err := s.lock(ctx); err != nil {
		return Runtime{}, err
	}
	defer s.unlock()
	values, revision, err := s.resolve(ctx, id, expected)
	if err != nil {
		return Runtime{}, err
	}
	secrets := []string{}
	for key := range s.schemas[id].Secrets {
		if value := values[key]; value != "" {
			secrets = append(secrets, value)
		}
	}
	return Runtime{Values: values, Secrets: secrets, Revision: revision}, nil
}
func (s *Store) resolve(ctx context.Context, id, expected string) (map[string]string, string, error) {
	schema, ok := s.schemas[id]
	if !ok {
		return nil, "", ErrRefused
	}
	snapshot, err := s.read(ctx, id, s.scopes[len(s.scopes)-1])
	if err != nil {
		return nil, "", err
	}
	if expected != "" && snapshot.Revision != expected {
		return nil, "", ErrConflict
	}
	rows, err := s.rows(ctx, id)
	if err != nil {
		return nil, "", err
	}
	effective := s.effective(schema, rows, len(s.scopes)-1)
	config := map[string]string{}
	for key, f := range schema.Fields {
		r, present := effective[key]
		if !present {
			if f.Required {
				return nil, "", ErrRefused
			}
			continue
		}
		if _, err = parseScalar(f, r.Value); err != nil {
			return nil, "", ErrRefused
		}
		config[key] = r.Value
	}
	for key, f := range schema.Secrets {
		r, present := effective[key]
		if !present {
			if f.Required {
				return nil, "", ErrRefused
			}
			continue
		}
		value, getErr := s.secrets.Get(ctx, r.SecretRef)
		if getErr != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			return nil, "", ErrSecret
		}
		config[key] = value
	}
	return config, snapshot.Revision, nil
}
func (s *Store) MarkApplied(ctx context.Context, id, revision string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	schema, ok := s.schemas[id]
	if !ok {
		return ErrRefused
	}
	current, _, err := s.revision(ctx, id)
	if err != nil {
		return err
	}
	if schema.Digest+":"+current != revision {
		return ErrConflict
	}
	result, err := s.db.ExecContext(ctx, "UPDATE plugin_config_revision SET applied=? WHERE plugin=? AND revision=?", revision, id, current)
	if err != nil {
		return ErrRefused
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Store) unlock() { <-s.gate }

func prepareDatabaseFile(dir string) (path string, failure error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", ErrRefused
	}
	defer func() { failure = errors.Join(failure, root.Close()) }()
	file, err := root.OpenFile("config.sqlite", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		if err = file.Close(); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", ErrRefused
	}
	info, err := root.Lstat("config.sqlite")
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", ErrRefused
	}
	return filepath.Join(dir, "config.sqlite"), nil
}

// ChangeString validates a scoped SDK SetConfig call against the reviewed
// declaration and saves with the current revision. It does not change the
// running incarnation's snapshot or restart the caller inside its callback.
func (s *Store) ChangeString(ctx context.Context, id, key, value string) error {
	schema, err := s.Schema(ctx, id)
	if err != nil {
		return err
	}
	var scalar any
	if field, ok := schema.Fields[key]; ok {
		scalar, err = parseScalar(field, value)
		if err != nil {
			return ErrRefused
		}
	} else if _, ok := schema.Secrets[key]; ok {
		scalar = value
	} else {
		return ErrRefused
	}
	scope := s.scopes[0]
	snap, err := s.Read(ctx, id, scope)
	if err != nil {
		return err
	}
	_, validation, err := s.Save(ctx, id, scope, snap.Revision, Changes{Set: map[string]any{key: scalar}})
	if err != nil {
		return err
	}
	if !validation.Valid {
		return ErrRefused
	}
	return nil
}
