package pluginconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
	fail   bool
}

func (m *memorySecrets) Get(ctx context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	v, ok := m.values[key]
	if !ok {
		return "", errors.New("synthetic missing")
	}
	return v, nil
}
func (m *memorySecrets) Set(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.fail {
		return errors.New("do not expose secret synthetic-password")
	}
	m.values[key] = value
	return nil
}
func (m *memorySecrets) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}
func testStore(t *testing.T) (*Store, *memorySecrets, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	keys := &memorySecrets{values: map[string]string{}}
	store, err := Open(context.Background(), dir, keys, []Scope{{"client", "owner"}, {"environment", "dev"}, {"project", "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, keys, dir
}
func register(t *testing.T, s *Store) {
	t.Helper()
	err := s.Register(context.Background(), "example", sdk.Config{Fields: map[string]sdk.Field{"channel": {Type: "string", Default: "base"}, "enabled": {Type: "boolean", Default: "false"}, "limit": {Type: "integer", Default: "0"}, "choice": {Type: "select", Options: []string{"a", "b"}, Default: "a"}}, Secrets: map[string]sdk.Secret{"token": {}}})
	if err != nil {
		t.Fatal(err)
	}
}
func save(t *testing.T, s *Store, scope Scope, changes Changes) Snapshot {
	t.Helper()
	snap, err := s.Read(context.Background(), "example", scope)
	if err != nil {
		t.Fatal(err)
	}
	next, v, err := s.Save(context.Background(), "example", scope, snap.Revision, changes)
	if err != nil || !v.Valid {
		t.Fatalf("save: %v %+v", err, v)
	}
	return next
}
func TestScopedRoundTripAndSecretCustody(t *testing.T) {
	s, _, dir := testStore(t)
	register(t, s)
	ctx := context.Background()
	scopes := s.Scopes()
	client := save(t, s, scopes[0], Changes{Set: map[string]any{"channel": "client", "token": "synthetic-password", "enabled": false, "limit": json.Number("0")}})
	save(t, s, scopes[1], Changes{Set: map[string]any{"channel": "environment"}})
	save(t, s, scopes[2], Changes{Set: map[string]any{"channel": "project"}})
	resolved, revision, err := s.Resolve(ctx, "example")
	if err != nil || resolved["channel"] != "project" || resolved["token"] != "synthetic-password" || resolved["limit"] != "0" || resolved["enabled"] != "false" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	if err = s.MarkApplied(ctx, "example", revision); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Read(ctx, "example", scopes[2])
	if err != nil || snap.PendingRestart {
		t.Fatalf("applied: %+v %v", snap, err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("synthetic-password")) {
		t.Fatal("secret reached browser snapshot")
	}
	if snap.Values["token"].Value != nil || snap.Values["token"].SecretPresent == nil || !*snap.Values["token"].SecretPresent {
		t.Fatal("secret presence boundary")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	db, err := root.ReadFile("config.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(db, []byte("synthetic-password")) {
		t.Fatal("secret written to database")
	}
	// A stale client write cannot overwrite updates from another scope.
	_, _, err = s.Save(ctx, "example", scopes[0], client.Revision, Changes{Set: map[string]any{"channel": "lost"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS: %v", err)
	}
	save(t, s, scopes[2], Changes{Unset: []string{"channel"}})
	resolved, _, err = s.Resolve(ctx, "example")
	if err != nil || resolved["channel"] != "environment" {
		t.Fatalf("fallback: %+v %v", resolved, err)
	}
	resolved["channel"] = "caller mutation"
	fresh, _, err := s.Resolve(ctx, "example")
	if err != nil || fresh["channel"] != "environment" {
		t.Fatal("caller owns registry map")
	}
}
func TestValidationAndKeychainFailureDoNotCommit(t *testing.T) {
	s, keys, _ := testStore(t)
	register(t, s)
	ctx := context.Background()
	scope := s.Scopes()[0]
	before, err := s.Read(ctx, "example", scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, changes := range []Changes{{Set: map[string]any{"limit": json.Number("9007199254740992")}}, {Set: map[string]any{"limit": json.Number("1.1")}}, {Set: map[string]any{"enabled": "true"}}, {Set: map[string]any{"choice": "other"}}, {Set: map[string]any{"unknown": "value"}}, {Set: map[string]any{"token": ""}}, {Set: map[string]any{"channel": "x"}, Unset: []string{"channel"}}} {
		_, v, saveErr := s.Save(ctx, "example", scope, before.Revision, changes)
		if saveErr != nil || v.Valid {
			t.Fatalf("validation accepted %+v: %+v %v", changes, v, saveErr)
		}
	}
	keys.fail = true
	_, _, err = s.Save(ctx, "example", scope, before.Revision, Changes{Set: map[string]any{"channel": "new", "token": "synthetic-password"}})
	if !errors.Is(err, ErrSecret) || strings.Contains(err.Error(), "synthetic-password") {
		t.Fatalf("keychain error boundary: %v", err)
	}
	after, err := s.Read(ctx, "example", scope)
	if err != nil || after.Revision != before.Revision || after.Values["channel"].Value != "base" {
		t.Fatalf("partial commit: %+v %v", after, err)
	}
}
func TestSchemaSnapshotAndRestartRevision(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()
	config := sdk.Config{Fields: map[string]sdk.Field{"choice": {Type: "select", Default: "a", Options: []string{"a", "b"}}}}
	if err := s.Register(ctx, "example", config); err != nil {
		t.Fatal(err)
	}
	config.Fields["choice"] = sdk.Field{Type: "string", Default: "evil"}
	values, rev, err := s.Resolve(ctx, "example")
	if err != nil || values["choice"] != "a" {
		t.Fatalf("schema snapshot: %v %v", values, err)
	}
	schema, err := s.Schema(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	schema.Fields["choice"].Options[0] = "evil"
	values, _, err = s.Resolve(ctx, "example")
	if err != nil || values["choice"] != "a" {
		t.Fatal("schema projection mutated registry")
	}
	save(t, s, s.Scopes()[0], Changes{Set: map[string]any{"choice": "b"}})
	if err = s.MarkApplied(ctx, "example", rev); !errors.Is(err, ErrConflict) {
		t.Fatalf("old incarnation activated new revision: %v", err)
	}
}
func TestReopenPreservesReferencesAndRequiredUnsetRefuses(t *testing.T) {
	s, keys, dir := testStore(t)
	ctx := context.Background()
	config := sdk.Config{Fields: map[string]sdk.Field{"channel": {Type: "string", Required: true}}, Secrets: map[string]sdk.Secret{"token": {Required: true}}}
	if err := s.Register(ctx, "example", config); err != nil {
		t.Fatal(err)
	}
	scope := s.Scopes()[0]
	save(t, s, scope, Changes{Set: map[string]any{"channel": "ready", "token": "synthetic-password"}})
	second, err := Open(ctx, dir, keys, s.Scopes())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.Register(ctx, "example", config); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := second.Resolve(ctx, "example")
	if err != nil || resolved["token"] != "synthetic-password" {
		t.Fatalf("reopen: %+v %v", resolved, err)
	}
	snap, err := second.Read(ctx, "example", scope)
	if err != nil {
		t.Fatal(err)
	}
	_, v, err := second.Save(ctx, "example", scope, snap.Revision, Changes{Unset: []string{"token"}})
	if err != nil || v.Valid {
		t.Fatalf("required reset accepted: %+v %v", v, err)
	}
}
func TestUnknownScopeAndCancelledContextRefuse(t *testing.T) {
	s, _, _ := testStore(t)
	register(t, s)
	if _, err := s.Read(context.Background(), "example", Scope{"project", "other"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("unknown scope %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Resolve(ctx, "example"); err == nil {
		t.Fatal("cancelled resolve succeeded")
	}
}
