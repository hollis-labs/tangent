// Package room owns the in-memory multi-room session model that bridges
// the MCP server (server-side envelope producers) to a per-session
// browser tab over WebSocket.
//
// Design notes:
//
//  1. Multi-room is the default. Each MCP triage call creates a new
//     Room with its own Conn, pending-envelope map, and metadata. Two
//     parallel callers never collide. This is the explicit rejection of
//     Fast-Triage's single-tab model (~/Projects-apps/fast-triage/server/
//     ws-server.ts: connecting a second client closes the first).
//
//  2. Conn is replaceable but not multiplexed. One WebSocket per Room at
//     a time — refresh-tab semantics work because the new Conn replaces
//     the old, but Pending entries live on the Room (not the Conn) so
//     the in-flight envelope survives a reattach.
//
//  3. Disconnect rejects pending. Closing a Room (via Close or because
//     the WS Conn died) MUST fail every Pending with ROOM_DISCONNECTED.
//     Fast-Triage shipped without this and an MCP caller would hang
//     forever when the user closed the tab; Tangent must not.
//
//  4. Ephemeral. No SQLite, no persistence. A server restart loses all
//     Rooms. v0.1 acceptance per the WS-bridge implementer brief.
//
// The package depends only on go-envelopes types and coder/websocket;
// it has no knowledge of MCP, HTTP, or the Tangent SPA. Transports wrap
// it.
package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	envelopes "github.com/hollis-labs/go-envelopes"
)

// Sentinel errors used by Room.Push and the MCP-side translation layer.
var (
	// ErrRoomDisconnected indicates the Room's WebSocket peer went away
	// before the pending envelope resolved. The MCP-side caller maps
	// this onto a structured tool error so clients see a clean
	// disconnect signal rather than a hang.
	ErrRoomDisconnected = errors.New("room: disconnected")

	// ErrRoomClosed indicates the Room was closed for reasons unrelated
	// to a single connection (e.g. server shutdown). Currently surfaced
	// alongside ErrRoomDisconnected via Close.
	ErrRoomClosed = errors.New("room: closed")

	// ErrPendingExists is returned by Push if the same envelope id is
	// already in flight on this Room. Envelope ids are caller-supplied
	// and assumed unique per session; this is a defensive guard.
	ErrPendingExists = errors.New("room: pending envelope id already in flight")

	// ErrNoConn is returned by Push when the Room has no active WS
	// connection. The brief defaults to hold-server-side (see Push), so
	// in normal operation Push waits for the Conn rather than failing —
	// this error is reserved for Push paths that explicitly require a
	// live conn (none in v0.1).
	ErrNoConn = errors.New("room: no active websocket connection")
)

// Pending is the in-flight record for a single envelope awaiting its
// Response. It lives on Room.pending, keyed by envelope id, and is
// removed by HandleResponse / HandleCancel / room close.
type Pending struct {
	EnvelopeID string
	RespCh     chan *envelopes.Response
	ErrCh      chan error
	Cancel     func()
	Deadline   time.Time
}

// Room is one bridge between an MCP envelope producer and a browser tab.
// One Room == one /r/<roomID> URL == (eventually) one WS connection.
//
// Concurrent use: all exported methods are safe to call from any
// goroutine. Internally the Room takes connMu for Conn (de)attachment
// and pendingMu for the pending map; they never overlap so deadlock is
// impossible by construction.
type Room struct {
	ID        string
	CreatedAt time.Time

	// Meta is a free-form key/value bag (agentID, sessionID, hint
	// metadata). Set at creation and not mutated thereafter; readers
	// take a copy in MetaCopy when they need a stable snapshot.
	Meta map[string]string

	connMu sync.Mutex
	conn   *websocket.Conn

	pendingMu sync.Mutex
	pending   map[string]*Pending

	closed      atomic.Bool
	closeReason atomic.Value // string

	// connectedCh is closed every time a fresh conn is attached. Push
	// uses this to wake when the user opens the tab AFTER the MCP call
	// arrived (replay-on-reconnect: the envelope is held server-side
	// until a conn is present, then sent).
	connectedMu sync.Mutex
	connectedCh chan struct{}
}

// outboundEnvelopeMessage is the WS frame Room.Push writes to push an
// envelope to the connected client.
type outboundEnvelopeMessage struct {
	Type       string              `json:"type"`
	EnvelopeID string              `json:"envelopeId"`
	Envelope   *envelopes.Envelope `json:"envelope"`
}

// newRoom is unexported — Manager.Create is the public constructor so
// every Room is registered in exactly one place.
func newRoom(id string, meta map[string]string) *Room {
	r := &Room{
		ID:          id,
		CreatedAt:   time.Now(),
		Meta:        cloneMeta(meta),
		pending:     make(map[string]*Pending),
		connectedCh: make(chan struct{}),
	}
	return r
}

// MetaCopy returns a snapshot of the Room's metadata. Callers (e.g.
// log lines, SPA hydration) get a stable map they can iterate without
// holding any locks; the Room's Meta map is treated as immutable
// post-construction so this copy is cheap and race-free.
func (r *Room) MetaCopy() map[string]string {
	return cloneMeta(r.Meta)
}

