// Package room owns the live room/session model that bridges the MCP
// server (server-side envelope producers) to a per-session browser tab
// over WebSocket, while persisting room history in SQLite.
//
// Design notes:
//
//  1. Multi-room is the default. Each MCP triage call creates a new
//     Room with its own connection set, pending-envelope map, and
//     metadata. Two parallel callers never collide.
//  2. Connections are multiplexed. A Room tracks a set of live
//     connections in distinct roles (see connection.go): several tabs
//     may observe one surface simultaneously, and exactly one holds the
//     resolver lease that lets a submission become terminal. Refresh-tab
//     semantics work because Pending entries live on the Room, and
//     because a reconnecting tab is recognized by its client id and
//     replaces only its own predecessor.
//  3. A WebSocket is a replaceable presentation attachment. Transport
//     loss only changes connection state; explicit Room.Close remains
//     the authority that fails pending work.
//  4. Room metadata and resolved envelope state are persisted in SQLite.
//     Legacy-only Pending entries still live in memory and receive a
//     compatibility timeout; canonical durable interactions recover without
//     being rewritten by room hydration.
package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

	TransformResponse func(*envelopes.Response) (*envelopes.Response, error)

	// disposition is the canonical lifecycle authority for this envelope. When
	// it is set the Room is a presentation attachment only: a participant's
	// terminal action reaches the durable substrate through the disposition
	// before any projection row, waiter, or channel observes it.
	disposition Disposition

	// durable marks a presentation whose lifetime is owned by canonical
	// records rather than by any waiting caller. A durable entry survives
	// caller timeout, transport loss, and browser disconnect; only a terminal
	// disposition removes it.
	durable bool

	// done is closed when the entry settles so the presenter goroutine can
	// exit without leaking.
	done chan struct{}

	settleOnce sync.Once
	settled    atomic.Bool

	// presentationMu serializes delivery and terminal compare-and-set for this
	// pending envelope. presentationRevision is a single monotonic counter
	// across every connection: each frame written to any connection consumes
	// the next value, so a revision names one presentation to one client and
	// two tabs can never hold the same one.
	presentationMu       sync.Mutex
	presentationRevision int64
	// presentedConnectionID names the connection that received the most recent
	// frame. It is reported to the canonical authority as an operational fact
	// and is never an authorization input.
	presentedConnectionID string
}

func (p *Pending) settle(fn func()) {
	p.settleOnce.Do(func() {
		p.settled.Store(true)
		if p.done != nil {
			close(p.done)
		}
		fn()
	})
}

// Disposition is the canonical lifecycle authority behind a durable
// presentation. A Room relays participant actions to it and never decides a
// terminal outcome on its own: transport loss, caller timeout, and browser
// disconnect are presentation facts, not lifecycle facts.
type Disposition interface {
	// Presented records that the participant is now looking at revision of the
	// envelope on the named connection. Repeat presentations for the same
	// envelope are idempotent; the connection id is an operational fact only.
	Presented(
		ctx context.Context,
		roomID string,
		env *envelopes.Envelope,
		revision int64,
		connectionID string,
	) error
	// Resolve records the participant's immutable terminal response. Returning
	// an error leaves the interaction open so the participant can submit again;
	// the Room must not settle.
	Resolve(ctx context.Context, roomID string, env *envelopes.Envelope, resp *envelopes.Response) error
	// Cancel records an explicit participant cancellation.
	Cancel(ctx context.Context, roomID string, env *envelopes.Envelope) error
	// DurableRevision reports the canonical record this presentation projects,
	// so an attaching connection can be synchronized from durable state rather
	// than from whatever the previous socket happened to hold.
	DurableRevision(ctx context.Context) (DurableRevision, error)
}

// Room is one bridge between an MCP envelope producer and a browser tab.
// One Room == one /r/<roomID> URL == (eventually) one WS connection.
type Room struct {
	ID        string
	CreatedAt time.Time
	Meta      map[string]string

	db *sql.DB

	phaseMu       sync.RWMutex
	currentPhase  string
	phasesVisited []string
	phaseOutputs  map[string]PhaseOutput

	// connMu guards the connection set and the resolver lease together: which
	// connections exist and which one may resolve are one consistent fact.
	connMu sync.Mutex
	// connections holds every live attachment, keyed by server-issued id.
	connections map[string]*Connection
	// connSeq counts attachments for the life of the Room. It supplies default
	// connection labels and identifies the surface's first-ever connection,
	// which is the only one a pre-revision client may answer without a
	// revision.
	connSeq uint64
	// lease names the single connection currently permitted to produce a
	// terminal participant action. Nil means the right is unclaimed.
	lease *resolverLease

	pendingMu sync.Mutex
	pending   map[string]*Pending

	advanceClaimed atomic.Bool

	closed      atomic.Bool
	closeReason atomic.Value // string

	connectedMu sync.Mutex
	connectedCh chan struct{}

	// lifetime bounds durable presenter goroutines. It ends when the Room is
	// explicitly closed, never when a caller or socket goes away.
	lifetime    context.Context
	endLifetime context.CancelFunc
}

type outboundEnvelopeMessage struct {
	Type       string              `json:"type"`
	EnvelopeID string              `json:"envelopeId"`
	Revision   int64               `json:"revision"`
	Envelope   *envelopes.Envelope `json:"envelope"`
}

