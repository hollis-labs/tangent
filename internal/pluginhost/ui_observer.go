package pluginhost

import (
	"context"
	"errors"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/tangent/internal/pluginui"
)

// UIObserver is an optional host-composition port. It observes the real driver
// incarnation, never a manifest/native-tools tuple. Activated may stage custody;
// current remains false until BOTH driver activation and host registration are
// complete. There is no observer configured by the installed loader yet.
type UIObserver interface {
	Activated(context.Context, driver.Owner, func() bool) error
	Revoked(context.Context, driver.Owner) error
}

func WithUIObserver(observer UIObserver) ChildPluginOption {
	return func(p *ChildPlugin) { p.uiObserver = observer }
}
func (p *ChildPlugin) uiCurrent(owner driver.Owner) bool {
	p.mu.Lock()
	current, active, lifecycle, ready := p.owner, p.active, p.lifecycle, p.uiReady
	p.mu.Unlock()
	return current == owner && active != nil && active.Err() == nil && lifecycle != nil && lifecycle.IsCurrent(owner) && ready != nil && ready()
}

// NewUIObserver binds an already reviewed graph to host lifecycle observation.
// It does not discover installed UI, mount HTTP routes or select a renderer.
func NewUIObserver(controller *pluginui.Controller, graph *pluginui.Graph) (UIObserver, error) {
	if controller == nil || graph == nil {
		return nil, errors.New("pluginhost: missing reviewed UI custody")
	}
	return &uiControllerObserver{controller: controller, graph: graph}, nil
}

type uiControllerObserver struct {
	controller *pluginui.Controller
	graph      *pluginui.Graph
}

func (o *uiControllerObserver) Activated(_ context.Context, owner driver.Owner, current func() bool) error {
	return o.controller.Publish(pluginui.OwnerLease{Owner: owner, Current: current}, o.graph)
}
func (o *uiControllerObserver) Revoked(ctx context.Context, owner driver.Owner) error {
	return o.controller.Withdraw(ctx, owner)
}
