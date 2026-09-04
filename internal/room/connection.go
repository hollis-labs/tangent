package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// This file implements the Connection lifecycle of ADR 0001.
//
// A Room used to hold exactly one *websocket.Conn. That single field made four
// unrelated facts share one lifetime: whether anyone is looking, who may
// answer, what the browser has already been shown, and whether the surface
// still exists. The model here separates them:
//
//   - A surface tracks a set of live connections, each with a server-assigned
//     id. Attaching one never disturbs another, so two tabs can watch one
//     surface and one tab can move between surfaces without collateral damage.
//   - Exactly one connection at a time may produce a terminal participant
//     action. That right is a lease held by a connection id and renewed by
//     that connection's own traffic. Every other connection observes.
//   - A connection carries a client id supplied by the tab that opened it. A
//     new connection reusing a client id is that same tab reconnecting — a
//     refresh — and replaces its predecessor, inheriting its lease. A
//     different client id is a different tab and joins alongside.
//   - Presentation state is per connection. Each connection is shown an
//     envelope once and remembers the revision it was shown, so a submission
//     is compare-and-set against the exact frame the participant acted on.
//
// Nothing here is a lifecycle authority. Connections come and go without
// changing a surface, an interaction, or a durable record; the lease decides
// only which connection Tangent will *relay* to the canonical authority.

// ConnectionRole names what a connection may currently do with its surface.
type ConnectionRole string

const (
	// RoleResolver holds the surface's resolver lease. It is the one
	// connection whose response or cancel may become a terminal outcome.
	RoleResolver ConnectionRole = "resolver"
	// RoleObserver sees exactly what the resolver sees and changes nothing.
	RoleObserver ConnectionRole = "observer"
)

// ResolverLeaseTTL bounds how long a resolver lease survives without traffic
// from its holder.
//
// A closed socket releases the lease immediately, so the TTL exists only for
// the case a socket cannot report: a tab that is frozen, suspended, or wedged
// behind a dead network path while its TCP connection still looks alive. Three
// missed client heartbeats (10s apart) is long enough that ordinary jitter
// never revokes a working tab, and short enough that a human who gives up on a
// frozen tab and opens a new one is not left staring at an observer.
const ResolverLeaseTTL = 30 * time.Second

// connectionWriteTimeout bounds one frame write to one connection. A wedged
// socket must not hold up presentation to its peers.
const connectionWriteTimeout = 5 * time.Second