// outboundConnectionMessage reports the Connection lifecycle to one client.
// It is deliberately a frame of its own: connection state changes for reasons
// that have nothing to do with any interaction, and a client must be able to
// render "another tab is here" without an envelope in hand.
type outboundConnectionMessage struct {
	Type         string           `json:"type"`
	ConnectionID string           `json:"connectionId"`
	Role         ConnectionRole   `json:"role"`
	RoomID       string           `json:"roomId"`
	Lease        *LeaseView       `json:"lease,omitempty"`
	Connections  []ConnectionView `json:"connections"`
}

// outboundSyncMessage tells one client which durable revisions its view now
// corresponds to.
type outboundSyncMessage struct {
	Type string      `json:"type"`
	Sync SurfaceSync `json:"sync"`
}

func newRoom(id string, createdAt time.Time, meta map[string]string, phaseState PhaseState, db *sql.DB) *Room {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	// The cancel function is retained on the Room and called exactly once by
	// Close. Presentation must outlive every request context, so it cannot be
	// derived from one.
	lifetime, endLifetime := context.WithCancel(context.Background()) //nolint:gosec // G118: cancelled by Room.Close
	return &Room{
		ID:            id,
		CreatedAt:     createdAt.UTC(),
		Meta:          cloneMeta(meta),
		db:            db,
		currentPhase:  phaseState.CurrentPhase,
		phasesVisited: cloneStringSlice(phaseState.PhasesVisited),
		phaseOutputs:  clonePhaseOutputs(phaseState.PhaseOutputs),
		pending:       make(map[string]*Pending),
		connections:   make(map[string]*Connection),
		connectedCh:   make(chan struct{}),
		lifetime:      lifetime,
		endLifetime:   endLifetime,
	}
}

// MetaCopy returns a stable snapshot of the Room metadata.
func (r *Room) MetaCopy() map[string]string {
	return cloneMeta(r.Meta)
}

// AttachConn registers a WebSocket connection with the Room and returns the
// Connection that owns it.
//
// Attaching never disturbs an unrelated connection: two tabs observing one
// surface, and one tab moving between surfaces, both leave every other
// attachment exactly as it was. The single exception is a client reconnecting
// under a client id it already holds — a refresh of the same tab — which
// replaces its own predecessor and inherits its resolver lease. That is the
// one-active-connection compatibility policy ADR 0001 permits, narrowed to the
// one case where a second live socket would be a duplicate rather than a peer.
//
// Pending envelopes are replayed separately by ReplayPending once the handler
// owns the attachment.
func (r *Room) AttachConn(
	ctx context.Context,
	conn *websocket.Conn,
	opts AttachOptions,
) (*Connection, error) {
	now := time.Now().UTC()

	r.connMu.Lock()
	if r.closed.Load() {
		r.connMu.Unlock()
		return nil, ErrRoomClosed
	}
	r.connSeq++
	connection := newConnection(conn, opts, r.connSeq, now)
	replaced := r.evictClientLocked(connection.clientID)
	r.connections[connection.id] = connection
	// A replaced tab hands its lease to its own reconnection rather than
	// dropping it: a refresh must not demote the operator to an observer.
	inheritsLease := false
	for _, prior := range replaced {
		if r.lease != nil && r.lease.connectionID == prior.id {
			inheritsLease = true
		}
	}
	if opts.Role != RoleObserver && (inheritsLease || r.lease.expired(now) || !r.leaseHolderLiveLocked()) {
		r.grantLeaseLocked(connection, now)
	}
	r.connMu.Unlock()

	r.signalAttachment()

	for _, prior := range replaced {
		closeReplaced(prior.conn)
	}
	r.BroadcastConnectionState(ctx)
	return connection, nil
}

// closeGrace bounds a closing handshake with a client.
//
// websocket.Conn.Close waits for the peer to answer. A cooperative peer answers
// in microseconds on loopback, but a tab that has stopped reading would
// otherwise hold up the refresh that replaced it — or, with several tabs
// attached, multiply that delay across every one of them when a room closes.
// Once the grace elapses the socket is forced down. The close frame is already
// on the wire by then, so a peer that later reads still sees the intended
// closure.
const closeGrace = 250 * time.Millisecond

func closeSocket(conn *websocket.Conn, status websocket.StatusCode, reason string) {
	if conn == nil {
		return
	}
	watchdog := time.AfterFunc(closeGrace, func() { _ = conn.CloseNow() })
	defer watchdog.Stop()
	_ = conn.Close(status, reason)
}

func closeReplaced(conn *websocket.Conn) {
	closeSocket(conn, websocket.StatusNormalClosure, "replaced")
}

// evictClientLocked removes every prior connection carrying clientID and
// returns them so the caller can close their sockets outside the lock. An
// empty client id never matches: a client that declines to identify its tab
// gets peer semantics, not replacement semantics.
func (r *Room) evictClientLocked(clientID string) []*Connection {
	if clientID == "" {
		return nil
	}
	var replaced []*Connection
	for id, existing := range r.connections {
		if existing.clientID != clientID {
			continue
		}
		delete(r.connections, id)
		replaced = append(replaced, existing)
	}
	return replaced
}

// leaseHolderLiveLocked reports whether the lease names a connection that is
// still attached. A lease left behind by a vanished connection is free.
func (r *Room) leaseHolderLiveLocked() bool {
	if r.lease == nil {
		return false
	}
	_, ok := r.connections[r.lease.connectionID]
	return ok
}

func (r *Room) grantLeaseLocked(c *Connection, now time.Time) {
	r.lease = &resolverLease{
		connectionID: c.id,
		clientID:     c.clientID,
		label:        c.label,
		grantedAt:    now,
		expiresAt:    now.Add(ResolverLeaseTTL),
	}
}

