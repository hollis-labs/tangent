package interaction

import "fmt"

// DraftRevisionConflictError reports a draft whose revision was not the next
// one in the interaction's sequence, and says which one would have been.
//
// # Why this carries a number when the other revision conflicts do not
//
// A draft sequence is the only revision in this system a client is expected to
// predict. Every other one it echoes back from something the server told it:
// the interaction revision rides on the presentation, the surface revision on
// the record. The draft counter has no such source — `SaveDraftRevision`
// computes `MAX(revision) + 1` and the browser has to have arrived at the same
// number on its own, from a sequence it kept in memory.
//
// So a client that loses that memory — a reload, a reconnect, a second tab —
// has no way back. It cannot ask, because nothing answers the question, and it
// cannot infer, because the refusal used to echo the number it had just sent,
// which it already knew. It guessed, was refused, incremented optimistically,
// and guessed again, healing only by accident after as many clicks as there
// were drafts it had missed. That was CW-20260910-0134, and it was reachable by
// reloading a page once.
//
// The store is the only place that knows the right answer, and it knows it for
// free — it has just computed it to make this comparison. Carrying it out from
// here is what lets every layer above hand it to the client, so a refusal
// becomes a correction rather than the start of a guessing game.
type DraftRevisionConflictError struct {
	InteractionID string
	// Sent is the revision the client built its update on.
	Sent int64
	// Expected is the revision that would have been accepted. It is the value
	// a client should resynchronize to; sending it next will succeed unless
	// another writer lands in between, which is the ordinary case this same
	// error then reports again with a new number.
	Expected int64
}

func (e *DraftRevisionConflictError) Error() string {
	return fmt.Sprintf("%v: draft revision %d on interaction %s; expected %d",
		ErrRevisionConflict, e.Sent, e.InteractionID, e.Expected)
}

// Unwrap keeps every existing `errors.Is(err, ErrRevisionConflict)` true. This
// error narrows an existing refusal rather than introducing a new one, so no
// caller has to learn about it to keep behaving correctly — a caller that does
// not care still sees the conflict it always saw.
func (e *DraftRevisionConflictError) Unwrap() error { return ErrRevisionConflict }