// ParticipantBinding is the principal a connection acts as.
//
// It is the seam ADR 0004 lands on: today the ws handler supplies an explicit
// unverified loopback binding, and CW-20260825-0075 replaces that with a
// participant session established from an HttpOnly cookie before the upgrade
// is accepted. Nothing in this package infers authority from it — a connection
// holds no capability and grants none — but every connection carries one, and
// it reaches the durable record through Disposition.Presented.
type ParticipantBinding struct {
	// SessionID names the participant session behind this attachment.
	//
	// It is deliberately left empty by the shipped resolver. The session is
	// the only capability material in the system, and ADR 0004 §6.1 keeps it
	// in exactly two places — the cookie header and a hash column — never in a
	// ConnectionRecord, a room's metadata, or an slog attribute. The field
	// stays so a composed identity authority that has a *non-capability*
	// reference to bind has somewhere to put it.
	SessionID    string `json:"session_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	Authority    string `json:"authority,omitempty"`
	Assurance    string `json:"assurance,omitempty"`

	// Capabilities is the object-access capability set this session was
	// granted (ADR 0004 §2).
	//
	// `json:"-"` is load-bearing, and for the same reason
	// interaction.Reference.Capability carries it: a capability set is
	// established by the host from a session, never accepted from a wire
	// frame and never projected into a durable record or a peer's connection
	// state. It rides in memory with the connection so a `response` or
	// `cancel` frame can be checked against the session that opened the
	// socket rather than against the socket itself.
	Capabilities []string `json:"-"`
}

// Holds reports whether this binding was granted a capability.
func (p ParticipantBinding) Holds(capability string) bool {
	for _, granted := range p.Capabilities {
		if granted == capability {
			return true
		}
	}
	return false
}

// AttachOptions describes one client's attempt to attach to a surface.
type AttachOptions struct {
	// ClientID is the stable identity of the browser tab (or native client)
	// behind this attachment. It is what distinguishes a refresh from a second
	// tab, and it is a routing label only: it confers nothing.
	ClientID string
	// Label is a human-readable name for the connection, shown to the operator
	// when more than one client is attached.
	Label string
	// ClientKind records the sort of client, for operator-facing display.
	ClientKind string
	// Role asks to attach as an observer, declining a free lease. It is a
	// preference at attach time, not a prohibition: a client that later acts
	// on the surface still takes an unheld lease, because refusing that would
	// leave a surface nobody can answer.
	Role ConnectionRole
	// Participant is the principal established by the transport before the
	// upgrade. See ParticipantBinding.
	Participant ParticipantBinding
}

// Connection is one live presentation attachment to a surface.
type Connection struct {
	id          string
	clientID    string
	label       string
	clientKind  string
	attachedAt  time.Time
	sequence    uint64
	participant ParticipantBinding

	conn *websocket.Conn

	// writeMu serializes frame writes. Presentation, connection-state
	// broadcasts, and error frames all contend for one socket.
	writeMu sync.Mutex

	mu sync.Mutex
	// presented records the revision at which this connection was last shown
	// each envelope. A connection that has never been shown an envelope cannot
	// resolve it, which is what keeps a replaced or newly attached socket from
	// answering a frame it never rendered.
	presented map[string]int64
	// synced is the durable revision snapshot this connection was last
	// synchronized to.
	//
	// Whether this connection is still attached is deliberately not stored
	// here: the Room's connection set is the single answer to that, and a
	// second copy could disagree with it.
	synced SurfaceSync
}

// ID returns the server-assigned connection id.
func (c *Connection) ID() string { return c.id }

// ClientID returns the client-supplied tab identity, which may be empty.
func (c *Connection) ClientID() string { return c.clientID }

// Participant returns the principal this connection acts as.
func (c *Connection) Participant() ParticipantBinding { return c.participant }

// Synced returns the durable revisions this connection was last synchronized
// to.
func (c *Connection) Synced() SurfaceSync {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced
}

func (c *Connection) markPresented(envelopeID string, revision int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.presented == nil {
		c.presented = make(map[string]int64)
	}
	c.presented[envelopeID] = revision
}

func (c *Connection) presentedRevision(envelopeID string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.presented[envelopeID]
}

func (c *Connection) forgetPresentation(envelopeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.presented, envelopeID)
}

func (c *Connection) recordSync(sync SurfaceSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.synced = sync
}

// acceptsRevision reports whether this connection may act on envelopeID at
// revision.
//
// The rule is compare-and-set against the exact frame this connection last
// rendered. Two tabs holding different revisions of the same envelope
// therefore cannot both be current, and a tab that never saw the envelope can
// never answer it.
func (c *Connection) acceptsRevision(envelopeID string, revision int64) bool {
	shown := c.presentedRevision(envelopeID)
	if shown == 0 {
		return false
	}
	if revision == shown {
		return true
	}
	// Compatibility for pre-revision clients is deliberately restricted to the
	// first presentation on the surface's first-ever connection. Once any
	// reconnect, resync, or second client has produced newer state, an
	// omitted/zero revision cannot bypass compare-and-set.
	return revision == 0 && shown == 1 && c.sequence == 1
}

// write sends one frame. Errors are the caller's signal to detach.
func (c *Connection) write(ctx context.Context, frame []byte) error {
	if c.conn == nil {
		return ErrNoConn
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, connectionWriteTimeout)
	defer cancel()
	if err := c.conn.Write(writeCtx, websocket.MessageText, frame); err != nil {
		return fmt.Errorf("%w: %w", ErrNoConn, err)
	}
	return nil
}

// Socket exposes the underlying WebSocket. The transport owns reads; the Room
// owns writes. Nothing else should touch it.
func (c *Connection) Socket() *websocket.Conn { return c.conn }

// WriteJSON marshals and sends one frame to this connection.
func (c *Connection) WriteJSON(ctx context.Context, value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("room: marshal frame for connection %q: %w", c.id, err)
	}
	return c.write(ctx, frame)
}

// ConnectionView is the operator- and agent-facing projection of one
// connection. It carries safe operational metadata only: no headers, no
// capability material, no payload.
type ConnectionView struct {
	ID             string         `json:"connection_id"`
	ClientID       string         `json:"client_id,omitempty"`
	Label          string         `json:"label,omitempty"`
	ClientKind     string         `json:"client_kind,omitempty"`
	Role           ConnectionRole `json:"role"`
	AttachedAt     time.Time      `json:"attached_at"`
	ParticipantRef string         `json:"participant_ref,omitempty"`
	// Self is filled in per recipient when the state is sent to a client, so a
	// tab can tell itself apart from its peers without string-matching ids.
	Self bool `json:"self,omitempty"`
}

// LeaseView is the operator-facing projection of the resolver lease.
type LeaseView struct {
	ConnectionID string    `json:"connection_id"`
	ClientID     string    `json:"client_id,omitempty"`
	Label        string    `json:"label,omitempty"`
	GrantedAt    time.Time `json:"granted_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	// Expired reports that the lease has aged out and the next claimant takes
	// it without a takeover.
	Expired bool `json:"expired,omitempty"`
}