// signalAttachment wakes every presenter goroutine waiting for a client.
func (r *Room) signalAttachment() {
	r.connectedMu.Lock()
	close(r.connectedCh)
	r.connectedCh = make(chan struct{})
	r.connectedMu.Unlock()
}

// DetachConn removes a connection from the surface and releases its resolver
// lease. It reports whether the connection was still attached.
//
// Detaching is a connection fact only. No surface, interaction, or durable
// record changes here, and the envelopes this connection was shown stay
// presented for whoever is still looking.
func (r *Room) DetachConn(c *Connection) bool {
	if c == nil {
		return false
	}
	r.connMu.Lock()
	existing, ok := r.connections[c.id]
	if ok && existing == c {
		delete(r.connections, c.id)
		if r.lease != nil && r.lease.connectionID == c.id {
			r.lease = nil
		}
	}
	r.connMu.Unlock()
	if !ok {
		return false
	}
	r.BroadcastConnectionState(context.Background())
	return true
}

// HasConn reports whether the Room currently has at least one attached
// connection.
func (r *Room) HasConn() bool {
	return r.ConnectionCount() > 0
}

// ConnectionCount returns how many connections are attached.
func (r *Room) ConnectionCount() int {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return len(r.connections)
}

// IsAttached reports whether c is still one of the Room's connections.
func (r *Room) IsAttached(c *Connection) bool {
	if c == nil {
		return false
	}
	r.connMu.Lock()
	defer r.connMu.Unlock()
	existing, ok := r.connections[c.id]
	return ok && existing == c
}

// RoleOf reports the role c currently holds.
func (r *Room) RoleOf(c *Connection) ConnectionRole {
	if c == nil {
		return RoleObserver
	}
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.roleOfLocked(c.id, time.Now().UTC())
}

func (r *Room) roleOfLocked(connectionID string, now time.Time) ConnectionRole {
	if r.lease == nil || r.lease.connectionID != connectionID || r.lease.expired(now) {
		return RoleObserver
	}
	return RoleResolver
}

// ConnectionState snapshots the Connection lifecycle of this surface.
func (r *Room) ConnectionState() ConnectionState {
	now := time.Now().UTC()
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.connectionStateLocked(now)
}

func (r *Room) connectionStateLocked(now time.Time) ConnectionState {
	views := make([]ConnectionView, 0, len(r.connections))
	for _, c := range r.connections {
		views = append(views, ConnectionView{
			ID:             c.id,
			ClientID:       c.clientID,
			Label:          c.label,
			ClientKind:     c.clientKind,
			Role:           r.roleOfLocked(c.id, now),
			AttachedAt:     c.attachedAt,
			ParticipantRef: c.participant.PrincipalRef,
		})
	}
	// Attach order is the only stable order a surface has, and it is the order
	// an operator expects to read a tab list in.
	sort.Slice(views, func(i, j int) bool {
		if views[i].AttachedAt.Equal(views[j].AttachedAt) {
			return views[i].ID < views[j].ID
		}
		return views[i].AttachedAt.Before(views[j].AttachedAt)
	})
	return ConnectionState{
		RoomID:      r.ID,
		Connections: views,
		Lease:       r.lease.view(now),
		ObservedAt:  now,
	}
}

// ClaimResolver grants c the resolver lease.
//
// It succeeds when the lease is unheld, already c's, expired, or held by a
// connection that is no longer attached. It fails with a *LeaseConflictError
// when a live peer holds it, unless takeover is set: this is a single-operator
// host, the person at both tabs is the same person, and refusing a deliberate
// takeover would strand a surface behind a frozen tab. A takeover is explicit,
// is reported to the evicted holder, and never changes interaction state.
func (r *Room) ClaimResolver(c *Connection, takeover bool) (ConnectionState, error) {
	if c == nil {
		return ConnectionState{}, errConnectionDetached
	}
	now := time.Now().UTC()

	r.connMu.Lock()
	if existing, ok := r.connections[c.id]; !ok || existing != c {
		r.connMu.Unlock()
		return ConnectionState{}, errConnectionDetached
	}
	if r.closed.Load() {
		r.connMu.Unlock()
		return ConnectionState{}, ErrRoomClosed
	}
	held := r.lease != nil && r.lease.connectionID != c.id &&
		!r.lease.expired(now) && r.leaseHolderLiveLocked()
	if held && !takeover {
		conflict := &LeaseConflictError{Holder: *r.lease.view(now)}
		r.connMu.Unlock()
		return ConnectionState{}, conflict
	}
	r.grantLeaseLocked(c, now)
	state := r.connectionStateLocked(now)
	r.connMu.Unlock()

	r.BroadcastConnectionState(context.Background())
	return state, nil
}

// ReleaseResolver drops c's resolver lease if it holds one. Releasing is a
// courtesy that lets a peer take over without a takeover; it changes nothing
// else.
func (r *Room) ReleaseResolver(c *Connection) ConnectionState {
	if c == nil {
		return r.ConnectionState()
	}
	now := time.Now().UTC()
	r.connMu.Lock()
	if r.lease != nil && r.lease.connectionID == c.id {
		r.lease = nil
	}
	state := r.connectionStateLocked(now)
	r.connMu.Unlock()

	r.BroadcastConnectionState(context.Background())
	return state
}

