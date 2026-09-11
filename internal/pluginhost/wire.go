package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// The host side of the ADR 0007 §4 plugin wire (CW-20260910-0034).
//
// # What this is a client of, and what it is not
//
// `libs/plugin-sdk/subprocess` defines the protocol and implements the PLUGIN
// side of it: `subprocess.Serve` reads newline-delimited JSON-RPC 2.0 from
// stdin, dispatches, and writes responses to stdout. It ships no host-side
// client — there is no `exec.Command` anywhere in that module — so the spawning
// half is Tangent's to write, against a protocol that already exists.
//
// That asymmetry is worth stating because it decides what is and is not open to
// design here. The methods, the framing, the error codes and the handshake are
// the SDK's (`subprocess/protocol.go`). What follows is transport mechanics.
//
// # One reader, correlated by id
//
// Every call gets a monotonic id, registers a channel under it, and a single
// reader goroutine routes each response to the waiter that asked for it.
//
// The obvious alternative is to hold a mutex across write-then-read so only one
// call is ever outstanding. Nanite's client does that, and it is where its
// CW-20260902-0062 lives: on a read timeout it closes the underlying reader to
// unblock the abandoned read, which permanently kills the connection — every
// later call on that plugin fails, for a plugin that is alive and well.
//
// Correlation removes the reason to do that. A caller that gives up deregisters
// its own waiter and returns; the reader goroutine is untouched, still reading,
// still able to route the late response to nobody. A slow call delays itself
// and nothing else, and a timeout costs one call rather than the process.
//
// # What ends the read loop
//
// EOF on the child's stdout, which happens when the child exits — for any
// reason, including one Tangent did not ask for. Every waiter still registered
// at that point is failed with [ErrChildGone] rather than left to time out,
// because "the process you were talking to is gone" is an answer and waiting
// out a budget for it is not.

// ErrChildGone reports that a subprocess plugin's pipe closed — it exited,
// crashed, or was killed — while a call was outstanding or before one was sent.
var ErrChildGone = errors.New("pluginhost: subprocess plugin is gone")

// wire is one child's JSON-RPC connection. It owns the framing and the
// correlation; it owns neither the process nor its lifecycle (see child.go).
type wire struct {
	out io.Writer
	in  *bufio.Reader

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan subprocess.RPCResponse
	// closedBy is the error every pending and future call gets once the read
	// loop has ended. Nil while the wire is live.
	closedBy error

	// writeMu serializes writes. Frames are newline-delimited, so two
	// concurrent marshals interleaving would corrupt both.
	writeMu sync.Mutex

	// done closes when the read loop exits, so the process side can tell
	// "the child's stdout ended" from "we are still waiting".
	done chan struct{}
}

func newWire(in io.Reader, out io.Writer) *wire {
	w := &wire{
		out:     out,
		in:      bufio.NewReaderSize(in, 64*1024),
		pending: map[int64]chan subprocess.RPCResponse{},
		done:    make(chan struct{}),
	}
	go w.read()
	return w
}

// call sends one request and waits for the response with that id.
//
// ctx bounds this call and nothing else. Canceling it deregisters the waiter
// and returns; it does not touch the connection, does not cancel the child's
// work, and does not affect any other call in flight.
func (w *wire) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := w.nextID.Add(1)
	reply := make(chan subprocess.RPCResponse, 1)

	w.mu.Lock()
	if w.closedBy != nil {
		err := w.closedBy
		w.mu.Unlock()
		return nil, err
	}
	w.pending[id] = reply
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
	}()

	if err := w.writeFrame(subprocess.RPCRequest{
		JSONRPC: "2.0", ID: id, Method: method, Params: params,
	}); err != nil {
		return nil, fmt.Errorf("pluginhost: send %s: %w", method, err)
	}

	select {
	case response := <-reply:
		if response.Error != nil {
			return nil, fmt.Errorf("pluginhost: %s: %w", method, response.Error)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("pluginhost: %s: %w", method, ctx.Err())
	case <-w.done:
		return nil, fmt.Errorf("pluginhost: %s: %w", method, ErrChildGone)
	}
}

func (w *wire) writeFrame(request subprocess.RPCRequest) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	encoded = append(encoded, '\n')

	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if _, err := w.out.Write(encoded); err != nil {
		return err
	}
	return nil
}

// read is the single reader. It runs until the child's stdout ends.
func (w *wire) read() {
	var readErr error
	for {
		line, err := w.in.ReadBytes('\n')
		if len(line) > 0 {
			w.deliver(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	w.closeWith(readErr)
}

// deliver routes one response frame to whoever is waiting for its id.
//
// A frame that does not parse, or whose id nobody is waiting for, is dropped
// rather than fatal. Both are reachable without the child misbehaving: a late
// response to a call whose caller already gave up has no waiter by definition,
// and killing the connection over it would turn one abandoned call into an
// outage.
func (w *wire) deliver(line []byte) {
	var response subprocess.RPCResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return
	}
	w.mu.Lock()
	reply, waiting := w.pending[response.ID]
	w.mu.Unlock()
	if !waiting {
		return
	}
	select {
	case reply <- response:
	default:
	}
}

// closeWith fails every outstanding call and every later one.
func (w *wire) closeWith(cause error) {
	w.mu.Lock()
	if w.closedBy == nil {
		if cause != nil {
			w.closedBy = fmt.Errorf("%w: %w", ErrChildGone, cause)
		} else {
			w.closedBy = ErrChildGone
		}
	}
	w.pending = map[int64]chan subprocess.RPCResponse{}
	w.mu.Unlock()
	close(w.done)
}
