// Package room owns the live room/session model that bridges the MCP
// server (server-side envelope producers) to a per-session browser tab
// over WebSocket, while persisting room history in SQLite.
//
// Design notes:
//
//  1. Multi-room is the default. Each MCP triage call creates a new
//     Room with its own Conn, pending-envelope map, and metadata. Two
//     parallel callers never collide.
//  2. Conn is replaceable but not multiplexed. One WebSocket per Room
//     at a time; refresh-tab semantics work because Pending entries
//     live on the Room, not the Conn.
//  3. Disconnect rejects pending. Closing a Room MUST fail every
//     Pending with ROOM_DISCONNECTED so MCP callers never hang.
//  4. Room metadata and resolved envelope state are persisted in
//     SQLite. Active Pending entries still live in memory; restart
//     hydration rebuilds active rooms and times out stale pending rows.
package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	envelopes "github.com/hollis-labs/go-envelopes"
)

// ErrPendingExists is returned by Push if the same envelope id is
// already in flight on this Room.
var ErrPendingExists = fmt.Errorf("room: pending envelope id already in flight")

// Pending is the in-flight record for a single envelope awaiting its
// Response. It lives on Room.pending, keyed by envelope id, and is
// removed by HandleResponse / HandleCancel / room close.
type Pending struct {
	EnvelopeID string
	Envelope   *envelopes.Envelope
	RespCh     chan *envelopes.Response
	ErrCh      chan error
	Cancel     func() error
	Deadline   time.Time

	settleOnce sync.Once
}

func (p *Pending) settle(fn func()) {
	p.settleOnce.Do(func() {
		fn()
	})
}

// Room is one bridge between an MCP envelope producer and a browser tab.
// One Room == one /r/<roomID> URL == (eventually) one WS connection.
type Room struct {
	ID        string
	CreatedAt time.Time
	Meta      map[string]string

	db *sql.DB

	connMu sync.Mutex
	conn   *websocket.Conn

	pendingMu sync.Mutex
	pending   map[string]*Pending

	advanceClaimed atomic.Bool

	closed      atomic.Bool
	closeReason atomic.Value // string

	connectedMu sync.Mutex
	connectedCh chan struct{}
}

type outboundEnvelopeMessage struct {
	Type       string              `json:"type"`
	EnvelopeID string              `json:"envelopeId"`
	Envelope   *envelopes.Envelope `json:"envelope"`
}

func newRoom(id string, createdAt time.Time, meta map[string]string, db *sql.DB) *Room {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return &Room{
		ID:          id,
		CreatedAt:   createdAt.UTC(),
		Meta:        cloneMeta(meta),
		db:          db,
		pending:     make(map[string]*Pending),
		connectedCh: make(chan struct{}),
	}
}

// MetaCopy returns a stable snapshot of the Room metadata.
func (r *Room) MetaCopy() map[string]string {
	return cloneMeta(r.Meta)
}

// AttachConn binds a WebSocket connection to the Room, replacing any
// prior Conn.
func (r *Room) AttachConn(ctx context.Context, conn *websocket.Conn) {
	r.connMu.Lock()
	prev := r.conn
	r.conn = conn

	r.connectedMu.Lock()
	close(r.connectedCh)
	r.connectedCh = make(chan struct{})
	r.connectedMu.Unlock()
	r.connMu.Unlock()

	if prev != nil {
		_ = prev.Close(websocket.StatusNormalClosure, "replaced")
	}
	_ = ctx
}

// DetachConn clears the active conn if it matches the supplied one.
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
func (r *Room) HasConn() bool {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.conn != nil
}