// TouchResolver renews c's lease if it holds one.
//
// Every inbound frame from the holder renews it, which is what keeps the TTL a
// liveness check rather than a timer the operator has to beat. Presentation
// writes deliberately do not renew: those are server-driven and would keep a
// dead tab's lease alive forever.
func (r *Room) TouchResolver(c *Connection) {
	if c == nil {
		return
	}
	now := time.Now().UTC()
	r.connMu.Lock()
	defer r.connMu.Unlock()
	if r.lease != nil && r.lease.connectionID == c.id {
		r.lease.expiresAt = now.Add(ResolverLeaseTTL)
	}
}

// BroadcastConnectionState sends the current Connection lifecycle to every
// attached client, each seeing itself marked.
//
// A failed write is not an error worth propagating: the peer is either gone
// (its own read loop will detach it) or momentarily wedged, and connection
// state is advisory display, never correctness.
func (r *Room) BroadcastConnectionState(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	r.connMu.Lock()
	state := r.connectionStateLocked(now)
	targets := make([]*Connection, 0, len(r.connections))
	for _, c := range r.connections {
		targets = append(targets, c)
	}
	r.connMu.Unlock()

	for _, target := range targets {
		_ = target.WriteJSON(ctx, connectionMessageFor(r.ID, target, state))
	}
}

// SendConnectionState sends the current Connection lifecycle to one client.
func (r *Room) SendConnectionState(ctx context.Context, c *Connection) error {
	if c == nil {
		return errConnectionDetached
	}
	return c.WriteJSON(ctx, connectionMessageFor(r.ID, c, r.ConnectionState()))
}

func connectionMessageFor(roomID string, recipient *Connection, state ConnectionState) outboundConnectionMessage {
	views := make([]ConnectionView, len(state.Connections))
	role := RoleObserver
	for i, view := range state.Connections {
		if view.ID == recipient.id {
			view.Self = true
			role = view.Role
		}
		views[i] = view
	}
	return outboundConnectionMessage{
		Type:         "connection",
		ConnectionID: recipient.id,
		Role:         role,
		RoomID:       roomID,
		Lease:        state.Lease,
		Connections:  views,
	}
}

// Push persists a pending envelope row, sends the envelope on the live
// WebSocket conn, and blocks until response, cancel, close, or ctx.
func (r *Room) Push(ctx context.Context, env *envelopes.Envelope) (*envelopes.Response, error) {
	return r.push(ctx, env, nil)
}

// PushWithResponseTransform behaves like Push, but runs the supplied
// transform against the client response before it is persisted and returned.
func (r *Room) PushWithResponseTransform(
	ctx context.Context,
	env *envelopes.Envelope,
	transform func(*envelopes.Response) (*envelopes.Response, error),
) (*envelopes.Response, error) {
	return r.push(ctx, env, transform)
}

