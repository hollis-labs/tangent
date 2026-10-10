package pluginui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
)

func testGraph(t *testing.T) *Graph {
	t.Helper()
	module := Module{ID: "plugin", Bytes: []byte("export const Panel = 1;")}
	module.SHA256 = digestBytes(module.Bytes)
	runtime := Module{ID: "react", Bytes: []byte("export const version = 'test';")}
	runtime.SHA256 = digestBytes(runtime.Bytes)
	graph, err := AdmitGraph(strings.Repeat("a", 64), []Module{module, runtime}, []Import{{Specifier: "react", Artifact: "react"}}, func(modules []Module, imports []Import) error {
		// Synthetic host reviewer only; no claim to JavaScript parser conformance.
		modules[0].Bytes[0] = 'X'
		imports[0].Specifier = "changed"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}
func testStore(t *testing.T) (*Store, *Retention, string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "retained")
	identity, err := InitializeRetention(dir)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := OpenRetention(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := retention.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	store, err := NewStore("https://tangent.test", retention)
	if err != nil {
		t.Fatal(err)
	}
	return store, retention, dir, identity
}
func testLease(generation uint64, live *atomic.Bool) OwnerLease {
	return OwnerLease{Owner: driver.Owner{HostInstance: strings.Repeat("b", 64), OwnerID: "sample.panel", OwnerGeneration: generation}, Current: live.Load}
}
func seal(t *testing.T, d *Delivery) {
	t.Helper()
	err := d.SealDocument(Document{FrameID: d.scope, HTML: "<!doctype html><p>owned generated fixture</p>", CSP: "script-src 'none'", PermissionsPolicy: "camera=()"}, func(Document) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}
func pathFromURL(value string) string { return strings.TrimPrefix(value, "https://tangent.test") }

func TestGraphRequiresReviewAndDetachedExactInventory(t *testing.T) {
	graph := testGraph(t)
	modules, imports := graph.Snapshot()
	if modules[0].Bytes[0] != 'e' || imports[0].Specifier != "react" {
		t.Fatal("reviewer mutated custody")
	}
	modules[0].Bytes[0] = 'X'
	if next, _ := graph.Snapshot(); next[0].Bytes[0] != 'e' {
		t.Fatal("snapshot mutated graph")
	}
	module := Module{ID: "plugin", Bytes: []byte("export const a = 1;")}
	module.SHA256 = digestBytes(module.Bytes)
	if _, admitGraphErr := AdmitGraph(strings.Repeat("a", 64), []Module{module}, nil, nil); admitGraphErr == nil {
		t.Fatal("implicit graph review")
	}
	refuse := errors.New("computed import not reviewed")
	if _, admitGraphErr := AdmitGraph(strings.Repeat("a", 64), []Module{module}, nil, func([]Module, []Import) error { return refuse }); !errors.Is(admitGraphErr, refuse) {
		t.Fatal("review refusal lost")
	}
	if _, admitGraphErr := AdmitGraph(strings.Repeat("a", 64), []Module{module}, []Import{{"react", "missing"}}, func([]Module, []Import) error { return nil }); admitGraphErr == nil {
		t.Fatal("unadmitted mapping")
	}
	module.Bytes[0] = 'X'
	if _, admitGraphErr := AdmitGraph(strings.Repeat("a", 64), []Module{module}, nil, func([]Module, []Import) error { return nil }); admitGraphErr == nil {
		t.Fatal("hash mismatch")
	}
}

func TestSealedScopeRetainsCustodyUntilStopAndNeverReusesAfterRestart(t *testing.T) {
	store, retention, dir, identity := testStore(t)
	var live atomic.Bool
	live.Store(true)
	graph := testGraph(t)
	scope := strings.Repeat("c", 64)
	stopped := false
	delivery, err := store.Provision(scope, testLease(1, &live), graph, func(context.Context) error {
		if !stopped {
			return errors.New("frame stop uncertain")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	modulePath := pathFromURL(delivery.URLs()["plugin"])
	if _, ok := store.Read(modulePath); ok {
		t.Fatal("served before generated document admission")
	}
	seal(t, delivery)
	body, ok := store.Read(modulePath)
	if !ok {
		t.Fatal("sealed module absent")
	}
	original := bytes.Clone(body.Body)
	body.Body[0] = 'X'
	again, _ := store.Read(modulePath)
	if !bytes.Equal(again.Body, original) {
		t.Fatal("response caller mutated sealed bytes")
	}
	if _, ok := store.Read(strings.Replace(modulePath, "sample.panel", "other.owner", 1)); ok {
		t.Fatal("foreign owner alias")
	}
	if releaseErr := delivery.Release(context.Background()); releaseErr == nil {
		t.Fatal("uncertain frame stop accepted")
	}
	if _, ok := store.Read(modulePath); ok {
		t.Fatal("revoked scope served")
	}
	store.mu.Lock()
	retained := store.scopes[scope] == delivery
	store.mu.Unlock()
	if !retained {
		t.Fatal("bytes dropped before frame stop")
	}
	stopped = true
	if releaseErr := delivery.Release(context.Background()); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if _, provisionErr := store.Provision(scope, testLease(2, &live), graph, func(context.Context) error { return nil }); provisionErr == nil {
		t.Fatal("disposed scope rebound")
	}
	if closeErr := retention.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := OpenRetention(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	next, err := NewStore("https://tangent.test", reopened)
	if err != nil {
		t.Fatal(err)
	}
	if _, provisionErr := next.Provision(scope, testLease(2, &live), graph, func(context.Context) error { return nil }); provisionErr == nil {
		t.Fatal("restart rebound old URL scope")
	}
	if _, ok := next.Read(modulePath); ok {
		t.Fatal("restart served nonexistent old bytes")
	}
}
func TestMissingOrWrongRetentionFailsClosed(t *testing.T) {
	_, _, dir, identity := testStore(t)
	if _, openRetentionErr := OpenRetention(dir, strings.Repeat("d", 64)); openRetentionErr == nil {
		t.Fatal("wrong retained identity accepted")
	}
	if removeErr := os.Remove(filepath.Join(dir, retentionMarker)); removeErr != nil {
		t.Fatal(removeErr)
	}
	if _, openRetentionErr := OpenRetention(dir, identity); openRetentionErr == nil {
		t.Fatal("missing history reset")
	}
	if _, initializeRetentionErr := InitializeRetention(dir); initializeRetentionErr == nil {
		t.Fatal("existing store initialized again")
	}
}
func TestControllerExactOwnerFenceAndReplacement(t *testing.T) {
	store, _, _, _ := testStore(t)
	controller, err := NewController(store)
	if err != nil {
		t.Fatal(err)
	}
	graph := testGraph(t)
	var oldLive, newLive atomic.Bool
	oldLive.Store(true)
	newLive.Store(true)
	old := testLease(1, &oldLive)
	next := testLease(2, &newLive)
	if publishErr := controller.Publish(old, graph); publishErr != nil {
		t.Fatal(publishErr)
	}
	if publishErr := controller.Publish(next, graph); publishErr == nil {
		t.Fatal("unwithdrawn owner replaced")
	}
	stopped := false
	frame, err := controller.Provision(context.Background(), old.Owner, strings.Repeat("e", 64), func(context.Context) error { stopped = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	seal(t, frame)
	if withdrawErr := controller.Withdraw(context.Background(), old.Owner); withdrawErr != nil {
		t.Fatal(withdrawErr)
	}
	if !stopped {
		t.Fatal("frame not joined")
	}
	if publishErr := controller.Publish(next, graph); publishErr != nil {
		t.Fatal(publishErr)
	}
	if withdrawErr := controller.Withdraw(context.Background(), old.Owner); withdrawErr != nil {
		t.Fatal(withdrawErr)
	}
	nextFrame, err := controller.Provision(context.Background(), next.Owner, strings.Repeat("f", 64), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal("late old cleanup withdrew replacement", err)
	}
	seal(t, nextFrame)
	newLive.Store(false)
	if _, ok := store.Read(pathFromURL(nextFrame.DocumentURL())); ok {
		t.Fatal("ended driver served document")
	}
	if withdrawErr := controller.Withdraw(context.Background(), next.Owner); withdrawErr != nil {
		t.Fatal(withdrawErr)
	}
	if publishErr := controller.Publish(next, graph); publishErr == nil {
		t.Fatal("same driver generation reused")
	}
}

func TestWithdrawFencesPendingProvisionBeforeLateCompletion(t *testing.T) {
	store, _, _, _ := testStore(t)
	controller, err := NewController(store)
	if err != nil {
		t.Fatal(err)
	}
	graph := testGraph(t)
	var live atomic.Bool
	live.Store(true)
	lease := testLease(1, &live)
	entered := make(chan struct{})
	resume := make(chan struct{})
	var checks atomic.Int32
	lease.Current = func() bool {
		if checks.Add(1) == 2 {
			close(entered)
			<-resume
		}
		return live.Load()
	}
	if publishErr := controller.Publish(lease, graph); publishErr != nil {
		t.Fatal(publishErr)
	}
	var stopped atomic.Bool
	result := make(chan error, 1)
	go func() {
		_, err := controller.Provision(context.Background(), lease.Owner, strings.Repeat("a", 64), func(context.Context) error { stopped.Store(true); return nil })
		result <- err
	}()
	<-entered
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if withdrawErr := controller.Withdraw(canceled, lease.Owner); !errors.Is(withdrawErr, context.Canceled) {
		t.Fatal("pending withdrawal did not retain cancellation", withdrawErr)
	}
	close(resume)
	if operationErr := <-result; operationErr == nil {
		t.Fatal("late provision escaped fence")
	}
	if !stopped.Load() {
		t.Fatal("late owned frame not stopped")
	}
	if withdrawErr := controller.Withdraw(context.Background(), lease.Owner); withdrawErr != nil {
		t.Fatal(withdrawErr)
	}
}

func TestMissingReservationFileCannotRecycleDisposedScope(t *testing.T) {
	store, retention, dir, identity := testStore(t)
	var live atomic.Bool
	live.Store(true)
	scope := strings.Repeat("9", 64)
	delivery, err := store.Provision(scope, testLease(1, &live), testGraph(t), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if releaseErr := delivery.Release(context.Background()); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	// Loss of optional metadata must not erase the durable consumed-scope ledger.
	if removeErr := os.Remove(filepath.Join(dir, digestBytes([]byte(scope))+".reserved")); removeErr != nil {
		t.Fatal(removeErr)
	}
	if closeErr := retention.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopened, err := OpenRetention(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	next, err := NewStore("https://tangent.test", reopened)
	if err != nil {
		t.Fatal(err)
	}
	if _, provisionErr := next.Provision(scope, testLease(2, &live), testGraph(t), func(context.Context) error { return nil }); provisionErr == nil {
		t.Fatal("disposed scope reused after metadata loss")
	}
}

func TestMissingOrIncompleteConsumedJournalRefusesReopen(t *testing.T) {
	for _, damage := range []string{"missing", "partial"} {
		t.Run(damage, func(t *testing.T) {
			_, retention, dir, identity := testStore(t)
			if closeErr := retention.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			path := filepath.Join(dir, reservationHistory)
			if damage == "missing" {
				if removeErr := os.Remove(path); removeErr != nil {
					t.Fatal(removeErr)
				}
			} else {
				if writeFileErr := os.WriteFile(path, []byte("truncated uncertain reservation"), 0600); writeFileErr != nil {
					t.Fatal(writeFileErr)
				}
			}
			if _, openRetentionErr := OpenRetention(dir, identity); openRetentionErr == nil {
				t.Fatal("damaged consumed journal silently reset")
			}
		})
	}
}