// AttachConn binds a WebSocket connection to the Room, replacing any
// prior Conn. The previous Conn (if any) is gracefully closed with code
// 1000 + reason "replaced". Pending entries are left intact — the
// envelope is replayed on the new Conn by Push (or, if Push already
// returned, the next push will use the new Conn).
//
// In v0.1 we ALSO replay any in-flight pending envelopes on attach so a
// user who refreshes the tab gets the live envelope back without a
// dance with the MCP caller. Reconnect semantics doc: replay-on-reconnect.
func (r *Room) AttachConn(ctx context.Context, conn *websocket.Conn) {
	r.connMu.Lock()
	prev := r.conn
	r.conn = conn

	// Replay-on-reconnect: we don't iterate r.pending here because
	// Pending intentionally does not hold the original envelope
	// payload — Push owns the lifecycle and re-sends on the
	// connectedCh wakeup below. Subsequent detach/reattach cycles
	// continue to wake parked Push calls because we swap the channel.
	r.connectedMu.Lock()
	close(r.connectedCh)
	r.connectedCh = make(chan struct{})
	r.connectedMu.Unlock()
	r.connMu.Unlock()

	if prev != nil {
		// Best-effort close on the old conn. Ignore errors — the conn
		// may already be dead, and we're racing the read loop's own
		// close path.
		_ = prev.Close(websocket.StatusNormalClosure, "replaced")
	}
	_ = ctx
}

// DetachConn clears the active conn if it matches the supplied one
// (called on read-loop exit). Does NOT touch pending entries — that's
// Close's job. Detach without close keeps the Room "warm" so a quick
// refresh hits the same Pending. Returns true when the active conn was
// actually cleared, false when this conn had already been replaced (the
// caller must not close the Room in that case, or the new conn loses
// its pending envelopes).
func (r *Room) DetachConn(conn *websocket.Conn) bool {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	if r.conn == conn {
		r.conn = nil
		return true
	}
	return false
}

// HasConn reports whether the Room currently has an attached conn.
// Used by HTTP probes and tests; Push never branches on this directly
// because the conn-park path inside Push needs the same lock anyway.
func (r *Room) HasConn() bool {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.conn != nil
}