// Push persists a pending envelope row, sends the envelope on the live
// WebSocket conn, and blocks until response, cancel, close, or ctx.
func (r *Room) Push(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	if env == nil {
		return nil, fmt.Errorf("room: push: nil envelope")
	}
	if r.closed.Load() {
		return nil, fmt.Errorf("%w: %s", ErrRoomClosed, r.closeReasonString())
	}

	pending := &Pending{
		EnvelopeID: env.ID,
		Envelope:   cloneEnvelope(env),
		RespCh:     make(chan *envelopes.Response, 1),
		ErrCh:      make(chan error, 1),
		Deadline:   deadlineFromCtx(ctx),
	}
	pending.Cancel = func() error {
		return fmt.Errorf("%w: envelope %q", ErrUserCancelled, env.ID)
	}

	r.pendingMu.Lock()
	if _, exists := r.pending[env.ID]; exists {
		r.pendingMu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrPendingExists, env.ID)
	}
	r.pending[env.ID] = pending
	r.pendingMu.Unlock()

	defer func() {
		r.pendingMu.Lock()
		delete(r.pending, env.ID)
		r.pendingMu.Unlock()
	}()

	if err := r.persistPendingEnvelope(env); err != nil {
		return nil, fmt.Errorf("room: persist pending envelope %q: %w", env.ID, err)
	}
	if err := r.sendEnvelopeWithReconnect(ctx, env); err != nil {
		return nil, r.finalizePushError(env.ID, pending, err)
	}

	select {
	case resp := <-pending.RespCh:
		return resp, nil
	case err := <-pending.ErrCh:
		return nil, err
	case <-ctx.Done():
		return nil, r.finalizePushError(env.ID, pending, ctx.Err())
	}
}

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
			r.DetachConn(conn)
		}

		r.connectedMu.Lock()
		ch := r.connectedCh
		r.connectedMu.Unlock()
		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// HandleResponse resolves the matching pending envelope, if any.
func (r *Room) HandleResponse(envelopeID string, resp *envelopes.Response) {
	p := r.pendingByID(envelopeID)
	if p == nil {
		return
	}
	p.settle(func() {
		if err := r.persistResolvedEnvelope(envelopeID, resp); err != nil {
			trySendErr(p.ErrCh, fmt.Errorf("room: persist resolved envelope %q: %w", envelopeID, err))
			return
		}
		trySendResp(p.RespCh, resp)
	})
}

// HandleCancel resolves the matching pending envelope as cancelled.
func (r *Room) HandleCancel(envelopeID string) {
	p := r.pendingByID(envelopeID)
	if p == nil {
		return
	}
	p.settle(func() {
		cancelErr := ErrUserCancelled
		if p.Cancel != nil {
			cancelErr = p.Cancel()
		}
		if err := r.persistCancelledEnvelope(envelopeID, cancelErr); err != nil {
			trySendErr(p.ErrCh, fmt.Errorf("room: persist cancelled envelope %q: %w", envelopeID, err))
			return
		}
		trySendErr(p.ErrCh, cancelErr)
	})
}

// Close marks the Room closed, persists the room close, fails every
// pending envelope, and closes the WS conn. Idempotent.
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
	r.pendingMu.Unlock()

	persistErr := r.persistRoomClose(reason)
	for _, p := range pendings {
		pending := p
		pending.settle(func() {
			if persistErr != nil {
				trySendErr(pending.ErrCh, fmt.Errorf("room: persist room close %q: %w", r.ID, persistErr))
				return
			}
			trySendErr(pending.ErrCh, fmt.Errorf("%w: %s", ErrRoomDisconnected, reason))
		})
	}

	r.connMu.Lock()
	conn := r.conn
	r.conn = nil
	r.connMu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusGoingAway, reason)
	}

	r.connectedMu.Lock()
	close(r.connectedCh)
	r.connectedCh = make(chan struct{})
	r.connectedMu.Unlock()
}

// IsClosed reports whether Close has been called on the Room.
func (r *Room) IsClosed() bool { return r.closed.Load() }

// HasPending reports whether the Room currently has one or more
// envelopes in flight.
func (r *Room) HasPending() bool {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return len(r.pending) > 0
}

// CurrentEnvelope returns one in-flight envelope snapshot if present.
func (r *Room) CurrentEnvelope() *envelopes.Envelope {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	for _, pending := range r.pending {
		return cloneEnvelope(pending.Envelope)
	}
	return nil
}

