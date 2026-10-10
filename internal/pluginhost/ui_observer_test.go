package pluginhost

import (
	"context"
	"errors"
	"sync"
	"testing"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
)

type observerReceipt struct {
	owner             driver.Owner
	current           func() bool
	readyAtActivation bool
	revoked           bool
	readyAtRevocation bool
}
type fixtureUIObserver struct {
	mu       sync.Mutex
	receipts []*observerReceipt
	failure  error
}

func (o *fixtureUIObserver) Activated(_ context.Context, owner driver.Owner, current func() bool) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.receipts = append(o.receipts, &observerReceipt{owner: owner, current: current, readyAtActivation: current()})
	return o.failure
}
func (o *fixtureUIObserver) Revoked(_ context.Context, owner driver.Owner) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.receipts {
		if r.owner == owner {
			r.readyAtRevocation = r.current()
			r.revoked = true
		}
	}
	return nil
}
func TestUIObserverBindsRealDriverReadinessAndFencesFailedActivation(t *testing.T) {
	binary := buildEchoPlugin(t)
	host, _ := newHost(t)
	observer := &fixtureUIObserver{}
	child := NewChildPlugin(echoSpec(t, binary), nil, nil, WithUIObserver(observer))
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.UnloadAll() })
	first := observer.receipts[0]
	if first.readyAtActivation || !first.current() {
		t.Fatal("observer confused staging with fully ready load")
	}
	actual := echoedIncarnation(t, child)
	if first.owner.HostInstance != actual.HostInstance || first.owner.OwnerID != actual.OwnerID || first.owner.OwnerGeneration != actual.OwnerGeneration {
		t.Fatal("observer tuple is not actual wire incarnation")
	}
	if err := host.Unload(child.ID()); err != nil {
		t.Fatal(err)
	}
	if !first.revoked || first.readyAtRevocation || first.current() {
		t.Fatal("UI observer not fenced before cleanup")
	}
	next := NewChildPlugin(echoSpec(t, binary), nil, nil, WithUIObserver(observer))
	if err := host.Load(next); err != nil {
		t.Fatal(err)
	}
	second := observer.receipts[1]
	if first.current() || !second.current() || second.owner.OwnerGeneration <= first.owner.OwnerGeneration {
		t.Fatal("old UI lease rebound to replacement")
	}
	if err := host.Unload(next.ID()); err != nil {
		t.Fatal(err)
	}
	observer.failure = errors.New("synthetic UI custody refusal")
	refused := NewChildPlugin(echoSpec(t, binary), nil, nil, WithUIObserver(observer))
	if err := host.Load(refused); !errors.Is(err, observer.failure) {
		t.Fatal("UI admission refusal did not fail load", err)
	}
	third := observer.receipts[2]
	if !third.revoked || third.current() || refused.pid() != 0 {
		t.Fatal("failed activation retained UI authority or child")
	}
}