// Push registers a Pending for env, sends the envelope on the active
// WebSocket conn, and blocks until one of:
//
//   - The client sends a matching response message → returns the
//     Response (caller validates response shape; the Room is dumb).
//   - The client sends a cancel message → returns a context.Canceled-
//     style error wrapping ErrRoomDisconnected? No — cancel is its
//     own error: see HandleCancel. Push surfaces it via ErrCh.
//   - The Room is closed (Close() or WS disconnect followed by no
//     reattach within ctx) → returns ErrRoomDisconnected.
//   - ctx fires → cleans up the Pending and returns ctx.Err().
//
// If no WS conn is attached when Push is called, Push waits for one
// (replay-on-reconnect). If ctx fires while waiting, Push returns
// ctx.Err() and removes the Pending.
func (r *Room) Push(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	if env == nil {
		return nil, fmt.Errorf("room: push: nil envelope")
	}
	if r.closed.Load() {
		return nil, fmt.Errorf("%w: %s", ErrRoomClosed, r.closeReasonString())
	}

	pending := &Pending{
		EnvelopeID: env.ID,
		RespCh:     make(chan *envelopes.Response, 1),
		ErrCh:      make(chan error, 1),
		Deadline:   deadlineFromCtx(ctx),
	}
	pending.Cancel = func() {
		select {
		case pending.ErrCh <- fmt.Errorf("user cancelled envelope %q", env.ID):
		default:
		}
	}

	r.pendingMu.Lock()
	if _, exists := r.pending[env.ID]; exists {
		r.pendingMu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrPendingExists, env.ID)
	}
	r.pending[env.ID] = pending
	r.pendingMu.Unlock()

	// Cleanup hook: always remove the Pending on return so a stray
	// late response doesn't leak memory.
	defer func() {
		r.pendingMu.Lock()
		delete(r.pending, env.ID)
		r.pendingMu.Unlock()
	}()

	// Initial send. If no conn yet, park on connectedCh until one
	// attaches OR ctx fires OR room closes.
	if err := r.sendEnvelopeWithReconnect(ctx, env); err != nil {
		return nil, err
	}

	// Wait for response, cancel, room close, or ctx.
	select {
	case resp := <-pending.RespCh:
		return resp, nil
	case err := <-pending.ErrCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sendEnvelopeWithReconnect tries to send env on the active conn. If
// the conn is missing or the send fails, it parks on connectedCh and
// retries when a new conn attaches. ctx and room-close break the loop.
func (r *Room) sendEnvelopeWithReconnect(ctx context.Context, env *envelopes.Envelope) error {
	frame, err := json.Marshal(outboundEnvelopeMessage{
		Type:       "envelope",
		EnvelopeID: env.ID,
		Envelope:   env,
	})
	if err != nil {
		return fmt.Errorf("room: marshal envelope frame: %w", err)
	}

	for {
		if r.closed.Load() {
			return fmt.Errorf("%w: %s", ErrRoomDisconnected, r.closeReasonString())
		}
		r.connMu.Lock()
		conn := r.conn
		r.connMu.Unlock()
		if conn != nil {
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, frame)
			cancel()
			if err == nil {
				return nil
			}
			// Write failed — drop this conn and re-park; the read loop
			// will surface the underlying error and call DetachConn.
			r.DetachConn(conn)
			// fall through to wait for a new conn
		}

		// Park on connectedCh for next attach.
		r.connectedMu.Lock()
		ch := r.connectedCh
		r.connectedMu.Unlock()
		select {
		case <-ch:
			// new conn attached — retry
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// HandleResponse is called by the WS read loop when a "response"
// frame arrives. Looks up the Pending and resolves it. If no Pending
// exists for envelopeID, the response is silently dropped (a late
// response after a cancel/timeout/close is expected, not an error).
func (r *Room) HandleResponse(envelopeID string, resp *envelopes.Response) {
	r.pendingMu.Lock()
	p, ok := r.pending[envelopeID]
	r.pendingMu.Unlock()
	if !ok {
		return
	}
	select {
	case p.RespCh <- resp:
	default:
		// already resolved by another path — drop
	}
}

// HandleCancel is called by the WS read loop when a "cancel" frame
// arrives. Looks up the Pending and triggers its Cancel hook so Push
// returns the cancel error to the MCP caller.
func (r *Room) HandleCancel(envelopeID string) {
	r.pendingMu.Lock()
	p, ok := r.pending[envelopeID]
	r.pendingMu.Unlock()
	if !ok {
		return
	}
	if p.Cancel != nil {
		p.Cancel()
	}
}

// Close marks the Room closed, fails every pending envelope with
// ROOM_DISCONNECTED, and closes the WS conn (if any). Idempotent: a
// second Close after the first is a no-op.
func (r *Room) Close(reason string) {
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.closeReason.Store(reason)

	r.pendingMu.Lock()
	pendings := make([]*Pending, 0, len(r.pending))
	for _, p := range r.pending {
		pendings = append(pendings, p)
	}
	// Don't delete here — Push's defer handles it on return so we don't
	// race with a concurrent Push add. Close just signals.
	r.pendingMu.Unlock()

	for _, p := range pendings {
		select {
		case p.ErrCh <- fmt.Errorf("%w: %s", ErrRoomDisconnected, reason):
		default:
		}
	}

	r.connMu.Lock()
	conn := r.conn
	r.conn = nil
	r.connMu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusGoingAway, reason)
	}

	// Wake any sendEnvelopeWithReconnect parked on connectedCh.
	r.connectedMu.Lock()
	close(r.connectedCh)
	r.connectedCh = make(chan struct{})
	r.connectedMu.Unlock()
}

// IsClosed reports whether Close has been called on the Room.
func (r *Room) IsClosed() bool { return r.closed.Load() }

func (r *Room) closeReasonString() string {
	v := r.closeReason.Load()
	if v == nil {
		return "closed"
	}
	s, _ := v.(string)
	if s == "" {
		return "closed"
	}
	return s
}

// Manager owns the live set of Rooms. Lookup is keyed by ID; the
// internal map is RWMutex-protected so Get and Create are race-free.
//
// Removal happens explicitly via Remove. The Manager does not GC
// closed rooms automatically — for v0.1 the MCP-call lifetime is the
// natural cleanup window. v0.2+ may add an idle sweeper.
type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

// NewManager constructs an empty Manager.
func NewManager() *Manager {
	return &Manager{rooms: make(map[string]*Room)}
}

// Create allocates a new Room with a fresh UUID and registers it.
// Optional meta is shallow-copied onto the Room.
func (m *Manager) Create(meta map[string]string) *Room {
	r := newRoom(uuid.NewString(), meta)
	m.mu.Lock()
	m.rooms[r.ID] = r
	m.mu.Unlock()
	return r
}

// Get returns the Room with the given id, or false if not found.
func (m *Manager) Get(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}

// Remove drops the Room from the manager and Closes it. Idempotent.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	r, ok := m.rooms[id]
	delete(m.rooms, id)
	m.mu.Unlock()
	if ok {
		r.Close("removed by manager")
	}
}

// Len returns the number of registered rooms. Used by tests / probes.
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// IDs returns a snapshot of all current room ids. Allocation is
// proportional to room count; not for hot paths.
func (m *Manager) IDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.rooms))
	for id := range m.rooms {
		ids = append(ids, id)
	}
	return ids
}

// CloseAll closes every Room in the manager (for graceful shutdown).
// Does not remove them from the map — pair with explicit teardown if
// the caller needs the map drained.
func (m *Manager) CloseAll(reason string) {
	m.mu.RLock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.RUnlock()
	for _, r := range rooms {
		r.Close(reason)
	}
}

// cloneMeta returns a deep-enough copy of m for the Room's purposes.
// Values are strings so a single map copy suffices.
func cloneMeta(m map[string]string) map[string]string {
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func deadlineFromCtx(ctx context.Context) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Time{}
	}
	return d
}
