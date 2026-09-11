package room

import "fmt"

// DraftConflictError reports a refused draft along with the revision that would
// have been accepted.
//
// It is the room-layer peer of interaction.DraftRevisionConflictError, declared
// here for the reason every projection in this package is declared here: the
// room is a view over the canonical record and does not import the store's
// types, so a value that has to cross the boundary crosses it as this package's
// own shape.
//
// The pattern is LeaseConflictError's, deliberately — a sentinel for everyone
// who only needs to know THAT the disposition was refused, wrapped by a typed
// error carrying the one fact that lets a client fix it. `errors.As` at the
// WebSocket layer is what turns it back into a number on the wire.
type DraftConflictError struct {
	InteractionID string
	// Sent is the revision the participant's browser built its update on.
	Sent int64
	// Expected is the revision that would have been accepted, and the value the
	// client should resynchronize its own sequence to.
	Expected int64
}

func (e *DraftConflictError) Error() string {
	return fmt.Sprintf("%v: %s (sent %d, expected %d)",
		ErrDispositionDraftConflict, e.InteractionID, e.Sent, e.Expected)
}

func (e *DraftConflictError) Unwrap() error { return ErrDispositionDraftConflict }