func (r *Room) push(
	ctx context.Context,
	env *envelopes.Envelope,
	transform func(*envelopes.Response) (*envelopes.Response, error),
) (*envelopes.Response, error) {
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
		done:       make(chan struct{}),
	}
	pending.Cancel = func() error {
		return fmt.Errorf("%w: envelope %q", ErrUserCancelled, env.ID)
	}
	pending.TransformResponse = transform

	r.pendingMu.Lock()
	if _, exists := r.pending[env.ID]; exists {
		r.pendingMu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrPendingExists, env.ID)
	}
	// Keep the pending entry invisible to reconnect replay until its durable
	// row exists. Holding pendingMu also makes same-id reservation atomic for
	// in-memory and SQLite-backed rooms.
	if err := r.persistPendingEnvelope(env); err != nil {
		r.pendingMu.Unlock()
		return nil, fmt.Errorf("room: persist pending envelope %q: %w", env.ID, err)
	}
	r.pending[env.ID] = pending
	r.pendingMu.Unlock()

	defer func() {
		r.pendingMu.Lock()
		delete(r.pending, env.ID)
		r.pendingMu.Unlock()
		r.forgetPresentation(env.ID)
	}()

	if err := r.sendEnvelopeWithReconnect(ctx, pending); err != nil {
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

func (r *Room) sendEnvelopeWithReconnect(ctx context.Context, pending *Pending) error {
	for {
		if r.closed.Load() {
			return fmt.Errorf("%w: %s", ErrRoomDisconnected, r.closeReasonString())
		}
		if err := r.presentPending(ctx, pending, nil, false); err == nil {
			return nil
		} else if !errors.Is(err, ErrNoConn) && !errors.Is(err, ErrStaleConnection) {
			return err
		}

		r.connectedMu.Lock()
		ch := r.connectedCh
		r.connectedMu.Unlock()
		// AttachConn may have completed between the failed presentation and
		// this channel snapshot. Recheck connection state to avoid waiting on
		// the next attachment after missing the one we can already use.
		if r.HasConn() {
			continue
		}
		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Present registers a durable presentation for env and returns immediately.
//
// It is the non-blocking counterpart to Push and the entry point every
// canonical room workflow uses. The registered entry belongs to the durable
// interaction, not to any caller: it survives caller timeout, transport loss,
// browser refresh, and process restart, and it is removed only when the
// participant produces a terminal disposition or the interaction is
// terminalized elsewhere. Presentation itself continues in the background so a
// room with no attached tab still shows the envelope the moment one attaches.
//
// Present is idempotent. Re-presenting an envelope that is already registered
// is a no-op, which is what makes an identical retry of the original caller
// invocation safe.
func (r *Room) Present(
	env *envelopes.Envelope,
	transform func(*envelopes.Response) (*envelopes.Response, error),
	disposition Disposition,
) error {
	if env == nil {
		return fmt.Errorf("room: present: nil envelope")
	}
	if disposition == nil {
		return fmt.Errorf("room: present: disposition is required")
	}
	if r.closed.Load() {
		return fmt.Errorf("%w: %s", ErrRoomClosed, r.closeReasonString())
	}

	pending := &Pending{
		EnvelopeID:        env.ID,
		Envelope:          cloneEnvelope(env),
		RespCh:            make(chan *envelopes.Response, 1),
		ErrCh:             make(chan error, 1),
		TransformResponse: transform,
		disposition:       disposition,
		durable:           true,
		done:              make(chan struct{}),
	}
	pending.Cancel = func() error {
		return fmt.Errorf("%w: envelope %q", ErrUserCancelled, env.ID)
	}

	r.pendingMu.Lock()
	if _, exists := r.pending[env.ID]; exists {
		r.pendingMu.Unlock()
		return nil
	}
	// The legacy row is a compatibility projection of a canonical interaction
	// that already exists, so a row left behind by an earlier process is not an
	// error to reconcile — it is the same projection.
	if err := r.persistPendingEnvelopeIfAbsent(env); err != nil {
		r.pendingMu.Unlock()
		return fmt.Errorf("room: project pending envelope %q: %w", env.ID, err)
	}
	r.pending[env.ID] = pending
	r.pendingMu.Unlock()

	go r.presentUntilSettled(pending)
	return nil
}

// Release drops a durable presentation whose interaction became terminal
// somewhere other than this room — a caller withdrawal or a surface policy
// disposition. It never writes a terminal projection: the canonical record
// already holds the outcome.
func (r *Room) Release(envelopeID string) {
	r.pendingMu.Lock()
	pending := r.pending[envelopeID]
	delete(r.pending, envelopeID)
	r.pendingMu.Unlock()
	if pending == nil {
		return
	}
	pending.presentationMu.Lock()
	pending.settle(func() {})
	pending.presentationMu.Unlock()
	r.forgetPresentation(envelopeID)
}

// forgetPresentation drops a settled envelope from every connection's
// presentation memory. Nothing depends on it afterwards — the entry is gone
// from r.pending — and leaving it would grow without bound on a long-lived
// room.
func (r *Room) forgetPresentation(envelopeID string) {
	r.connMu.Lock()
	connections := make([]*Connection, 0, len(r.connections))
	for _, connection := range r.connections {
		connections = append(connections, connection)
	}
	r.connMu.Unlock()
	for _, connection := range connections {
		connection.forgetPresentation(envelopeID)
	}
}

// presentUntilSettled keeps a durable envelope visible on whichever socket is
// currently attached. It exits when the entry settles or the Room is explicitly
// closed; a disconnected browser only pauses presentation. Presentation is
// idempotent per connection generation, so re-running this loop on every
// attachment is exactly the refresh/reconnect replay path.
func (r *Room) presentUntilSettled(pending *Pending) {
	for {
		if pending.settled.Load() || r.closed.Load() {
			return
		}
		// Snapshot the attachment signal before presenting so an attachment
		// that lands during presentation is not missed.
		r.connectedMu.Lock()
		reconnected := r.connectedCh
		r.connectedMu.Unlock()

		err := r.presentPending(r.lifetime, pending, nil, false)
		switch {
		case err == nil:
			r.reportPresentation(pending)
		case errors.Is(err, ErrNoConn), errors.Is(err, ErrStaleConnection):
		default:
			return
		}

		select {
		case <-reconnected:
		case <-pending.done:
			return
		case <-r.lifetime.Done():
			return
		}
	}
}

// reportPresentation tells the canonical authority which revision the
// participant is currently looking at, and on which connection. A failure here
// is not fatal: the durable record still owns the interaction, and the next
// attachment reports again.
func (r *Room) reportPresentation(pending *Pending) {
	pending.presentationMu.Lock()
	revision := pending.presentationRevision
	connectionID := pending.presentedConnectionID
	pending.presentationMu.Unlock()
	if revision < 1 || pending.disposition == nil {
		return
	}
	_ = pending.disposition.Presented(r.lifetime, r.ID, pending.Envelope, revision, connectionID)
}

// ReplayPending resynchronizes all unresolved envelopes onto one connection.
// Each connection is shown an envelope at its own revision, so a submission
// naming a revision another client was shown is rejected rather than accepted.
// Passing a nil connection presents to every connection that has not already
// seen the envelope.
func (r *Room) ReplayPending(ctx context.Context, c *Connection) error {
	r.pendingMu.Lock()
	pendings := make([]*Pending, 0, len(r.pending))
	for _, pending := range r.pending {
		pendings = append(pendings, pending)
	}
	r.pendingMu.Unlock()

	for _, pending := range pendings {
		if err := r.presentPending(ctx, pending, c, false); err != nil {
			return err
		}
	}
	return nil
}

// ReplayEnvelope presents the current state of one envelope again on c. It is
// used after a revision conflict so the client can resynchronize without a
// timing-dependent error exchange.
func (r *Room) ReplayEnvelope(ctx context.Context, c *Connection, envelopeID string) error {
	pending := r.pendingByID(envelopeID)
	if pending == nil {
		return nil
	}
	return r.presentPending(ctx, pending, c, true)
}

// Resynchronize re-sends the durable revision snapshot and re-presents every
// open envelope to one connection.
//
// It is the client-driven half of the Connection lifecycle's stale ->
// resynchronizing -> connected path: a client that believes it has fallen
// behind asks, and gets a fresh view built from what the surface currently
// holds rather than from anything the client kept.
func (r *Room) Resynchronize(ctx context.Context, c *Connection) error {
	if !r.IsAttached(c) {
		return errConnectionDetached
	}
	if _, err := r.SendSync(ctx, c); err != nil {
		return err
	}
	r.pendingMu.Lock()
	pendings := make([]*Pending, 0, len(r.pending))
	for _, pending := range r.pending {
		pendings = append(pendings, pending)
	}
	r.pendingMu.Unlock()
	for _, pending := range pendings {
		if err := r.presentPending(ctx, pending, c, true); err != nil {
			return err
		}
	}
	return nil
}

// presentPending writes the envelope frame to one connection, or to every
// connection that has not already seen it when target is nil.
//
// Each write consumes the next presentation revision, and the receiving
// connection remembers it. Because the counter is shared across connections
// and the memory is per connection, two clients looking at one envelope always
// hold distinct revisions, and only the frame a client actually rendered can
// be used to answer.
func (r *Room) presentPending(
	ctx context.Context,
	pending *Pending,
	target *Connection,
	force bool,
) error {
	if pending.settled.Load() {
		return nil
	}
	if r.closed.Load() {
		return ErrRoomClosed
	}

	targets, err := r.presentationTargets(target)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return ErrNoConn
	}

	delivered := 0
	var lost []*Connection
	for _, connection := range targets {
		presented, presentErr := r.presentTo(ctx, pending, connection, force)
		switch {
		case presentErr != nil:
			lost = append(lost, connection)
		case presented:
			delivered++
		default:
			// Already holds this envelope; nothing to send.
			delivered++
		}
	}
	for _, connection := range lost {
		r.DetachConn(connection)
	}
	if delivered == 0 {
		return ErrNoConn
	}
	return nil
}

// presentationTargets resolves which connections a presentation is for.
func (r *Room) presentationTargets(target *Connection) ([]*Connection, error) {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	if target != nil {
		existing, ok := r.connections[target.id]
		if !ok || existing != target {
			return nil, ErrStaleConnection
		}
		return []*Connection{target}, nil
	}
	targets := make([]*Connection, 0, len(r.connections))
	for _, connection := range r.connections {
		targets = append(targets, connection)
	}
	return targets, nil
}

// presentTo writes one envelope frame to one connection. It reports whether a
// frame was actually sent.
func (r *Room) presentTo(
	ctx context.Context,
	pending *Pending,
	connection *Connection,
	force bool,
) (bool, error) {
	pending.presentationMu.Lock()
	defer pending.presentationMu.Unlock()
	if pending.settled.Load() {
		return false, nil
	}
	if !force && connection.presentedRevision(pending.EnvelopeID) != 0 {
		return false, nil
	}

	revision := pending.presentationRevision + 1
	frame, err := json.Marshal(outboundEnvelopeMessage{
		Type:       "envelope",
		EnvelopeID: pending.EnvelopeID,
		Revision:   revision,
		Envelope:   pending.Envelope,
	})
	if err != nil {
		return false, fmt.Errorf("room: marshal envelope frame: %w", err)
	}
	// The counter advances even when the write fails. A revision names an
	// attempt to show a specific client a specific frame, and reusing one after
	// a failure would let a half-delivered frame answer for a later one.
	pending.presentationRevision = revision
	if err := connection.write(ctx, frame); err != nil {
		return false, err
	}
	connection.markPresented(pending.EnvelopeID, revision)
	pending.presentedConnectionID = connection.id
	return true, nil
}

// SendSync builds the durable revision snapshot for this surface, records it
// against the connection, and sends it.
//
// The snapshot is read from the canonical authority behind each presentation,
// not from the room's in-memory state, so a client is told which durable
// revisions its view corresponds to rather than which sockets happened to
// exist. A legacy Push presentation has no canonical record and is reported
// with Durable false.
func (r *Room) SendSync(ctx context.Context, c *Connection) (SurfaceSync, error) {
	if !r.IsAttached(c) {
		return SurfaceSync{}, errConnectionDetached
	}
	sync := r.Sync(ctx)
	c.recordSync(sync)
	if err := c.WriteJSON(ctx, outboundSyncMessage{Type: "sync", Sync: sync}); err != nil {
		return sync, err
	}
	return sync, nil
}

// Sync reads the durable revisions behind every live presentation.
func (r *Room) Sync(ctx context.Context) SurfaceSync {
	r.pendingMu.Lock()
	pendings := make([]*Pending, 0, len(r.pending))
	for _, pending := range r.pending {
		pendings = append(pendings, pending)
	}
	r.pendingMu.Unlock()

	sync := SurfaceSync{
		RoomID:        r.ID,
		SyncedAt:      time.Now().UTC(),
		Presentations: make([]PresentationSync, 0, len(pendings)),
	}
	for _, pending := range pendings {
		entry := PresentationSync{EnvelopeID: pending.EnvelopeID}
		if pending.disposition != nil {
			if durable, err := pending.disposition.DurableRevision(ctx); err == nil {
				entry.Durable = true
				entry.InteractionID = durable.InteractionID
				entry.InteractionRevision = durable.InteractionRevision
				entry.PresentedProjectionRevision = durable.PresentedProjectionRevision
				entry.State = durable.State
				if durable.SurfaceRevision > sync.SurfaceRevision {
					sync.SurfaceRevision = durable.SurfaceRevision
				}
			}
		}
		sync.Presentations = append(sync.Presentations, entry)
	}
	sort.Slice(sync.Presentations, func(i, j int) bool {
		return sync.Presentations[i].EnvelopeID < sync.Presentations[j].EnvelopeID
	})
	return sync
}

// HandleResponse resolves the matching pending envelope, if any.
func (r *Room) HandleResponse(envelopeID string, resp *envelopes.Response) {
	p := r.pendingByID(envelopeID)
	if p == nil {
		return
	}
	p.presentationMu.Lock()
	defer p.presentationMu.Unlock()
	_ = r.settleResponse(envelopeID, p, resp)
}

// HandleResponseFrom resolves an envelope on behalf of one connection.
//
// Two independent checks gate it, and they answer different questions. The
// resolver lease answers "may this client produce a terminal action at all",
// which is the serialization point when several tabs watch one surface. The
// presentation revision answers "is this client acting on the frame it was
// actually shown", which is the staleness check that survived from the
// single-socket model. Both failures are reported explicitly so the losing tab
// can say why nothing happened.
//
// revision zero is accepted only for the initial presentation on the surface's
// first-ever connection, preserving pre-revision clients; reconnects, resyncs,
// and every additional client always require an exact revision.
func (r *Room) HandleResponseFrom(
	c *Connection,
	envelopeID string,
	revision int64,
	resp *envelopes.Response,
) error {
	p, err := r.authorizeDisposition(c, envelopeID, revision)
	if err != nil || p == nil {
		return err
	}
	defer p.presentationMu.Unlock()
	return r.settleResponse(envelopeID, p, resp)
}

// authorizeDisposition runs the lease and revision checks shared by response
// and cancel. It returns the pending entry with presentationMu held, or nil
// when there is nothing to settle.
func (r *Room) authorizeDisposition(
	c *Connection,
	envelopeID string,
	revision int64,
) (*Pending, error) {
	if c == nil {
		return nil, ErrStaleConnection
	}
	now := time.Now().UTC()

	if !r.IsAttached(c) {
		return nil, ErrStaleConnection
	}
	if r.closed.Load() {
		return nil, ErrRoomClosed
	}
	// The pending lookup comes before the lease check on purpose. An envelope
	// that already settled is not work anyone is competing for, so a late frame
	// naming it must not hand its sender the surface's resolver lease.
	p := r.pendingByID(envelopeID)
	if p == nil {
		return nil, nil
	}

	r.connMu.Lock()
	switch {
	case r.lease == nil || r.lease.connectionID == c.id ||
		r.lease.expired(now) || !r.leaseHolderLiveLocked():
		// Unheld, already ours, aged out, or abandoned: a client that is about
		// to act on the surface takes the lease rather than being told to ask
		// for it first. Single-tab behavior is therefore unchanged.
		r.grantLeaseLocked(c, now)
	default:
		conflict := &LeaseConflictError{Holder: *r.lease.view(now)}
		r.connMu.Unlock()
		return nil, conflict
	}
	r.connMu.Unlock()

	p.presentationMu.Lock()
	if !c.acceptsRevision(envelopeID, revision) {
		p.presentationMu.Unlock()
		return nil, ErrPresentationRevisionConflict
	}
	return p, nil
}

func (r *Room) settleResponse(envelopeID string, p *Pending, resp *envelopes.Response) error {
	if p.settled.Load() {
		return nil
	}
	nextResp := cloneResponse(resp)
	if p.TransformResponse != nil {
		transformed, err := p.TransformResponse(nextResp)
		if err != nil {
			return r.rejectSubmission(envelopeID, p, err)
		}
		nextResp = transformed
	}
	if nextResp == nil {
		return r.rejectSubmission(
			envelopeID, p,
			fmt.Errorf("room: envelope %q response transform returned nil", envelopeID),
		)
	}
	if p.disposition != nil {
		// Canonical first. Nothing — not the v0.12 projection row, not a
		// waiting caller, not the response channel — observes this submission
		// until the durable resolution exists.
		if err := p.disposition.Resolve(r.lifetime, r.ID, p.Envelope, nextResp); err != nil {
			return r.retireStalePresentation(envelopeID, p, err)
		}
	}
	p.settle(func() {
		if err := r.persistResolvedEnvelope(envelopeID, nextResp); err != nil {
			trySendErr(p.ErrCh, fmt.Errorf("room: persist resolved envelope %q: %w", envelopeID, err))
			return
		}
		trySendResp(p.RespCh, nextResp)
	})
	r.dropDurablePending(envelopeID, p)
	return nil
}

// rejectSubmission reports a participant submission Tangent refused to accept.
//
// A durable presentation stays open: a malformed or stale submission is not a
// terminal outcome, and the participant must be able to try again. A legacy
// Push entry keeps the v0.12 behavior of failing the blocked caller, because
// nothing else would ever unblock it.
func (r *Room) rejectSubmission(envelopeID string, p *Pending, cause error) error {
	if p.durable {
		return cause
	}
	p.settle(func() {
		if persistErr := r.persistTerminalEnvelopeError(envelopeID, cause); persistErr != nil {
			trySendErr(p.ErrCh, fmt.Errorf(
				"room: persist rejected submission for envelope %q: %w", envelopeID, persistErr))
			return
		}
		trySendErr(p.ErrCh, cause)
	})
	return cause
}

// retireStalePresentation drops a presentation whose interaction already
// reached a terminal outcome elsewhere. Nothing is written: the outcome is
// immutable, and this room was only ever a view of it.
func (r *Room) retireStalePresentation(envelopeID string, p *Pending, cause error) error {
	if !errors.Is(cause, ErrDispositionTerminal) {
		return cause
	}
	p.settle(func() {})
	r.dropDurablePending(envelopeID, p)
	return cause
}

// dropDurablePending removes a settled durable entry. Legacy Push entries are
// removed by push itself when the blocked caller returns.
func (r *Room) dropDurablePending(envelopeID string, p *Pending) {
	if !p.durable {
		return
	}
	r.pendingMu.Lock()
	if current, ok := r.pending[envelopeID]; ok && current == p {
		delete(r.pending, envelopeID)
	}
	r.pendingMu.Unlock()
	r.forgetPresentation(envelopeID)
}

// HandleCancel resolves the matching pending envelope as cancelled.
func (r *Room) HandleCancel(envelopeID string) {
	p := r.pendingByID(envelopeID)
	if p == nil {
		return
	}
	p.presentationMu.Lock()
	defer p.presentationMu.Unlock()
	_ = r.settleCancel(envelopeID, p)
}

// HandleCancelFrom applies the same lease and revision compare-and-set rules
// as HandleResponseFrom.
func (r *Room) HandleCancelFrom(c *Connection, envelopeID string, revision int64) error {
	p, err := r.authorizeDisposition(c, envelopeID, revision)
	if err != nil || p == nil {
		return err
	}
	defer p.presentationMu.Unlock()
	return r.settleCancel(envelopeID, p)
}

func (r *Room) settleCancel(envelopeID string, p *Pending) error {
	if p.settled.Load() {
		return nil
	}
	if p.disposition != nil {
		// Participant cancellation is one of the two authorized ways an
		// interaction becomes terminal, so it is recorded canonically before
		// anything else observes it.
		if err := p.disposition.Cancel(r.lifetime, r.ID, p.Envelope); err != nil {
			return r.retireStalePresentation(envelopeID, p, err)
		}
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
	r.dropDurablePending(envelopeID, p)
	return nil
}

// Close marks the Room closed, persists the room close, fails every
// pending envelope, and closes the WS conn. Idempotent.
func (r *Room) Close(reason string) {
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.closeReason.Store(reason)
	// Explicit close is the only thing that ends durable presentation. Socket
	// loss and caller timeout do not reach this path.
	r.endLifetime()

	r.pendingMu.Lock()
	pendings := make([]*Pending, 0, len(r.pending))
	for _, p := range r.pending {
		pendings = append(pendings, p)
	}
	r.pendingMu.Unlock()

	persistErr := r.persistRoomClose(reason)
	for _, p := range pendings {
		pending := p
		pending.presentationMu.Lock()
		pending.settle(func() {
			if persistErr != nil {
				trySendErr(pending.ErrCh, fmt.Errorf("room: persist room close %q: %w", r.ID, persistErr))
				return
			}
			trySendErr(pending.ErrCh, fmt.Errorf("%w: %s", ErrRoomDisconnected, reason))
		})
		pending.presentationMu.Unlock()
	}

	r.connMu.Lock()
	connections := make([]*Connection, 0, len(r.connections))
	for id, connection := range r.connections {
		connections = append(connections, connection)
		delete(r.connections, id)
	}
	r.lease = nil
	r.connMu.Unlock()
	for _, connection := range connections {
		closeSocket(connection.conn, websocket.StatusGoingAway, reason)
	}

	r.signalAttachment()
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

// HasPendingOther reports whether the Room is presenting some envelope other
// than the named one. It is the admission-control question a room-backed tool
// actually needs: re-issuing the request a room is already showing is a retry,
// while issuing a different one while work is outstanding is a busy room.
func (r *Room) HasPendingOther(envelopeID string) bool {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	for id := range r.pending {
		if id != envelopeID {
			return true
		}
	}
	return false
}

// IsPresenting reports whether the named envelope is currently registered.
func (r *Room) IsPresenting(envelopeID string) bool {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	_, ok := r.pending[envelopeID]
	return ok
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
	pending.presentationMu.Lock()
	defer pending.presentationMu.Unlock()
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
	r := newRoom(uuid.NewString(), time.Now().UTC(), meta, PhaseState{}, m.db)
	if err := m.persistRoomCreate(r); err != nil {
		return nil, fmt.Errorf("room: create %q: %w", r.ID, err)
	}
	m.mu.Lock()
	m.rooms[r.ID] = r
	m.mu.Unlock()
	return r, nil
}

// Hydrate loads all open rooms from the DB. Legacy-only process-local pending
// envelopes receive their compatibility timeout; canonical interactions are
// left to durable policy-driven recovery.
func (m *Manager) Hydrate(ctx context.Context) error {
	if m.db == nil {
		return nil
	}
	if err := m.reconcilePendingEnvelopesAfterRestart(ctx); err != nil {
		return err
	}
	rooms, err := m.loadActiveRooms(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range rooms {
		m.rooms[row.ID] = newRoom(row.ID, row.CreatedAt, row.Meta, row.PhaseState, m.db)
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

func cloneResponse(resp *envelopes.Response) *envelopes.Response {
	if resp == nil {
		return nil
	}
	out := *resp
	if resp.Handle != nil {
		handleCopy := *resp.Handle
		out.Handle = &handleCopy
	}
	if resp.Error != nil {
		errCopy := *resp.Error
		out.Error = &errCopy
	}
	if resp.Meta != nil {
		out.Meta = cloneAnyMap(resp.Meta)
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
