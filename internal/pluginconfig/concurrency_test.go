package pluginconfig

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueuedConfigOperationCancelsWithoutAcquiringGate(t *testing.T) {
	s, _, _ := testStore(t)
	register(t, s)
	if err := s.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Read(ctx, "example", s.Scopes()[0]); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel waited for active operation")
	}
	s.unlock()
	if _, err := s.Read(context.Background(), "example", s.Scopes()[0]); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentStoreRevisionCannotOverwriteCommittedValues(t *testing.T) {
	s, keys, dir := testStore(t)
	register(t, s)
	other, err := Open(context.Background(), dir, keys, s.Scopes())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	register(t, other)
	scope := s.Scopes()[0]
	before, err := other.Read(context.Background(), "example", scope)
	if err != nil {
		t.Fatal(err)
	}
	save(t, s, scope, Changes{Set: map[string]any{"channel": "first-owner"}})
	_, _, err = other.Save(context.Background(), "example", scope, before.Revision, Changes{Set: map[string]any{"channel": "stale-owner", "token": "private-fixture"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale store wrote", err)
	}
	got, _, err := s.Resolve(context.Background(), "example")
	if err != nil || got["channel"] != "first-owner" || got["token"] != "" {
		t.Fatal("stale data reached resolution", err)
	}
}
