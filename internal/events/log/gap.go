package log

import (
	"context"
	"errors"

	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// Gap reasons carried on the SSE gap frame.
const (
	GapPruned       = "pruned"
	GapCursorAhead  = "cursor_ahead"
	GapForeignEpoch = "foreign_epoch"
	// GapInvalidCursor is an unparsable cursor. The wire answer is a 200 gap
	// frame, not an error status.
	GapInvalidCursor = "invalid_cursor"
)

// GapFrame is the body of the SSE gap frame. EarliestAvailable and HighWater
// are full "<epoch>:<seq>" cursors. The client does a full resync and then
// continues from HighWater.
type GapFrame struct {
	Reason            string `json:"reason"`
	EarliestAvailable string `json:"earliest_available"`
	HighWater         string `json:"high_water"`
	Epoch             string `json:"epoch"`
}

// Resolve turns a client cursor ("<epoch>:<seq>") into the Seq to hand to
// After. An empty cursor (first connection) resolves to 0. A cursor from
// another epoch, or one that does not parse, returns a foreign_epoch gap frame
// against the current head instead, and after is meaningless. A cursor that
// does not parse is reason invalid_cursor, a foreign one foreign_epoch. An
// empty cursor is the caller's call: 0 replays from the start, Head.Latest is
// live-only.
func (l *Log) Resolve(ctx context.Context, stream, cursor string) (after streamhub.Seq, gap *GapFrame, err error) {
	if cursor == "" {
		return 0, nil, nil
	}
	epoch, err := l.Epoch(ctx)
	if err != nil {
		return 0, nil, err
	}
	ce, seq, perr := ParseCursor(cursor)
	switch {
	case perr != nil:
		g, err := l.frameFor(ctx, stream, epoch, GapInvalidCursor)
		return 0, g, err
	case ce != epoch:
		g, err := l.frameFor(ctx, stream, epoch, GapForeignEpoch)
		return 0, g, err
	}
	return seq, nil, nil
}

// FrameForError maps an After error (a *streamhub.GapError) to the frame to
// send. It returns nil, nil for any other error so the caller can handle it.
func (l *Log) FrameForError(ctx context.Context, stream string, err error) (*GapFrame, error) {
	var ge *streamhub.GapError
	if !errors.As(err, &ge) {
		return nil, nil
	}
	epoch, eerr := l.Epoch(ctx)
	if eerr != nil {
		return nil, eerr
	}
	reason := GapPruned
	if ge.Gap.Reason == streamhub.GapCursorAhead {
		reason = GapCursorAhead
	}
	return &GapFrame{
		Reason: reason, EarliestAvailable: FormatCursor(epoch, ge.Gap.OldestAvailable),
		HighWater: FormatCursor(epoch, ge.Gap.Latest), Epoch: epoch,
	}, nil
}

func (l *Log) frameFor(ctx context.Context, stream, epoch, reason string) (*GapFrame, error) {
	h, err := l.Head(ctx, stream)
	if err != nil {
		return nil, err
	}
	return &GapFrame{
		Reason: reason, EarliestAvailable: FormatCursor(epoch, h.PrunedThrough+1),
		HighWater: FormatCursor(epoch, h.Latest), Epoch: epoch,
	}, nil
}