// TryClaimAdvance reserves the room for a single session_advance-style
// caller. Call ReleaseAdvance after Push returns.
func (r *Room) TryClaimAdvance() bool {
	return r.advanceClaimed.CompareAndSwap(false, true)
}

// ReleaseAdvance clears a prior TryClaimAdvance reservation.
func (r *Room) ReleaseAdvance() {
	r.advanceClaimed.Store(false)
}

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

func (r *Room) pendingByID(envelopeID string) *Pending {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return r.pending[envelopeID]
}

func (r *Room) finalizePushError(envelopeID string, pending *Pending, err error) error {
	if err == nil {
		return nil
	}
	resolvedErr := err
	pending.settle(func() {
		if persistErr := r.persistTerminalEnvelopeError(envelopeID, err); persistErr != nil {
			resolvedErr = fmt.Errorf("room: persist envelope %q final state: %w", envelopeID, persistErr)
		}
	})
	return resolvedErr
}

// Manager owns the live set of Rooms plus the optional shared DB
// handle backing persistence.
type Manager struct {
	db *sql.DB

	mu    sync.RWMutex
	rooms map[string]*Room
}

// NewManager constructs an empty Manager. The DB is optional for
// legacy in-memory tests; production wiring should always pass one.
func NewManager(db *sql.DB) *Manager {
	return &Manager{
		db:    db,
		rooms: make(map[string]*Room),
	}
}

// Create allocates a new Room with a fresh UUID and registers it.
func (m *Manager) Create(meta map[string]string) *Room {
	r, err := m.CreateWithError(meta)
	if err != nil {
		panic(err)
	}
	return r
}

// CreateWithError allocates a new Room with a fresh UUID, persists it,
// and registers it.
func (m *Manager) CreateWithError(meta map[string]string) (*Room, error) {
	r := newRoom(uuid.NewString(), time.Now().UTC(), meta, m.db)
	if err := m.persistRoomCreate(r); err != nil {
		return nil, fmt.Errorf("room: create %q: %w", r.ID, err)
	}
	m.mu.Lock()
	m.rooms[r.ID] = r
	m.mu.Unlock()
	return r, nil
}

// Hydrate loads all open rooms from the DB and times out any stale
// pending envelopes left behind by a previous process.
func (m *Manager) Hydrate(ctx context.Context) error {
	if m.db == nil {
		return nil
	}
	if err := m.timeoutStalePendingEnvelopes(ctx); err != nil {
		return err
	}
	rooms, err := m.loadActiveRooms(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range rooms {
		m.rooms[row.ID] = newRoom(row.ID, row.CreatedAt, row.Meta, m.db)
	}
	return nil
}

// Get returns the Room with the given id, or false if not found.
func (m *Manager) Get(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}

// Close closes the Room, persists its terminal state, and removes it
// from the live-room registry.
func (m *Manager) Close(id, status string) error {
	m.mu.Lock()
	r, ok := m.rooms[id]
	if ok {
		delete(m.rooms, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrRoomNotFound, id)
	}
	r.Close(status)
	return nil
}

// Remove drops the Room from the manager and closes it.
func (m *Manager) Remove(id string) {
	_ = m.Close(id, "removed by manager")
}

// Len returns the number of registered rooms.
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// IDs returns a snapshot of all current room ids.
func (m *Manager) IDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.rooms))
	for id := range m.rooms {
		ids = append(ids, id)
	}
	return ids
}

// CloseAll closes every Room in the manager.
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

func cloneEnvelope(env *envelopes.Envelope) *envelopes.Envelope {
	if env == nil {
		return nil
	}
	out := *env
	if env.Data != nil {
		out.Data = cloneAnyMap(env.Data)
	}
	if env.Meta != nil {
		out.Meta = cloneAnyMap(env.Meta)
	}
	if env.Trace != nil {
		traceCopy := *env.Trace
		out.Trace = &traceCopy
	}
	return &out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
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

func trySendResp(ch chan *envelopes.Response, resp *envelopes.Response) {
	select {
	case ch <- resp:
	default:
	}
}

func trySendErr(ch chan error, err error) {
	select {
	case ch <- err:
	default:
	}
}
