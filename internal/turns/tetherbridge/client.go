package tetherbridge

import (
	"context"

	tether "github.com/hollis-labs/substrate/mesh/tetherclient"
)

// TetherClient abstracts the subset of Tether's client needed by the turns bridge.
// *tether.Client satisfies this interface.
type TetherClient interface {
	SendTurn(ctx context.Context, sessionID, text string) error
	SessionHealth(ctx context.Context, sessionID string) (tether.RuntimeHealthResponse, error)
	GetSession(ctx context.Context, sessionID string) (tether.Session, error)
	StreamEvents(ctx context.Context, opts tether.StreamEventsOptions) (<-chan tether.StreamEvent, <-chan error)
}

var _ TetherClient = (*tether.Client)(nil)
