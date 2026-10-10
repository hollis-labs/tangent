package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

// Dispatch guards bound callers and contain panics. Owned registrations trip
// their owner's circuit when the host budget expires: new admission is fenced
// immediately and the subprocess is torn down through the shared driver. A
// compiled-in test adapter's uncooperative goroutine cannot be forcibly stopped;
// quarantine prevents additional work. Caller cancellation is not a defect.

// dispatchBudget bounds one ordinary call. shutdownRelease is not a budget at
// all — it is the host's own done channel, closed by UnloadAll, and it releases
// every in-flight dispatch at once. Without it a shutdown arriving one second
// into a dispatch would still wait out the remaining budget, which is exactly
// the "must not block shutdown" clause failing quietly.

// dispatchBudget bounds one plugin dispatch, tool call or route. It is the
// host's default and the only value production uses; Host carries it as a field
// so a test can shrink it to something a test can wait for.
//
// Generous rather than tight: the shipped plugins call another application's
// HTTP API and then Tangent's own tool surface, and a board with a hundred
// cards is a real call that takes real time. This is a defect ceiling, not a
// latency target — a plugin routinely approaching it has a performance problem
// that a smaller number would convert into an outage.
const dispatchBudget = 30 * time.Second

// ErrDispatchBudget reports a plugin handler that did not return within
// dispatchBudget. Owned plugin admission is fenced before teardown.
var ErrDispatchBudget = errors.New("pluginhost: plugin handler exceeded its dispatch budget")

// ErrHostShuttingDown reports a dispatch released early because the host is
// unloading. It is distinct from ErrDispatchBudget because they mean opposite
// things about the plugin: one is a plugin that was too slow, the other is a
// plugin that was doing nothing wrong when the process decided to stop.
var ErrHostShuttingDown = errors.New("pluginhost: host is shutting down")

// guarded runs one plugin dispatch under the host's isolation contract and
// returns whichever comes first: the plugin's answer, a recovered panic as an
// ordinary error, the budget, the caller's own cancellation, or shutdown.
//
// The result channel is buffered so a late plugin send does not block forever
// on a receiver that has already given up — which would turn a leaked goroutine
// into a leaked goroutine holding a reference to the whole call.
func guarded[T any](
	ctx context.Context,
	release <-chan struct{},
	subject string,
	budget time.Duration,
	call func(context.Context) (T, error),
) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	results := make(chan outcome, 1)

	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				// A panic crossing a goroutine boundary takes the process with
				// it, so it is recovered on the goroutine that raised it and
				// handed back as an error. The plugin's own stack is lost here
				// and that is the tradeoff: an operator gets a named refusal
				// instead of a core dump of the whole server.
				var zero T
				results <- outcome{value: zero, err: fmt.Errorf("%s panicked: %v", subject, recovered)}
			}
		}()
		value, err := call(bounded)
		results <- outcome{value: value, err: err}
	}()

	var zero T
	select {
	case result := <-results:
		if ctx.Err() != nil {
			return zero, fmt.Errorf("%s: %w", subject, ctx.Err())
		}
		if errors.Is(bounded.Err(), context.DeadlineExceeded) {
			return zero, fmt.Errorf("%w: %s did not return within %s", ErrDispatchBudget, subject, budget)
		}
		return result.value, result.err
	case <-release:
		return zero, fmt.Errorf("%w: %s was still running and was not waited on", ErrHostShuttingDown, subject)
	case <-bounded.Done():
		// One case rather than two. `bounded` derives from the caller's context,
		// so a disconnected browser or an abandoned tool call releases here
		// immediately rather than at the budget — and selecting on both would
		// race, which matters because the two produce different messages and a
		// caller that cancelled did not "exceed its dispatch budget".
		if ctx.Err() != nil {
			return zero, fmt.Errorf("%s: %w", subject, ctx.Err())
		}
		if errors.Is(bounded.Err(), context.DeadlineExceeded) {
			return zero, fmt.Errorf("%w: %s did not return within %s", ErrDispatchBudget, subject, budget)
		}
		return zero, fmt.Errorf("%s: %w", subject, bounded.Err())
	}
}

// guardedMCPHandler wraps a plugin's tool dispatch. Registration installs it,
// so MCPTools() hands internal/mcp a handler that is already bounded and
// already panic-safe.
type guardedMCPHandler struct {
	name    string
	release <-chan struct{}
	budget  time.Duration
	inner   subprocess.MCPHandler
	owner   *registrationOwner
}

func (g guardedMCPHandler) MCPCallTool(
	ctx context.Context,
	request subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	return ownedGuard(ctx, g.owner, g.release, "plugin tool "+g.name, g.budget,
		func(bounded context.Context) (subprocess.MCPCallResult, error) {
			return g.inner.MCPCallTool(bounded, request)
		})
}

// guardedHTTPHandler wraps a plugin's route dispatch, for the same reason and
// with the same contract.
type guardedHTTPHandler struct {
	pattern string
	release <-chan struct{}
	budget  time.Duration
	inner   subprocess.HTTPHandler
	owner   *registrationOwner
}

func (g guardedHTTPHandler) HTTPHandle(
	ctx context.Context,
	request subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	return ownedGuard(ctx, g.owner, g.release, "plugin route "+g.pattern, g.budget,
		func(bounded context.Context) (subprocess.HTTPResponse, error) {
			return g.inner.HTTPHandle(bounded, request)
		})
}

func ownedGuard[T any](ctx context.Context, owner *registrationOwner, release <-chan struct{}, subject string, budget time.Duration, call func(context.Context) (T, error)) (T, error) {
	if owner == nil {
		return guarded(ctx, release, subject, budget, call)
	}
	var zero T
	if !owner.available() {
		return zero, ErrPluginNotLoaded
	}
	bounded, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(owner.ctx, cancel)
	defer stop()
	defer cancel()
	value, err := guarded(bounded, release, subject, budget, call)
	if errors.Is(err, ErrDispatchBudget) {
		owner.trip(err)
	}
	if owner.ctx.Err() != nil && err == nil {
		return zero, ErrPluginNotLoaded
	}
	return value, err
}
