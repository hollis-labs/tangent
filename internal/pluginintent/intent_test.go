package pluginintent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIntentPreservesSiblingDecisionsAndRefusesMalformedOrCanceledWrite(t *testing.T) {
	path := Path(t.TempDir())
	if _, err := Set(context.Background(), path, "existing.enabled", true); err != nil {
		t.Fatal(err)
	}
	if _, err := Set(context.Background(), path, "existing.disabled", false); err != nil {
		t.Fatal(err)
	}
	intent, err := Set(context.Background(), path, "new.plugin", true)
	if err != nil || !intent["existing.enabled"] || intent["existing.disabled"] || !intent["new.plugin"] {
		t.Fatal("sibling decisions changed", intent, err)
	}
	if intent["unknown.plugin"] {
		t.Fatal("unknown enabled")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("intent permissions", err)
	}
	before, err := os.ReadFile(path) // #nosec G304 -- fixed boolean intent or lock beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Set(ctx, path, "new.plugin", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path) // #nosec G304 -- fixed boolean intent or lock beneath t.TempDir.
	if err != nil || string(before) != string(after) {
		t.Fatal("canceled write changed intent", err)
	}
	if err = os.WriteFile(path, []byte(`{"existing.enabled":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Set(context.Background(), path, "new.plugin", false); err == nil {
		t.Fatal("malformed intent overwritten")
	}
	unchanged, err := os.ReadFile(path) // #nosec G304 -- fixed boolean intent or lock beneath t.TempDir.
	if err != nil || string(unchanged) != `{"existing.enabled":"invalid"}` {
		t.Fatal("existing malformed source lost", err)
	}
}

func TestIntentCompetingWriterRefusesThenReadsLatestSnapshot(t *testing.T) {
	path := Path(t.TempDir())
	if _, err := Set(context.Background(), path, "one", true); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600) // #nosec G304 -- fixed boolean intent or lock beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err = tryLock(lock); err != nil {
		t.Fatal(err)
	}
	if _, err = Set(context.Background(), path, "two", false); !errors.Is(err, ErrBusy) {
		t.Fatal("competing writer not refused", err)
	}
	if err = unlock(lock); err != nil {
		t.Fatal(err)
	}
	intent, err := Set(context.Background(), path, "two", false)
	if err != nil || !intent["one"] {
		t.Fatal("latest sibling lost", intent, err)
	}
	if _, ok := intent["two"]; !ok {
		t.Fatal("explicit false not recorded")
	}
	if _, err = Read(filepath.Join(t.TempDir(), "absent.json")); err != nil {
		t.Fatal(err)
	}
}
