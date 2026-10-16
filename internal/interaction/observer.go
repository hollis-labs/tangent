package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// TransitionPhase names which store transition an observer is told about.
type TransitionPhase string

const (
	// PhaseCreated is a newly stored interaction. An idempotent re-create that
	// returns the existing row is not a transition and is never observed.
	PhaseCreated TransitionPhase = "created"
	// PhaseResolved is a participant resolution.
	PhaseResolved TransitionPhase = "resolved"
	// PhaseTerminal is a non-resolution terminal disposition (canceled,
	// expired, failed, superseded), whichever path took it.
	PhaseTerminal TransitionPhase = "terminal"
)

// Transition describes one committed-with-the-journal state change.
type Transition struct {
	Phase TransitionPhase
	// Interaction is the record as it was just before the transition. For
	// PhaseCreated it is built from the creation parameters.
	Interaction InteractionRecord
	// To is the terminal state for PhaseTerminal and InteractionStateResolved
	// for PhaseResolved.
	To InteractionState
	// Cause is the terminal cause for PhaseTerminal.
	Cause TerminalCause
	// ResponsePayload is the resolution payload for PhaseResolved.
	ResponsePayload json.RawMessage
	// ActorRef and Authority are the actor the transition's journal row records.
	ActorRef  string
	Authority string
	// Assurance is the participant assurance recorded for a resolution.
	Assurance string
	At        time.Time
}

// TransitionObserver is called inside the transition's own transaction, after
// its journal rows are written and before it commits. Using tx for an extra
// write makes that write atomic with the transition; returning an error rolls
// the whole transition back, so an observer must only fail on a real fault.
type TransitionObserver interface {
	ObserveTransition(ctx context.Context, tx *sql.Tx, t Transition) error
}

// SetTransitionObserver installs the observer. Call it during construction,
// before the store is shared.
func (s *Store) SetTransitionObserver(o TransitionObserver) { s.observer = o }

func (s *Store) observe(ctx context.Context, tx *sql.Tx, t Transition) error {
	if s.observer == nil {
		return nil
	}
	return s.observer.ObserveTransition(ctx, tx, t)
}

// SetCommitHook installs a callback run after a create, resolve or terminal
// transaction has committed (never on rollback). It lets a relay wake as soon as
// an observer's outbox row is durable. It must be cheap and must not block;
// call it during construction, before the store is shared.
func (s *Store) SetCommitHook(f func()) { s.afterCommit = f }

func (s *Store) committed() {
	if s.afterCommit != nil {
		s.afterCommit()
	}
}