// ConnectionState is the whole Connection lifecycle of one surface, projected
// for display. It is deliberately separate from anything describing an
// interaction: a surface with no work still has connections, and a surface
// with pending work still reports zero connections when nobody is looking.
type ConnectionState struct {
	RoomID      string           `json:"room_id"`
	Connections []ConnectionView `json:"connections"`
	Lease       *LeaseView       `json:"resolver_lease,omitempty"`
	ObservedAt  time.Time        `json:"observed_at"`
}

// PresentationSync names the durable record behind one live presentation.
type PresentationSync struct {
	EnvelopeID string `json:"envelope_id"`
	// Durable is false for a legacy Room.Push presentation, which has no
	// canonical record behind it and therefore no revision to synchronize to.
	Durable                     bool   `json:"durable"`
	InteractionID               string `json:"interaction_id,omitempty"`
	InteractionRevision         int64  `json:"interaction_revision,omitempty"`
	PresentedProjectionRevision int64  `json:"presented_projection_revision,omitempty"`
	State                       string `json:"state,omitempty"`
}

// SurfaceSync is the durable revision snapshot a connection is synchronized
// to.
//
// It is what makes reconnection a durable operation rather than a replay of
// whatever the previous socket happened to hold: a client is told which
// canonical revisions its view corresponds to, and can ask to be
// resynchronized when it believes it has fallen behind.
type SurfaceSync struct {
	RoomID          string             `json:"room_id"`
	SurfaceRevision int64              `json:"surface_revision"`
	SyncedAt        time.Time          `json:"synced_at"`
	Presentations   []PresentationSync `json:"presentations"`
}

// DurableRevision is the canonical record one presentation projects.
type DurableRevision struct {
	SurfaceID                   string
	SurfaceRevision             int64
	InteractionID               string
	InteractionRevision         int64
	PresentedProjectionRevision int64
	State                       string
}

// LeaseConflictError reports that another live connection holds the resolver
// lease. It is returned rather than swallowed so the losing tab learns why its
// submission did nothing, and from whom it would have to take over.
type LeaseConflictError struct {
	Holder LeaseView
}

func (e *LeaseConflictError) Error() string {
	label := e.Holder.Label
	if label == "" {
		label = e.Holder.ConnectionID
	}
	return fmt.Sprintf("%v: held by %s until %s",
		ErrResolverLeaseHeld, label, e.Holder.ExpiresAt.UTC().Format(time.RFC3339))
}

func (e *LeaseConflictError) Unwrap() error { return ErrResolverLeaseHeld }

// resolverLease is the surface-scoped serialization point for terminal
// participant actions.
type resolverLease struct {
	connectionID string
	clientID     string
	label        string
	grantedAt    time.Time
	expiresAt    time.Time
}

func (l *resolverLease) expired(now time.Time) bool {
	return l == nil || now.After(l.expiresAt)
}

func (l *resolverLease) view(now time.Time) *LeaseView {
	if l == nil {
		return nil
	}
	return &LeaseView{
		ConnectionID: l.connectionID,
		ClientID:     l.clientID,
		Label:        l.label,
		GrantedAt:    l.grantedAt,
		ExpiresAt:    l.expiresAt,
		Expired:      l.expired(now),
	}
}

// newConnection allocates a Connection. The id is server-issued: a
// client-supplied connection id would be a bearer token for someone else's
// attachment, which ADR 0004 forbids.
func newConnection(conn *websocket.Conn, opts AttachOptions, sequence uint64, now time.Time) *Connection {
	label := opts.Label
	if label == "" {
		label = defaultConnectionLabel(sequence)
	}
	clientKind := opts.ClientKind
	if clientKind == "" {
		clientKind = "browser"
	}
	return &Connection{
		id:          uuid.NewString(),
		clientID:    opts.ClientID,
		label:       label,
		clientKind:  clientKind,
		attachedAt:  now,
		sequence:    sequence,
		participant: opts.Participant,
		conn:        conn,
		presented:   make(map[string]int64),
	}
}

func defaultConnectionLabel(sequence uint64) string {
	return fmt.Sprintf("client %d", sequence)
}

// errConnectionDetached reports an operation against a connection that has
// already left the surface.
var errConnectionDetached = errors.New("room: connection is no longer attached")
