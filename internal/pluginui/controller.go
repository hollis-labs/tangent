package pluginui

import (
	"context"
	"errors"
	"sync"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
)

// Controller is the host publish/withdraw port, not an HTTP registration API.
// Its graph snapshots and exact driver leases stay private to the composition.
type Controller struct {
	mu     sync.Mutex
	store  *Store
	owners map[driver.Owner]*publication
}
type publication struct {
	graph   *Graph
	lease   OwnerLease
	fenced  bool
	frames  map[*Delivery]struct{}
	pending int
	drained chan struct{}
}

func NewController(store *Store) (*Controller, error) {
	if store == nil {
		return nil, errors.New("pluginui: missing response store")
	}
	return &Controller{store: store, owners: make(map[driver.Owner]*publication)}, nil
}

// Publish may precede final host load readiness. Provision still requires the
// actual current-driver predicate; publishing never declares a child ready.
func (c *Controller) Publish(lease OwnerLease, graph *Graph) error {
	if !validOwner(lease.Owner) || lease.Current == nil || graph == nil {
		return errors.New("pluginui: missing driver graph binding")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.owners[lease.Owner]; exists {
		return errors.New("pluginui: driver owner already used")
	}
	for owner, p := range c.owners {
		if owner.OwnerID == lease.Owner.OwnerID && !p.fenced {
			return errors.New("pluginui: previous owner not withdrawn")
		}
	}
	c.owners[lease.Owner] = &publication{graph: graph, lease: lease, frames: make(map[*Delivery]struct{})}
	return nil
}
func (c *Controller) Provision(ctx context.Context, owner driver.Owner, scope string, stop func(context.Context) error) (*Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	p := c.owners[owner]
	if p == nil || p.fenced || !p.lease.Current() {
		c.mu.Unlock()
		return nil, errors.New("pluginui: stale driver owner")
	}
	if p.pending == 0 {
		p.drained = make(chan struct{})
	}
	p.pending++
	defer func() {
		c.mu.Lock()
		p.pending--
		if p.pending == 0 {
			close(p.drained)
		}
		c.mu.Unlock()
	}()
	graph, lease := p.graph, p.lease
	c.mu.Unlock()
	delivery, err := c.store.Provision(scope, lease, graph, stop)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	p.frames[delivery] = struct{}{}
	if p.fenced || c.owners[owner] != p || !p.lease.Current() || ctx.Err() != nil {
		c.mu.Unlock()
		delivery.Revoke()
		return nil, errors.Join(errors.New("pluginui: owner ended during frame provision"), delivery.Release(ctx))
	}
	c.mu.Unlock()
	return delivery, nil
}

// Withdraw fences the exact owner before joining each owned frame. A failed
// join retains bytes and can be retried; it cannot withdraw a replacement tuple.
func (c *Controller) Withdraw(ctx context.Context, owner driver.Owner) error {
	c.mu.Lock()
	p := c.owners[owner]
	if p == nil {
		c.mu.Unlock()
		return nil
	}
	p.fenced = true
	// Fence existing frames immediately, then join outstanding provisions before
	// collecting their resources. A late provision cannot escape this withdrawal.
	for frame := range p.frames {
		frame.Revoke()
	}
	drained := p.drained
	pending := p.pending > 0
	c.mu.Unlock()
	if pending {
		select {
		case <-drained:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	frames := make([]*Delivery, 0, len(p.frames))
	for frame := range p.frames {
		frame.Revoke()
		frames = append(frames, frame)
	}
	c.mu.Unlock()
	var err error
	for _, frame := range frames {
		stopErr := frame.Release(ctx)
		err = errors.Join(err, stopErr)
		if stopErr == nil {
			c.mu.Lock()
			delete(p.frames, frame)
			c.mu.Unlock()
		}
	}
	c.mu.Lock()
	if len(p.frames) == 0 {
		p.graph = nil
	}
	c.mu.Unlock()
	return err
}
