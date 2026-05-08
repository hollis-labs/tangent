package envelope

import "errors"

// Sentinel errors for the dispatcher. Use errors.Is to branch on these
// from caller code. Underlying-registry errors (ErrUnknownType,
// ErrSchemaValidation, etc.) flow through verbatim from go-envelopes;
// these supplement them with dispatcher-specific failures.
var (
	// ErrHandlerExists is returned by Dispatcher.Register when a handler
	// is already registered for the given envelope type. The dispatcher
	// does not silently overwrite — callers must Unregister first.
	ErrHandlerExists = errors.New("envelope: handler already registered")

	// ErrNoHandler is returned by Dispatcher.Dispatch when no handler is
	// registered for the envelope type. The envelope passed registry
	// validation, so the type is known; it's the dispatcher routing
	// table that's incomplete.
	ErrNoHandler = errors.New("envelope: no handler registered")

	// ErrNilResponse is returned by Dispatcher.Dispatch when a handler
	// returns (nil, nil). Handlers must produce either an error or a
	// non-nil response; nil/nil is a contract violation, not a soft
	// success path.
	ErrNilResponse = errors.New("envelope: handler returned nil response without error")
)
