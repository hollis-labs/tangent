package envelope

import (
	"context"
	"fmt"
	"sync"

	envelopes "github.com/hollis-labs/go-envelopes"
)

// Handler produces a Response for an envelope of a specific type. PR 2
// ships the dispatcher primitive without any registered handlers; the
// triage handler lands in PR 4 and additional kinds in PR 5.
//
// Handlers MUST NOT mutate env. They receive a context for cancellation
// and tracing; long-running handlers should honor ctx.Done().
type Handler interface {
	Handle(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error)
}

// HandlerFunc adapts an ordinary function to the Handler interface. Use
// for stateless handlers and stub registrations in tests.
type HandlerFunc func(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error)

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	return f(ctx, env)
}

// Dispatcher routes envelopes to their registered Handler and validates
// both inbound envelopes and outbound responses through the Service.
//
// One Dispatcher exists per Service. PR 3 (MCP) and PR 4 (WebSocket) both
// dispatch through the same instance so server-side handler logic never
// needs to be duplicated per transport.
type Dispatcher struct {
	svc      *Service
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewDispatcher constructs an empty Dispatcher bound to svc. Handlers are
// added with Register at startup; PR 2 leaves the map empty.
func NewDispatcher(svc *Service) *Dispatcher {
	return &Dispatcher{svc: svc, handlers: make(map[string]Handler)}
}

// Register binds a Handler to an envelope type. The type MUST already be
// registered in the underlying registry — registering for an unknown type
// is a programmer error and surfaces as ErrUnknownType immediately so the
// process fails fast at boot rather than at the first envelope.
//
// Returns ErrHandlerExists if a handler is already registered for the
// type. Replacement requires explicit Unregister; the dispatcher does not
// silently overwrite.
func (d *Dispatcher) Register(typeName string, h Handler) error {
	if h == nil {
		return fmt.Errorf("envelope: dispatch: handler is nil for %q", typeName)
	}
	if _, ok := d.svc.Lookup(typeName); !ok {
		return fmt.Errorf("envelope: dispatch: %w: %q", envelopes.ErrUnknownType, typeName)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.handlers[typeName]; exists {
		return fmt.Errorf("envelope: dispatch: %w: %q", ErrHandlerExists, typeName)
	}
	d.handlers[typeName] = h
	return nil
}

// Unregister removes the handler for typeName. Returns false if no
// handler was registered for the type. Used by plugin teardown paths in
// later PRs; PR 2 has no callers but the symmetry keeps the API minimal.
func (d *Dispatcher) Unregister(typeName string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.handlers[typeName]; !ok {
		return false
	}
	delete(d.handlers, typeName)
	return true
}

// Has reports whether a handler is registered for typeName.
func (d *Dispatcher) Has(typeName string) bool {
	d.mu.RLock()
	_, ok := d.handlers[typeName]
	d.mu.RUnlock()
	return ok
}

// Dispatch validates env, routes to the registered handler, validates the
// returned Response, and surfaces any error from any of those steps.
//
// Error contract:
//   - Inbound validation failure → wrapped envelopes.ErrSchemaValidation
//     or envelopes.ErrUnknownType from Service.Validate.
//   - No handler → ErrNoHandler.
//   - Handler error → returned verbatim (the handler's responsibility to
//     wrap meaningfully); the dispatcher does NOT validate the response
//     in that case because there is no response.
//   - Handler returns (nil, nil) → ErrNilResponse (a handler must return
//     either an error or a non-nil response).
//   - Outbound validation failure → wrapped from ValidateResponse.
//
// Note: response validation runs against env.Type, not against the
// registered handler's notion of the response — the handler does not get
// to decide which schema applies.
func (d *Dispatcher) Dispatch(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	if env == nil {
		return nil, fmt.Errorf("envelope: dispatch: nil envelope")
	}
	if err := d.svc.Validate(env); err != nil {
		return nil, err
	}
	d.mu.RLock()
	h, ok := d.handlers[env.Type]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("envelope: dispatch: %w: %q", ErrNoHandler, env.Type)
	}
	resp, err := h.Handle(ctx, env)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("envelope: dispatch: %w: %q", ErrNilResponse, env.Type)
	}
	if err := d.svc.ValidateResponse(env.Type, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
