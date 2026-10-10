package uicommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrForbidden   = errors.New("ui command: verified binding required")
	ErrNotVisible  = errors.New("ui command: no active view")
	ErrTimeout     = errors.New("ui command: acknowledgement timed out")
	ErrStale       = errors.New("ui command: attachment or view changed")
	ErrDisabled    = errors.New("ui command: agent control disabled")
	ErrRateLimited = errors.New("ui command: descriptor rate limited")
)

// Binding is supplied ONLY by a trusted host adapter. SessionRef is an opaque,
// non-credential reference; never put a participant cookie/hash in this struct.
// Labels, resolver leases and wire assertions cannot establish this binding.
type Binding struct {
	ParticipantRef  string
	ConversationRef string
	SessionRef      string
}

func (b Binding) valid() bool {
	return b.ParticipantRef != "" && b.ConversationRef != "" && b.SessionRef != ""
}

type Access string

const (
	Read    Access = "read"
	Control Access = "control"
)

// Authorizer derives the verified participant/conversation/session from the
// calling context and rechecks access on every read or command. No caller-
// supplied targeting parameters are accepted. Nil refuses all agent access.
type Authorizer func(context.Context, Access) (Binding, error)

type Frame struct {
	Type         string          `json:"type"`
	CommandID    string          `json:"command_id"`
	ViewRevision uint64          `json:"view_revision"`
	Name         string          `json:"name"`
	Scope        Scope           `json:"scope"`
	Arguments    json.RawMessage `json:"arguments"`
}
type Ack struct {
	CommandID    string `json:"command_id"`
	ViewRevision uint64 `json:"view_revision"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
}
type Snapshot struct {
	Descriptor     Descriptor `json:"descriptor"`
	Revision       uint64     `json:"revision"`
	ControlEnabled bool       `json:"control_enabled"`
}
type Sender func(context.Context, Frame) error

type attachment struct {
	binding   Binding
	send      Sender
	snapshot  Snapshot
	schemas   map[string]*jsonschema.Schema
	active    uint64
	published time.Time
}
type pending struct {
	attachmentID string
	revision     uint64
	result       chan Ack
}

// Broker keeps ephemeral state only. Attachment IDs must be server-issued
// connection IDs. It never persists descriptors or pushes drafts.
type Broker struct {
	mu          sync.Mutex
	deliveryMu  sync.RWMutex
	authorize   Authorizer
	timeout     time.Duration
	attachments map[string]*attachment
	pending     map[string]pending
	sequence    uint64
}

const (
	MaxAttachments  = 256
	MaxPending      = 64
	PublishInterval = 100 * time.Millisecond
)

func New(authorize Authorizer, timeout time.Duration) *Broker {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &Broker{authorize: authorize, timeout: timeout, attachments: map[string]*attachment{}, pending: map[string]pending{}}
}
func (b *Broker) Attach(id string, binding Binding, send Sender) error {
	if id == "" || !binding.valid() || send == nil {
		return ErrForbidden
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.attachments[id]; exists {
		return errors.New("ui command: duplicate attachment")
	}
	if len(b.attachments) >= MaxAttachments {
		return errors.New("ui command: attachment limit")
	}
	b.attachments[id] = &attachment{binding: binding, send: send}
	return nil
}
func (b *Broker) Detach(id string) {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.attachments, id)
	b.rejectPending(id, "attachment detached")
}
func (b *Broker) rejectPending(id, reason string) {
	for command, p := range b.pending {
		if p.attachmentID == id {
			p.result <- Ack{CommandID: command, ViewRevision: p.revision, Status: "rejected", Reason: reason}
			delete(b.pending, command)
		}
	}
}

// Publish accepts only the resolved attachment's current descriptor. Control
// starts disabled on every attachment, and only participant traffic opts in.
func (b *Broker) Publish(id string, raw []byte) (uint64, error) {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.attachments[id]
	if a == nil {
		return 0, ErrForbidden
	}
	now := time.Now()
	if !a.published.IsZero() && now.Sub(a.published) < PublishInterval {
		return 0, ErrRateLimited
	}
	// Count invalid attempts too; validation is otherwise a CPU amplification.
	a.published = now
	d, schemas, err := ValidateDescriptor(raw)
	if err != nil {
		return 0, err
	}
	b.rejectPending(id, "view changed")
	a.snapshot.Descriptor = d
	a.snapshot.Revision++
	a.schemas = schemas
	return a.snapshot.Revision, nil
}

// Active is explicit foreground/user activity, never a heartbeat or descriptor
// update. Background tabs publishing must not steal command targeting.
func (b *Broker) Active(id string, active bool) error {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.attachments[id]
	if a == nil {
		return ErrForbidden
	}
	if active {
		b.sequence++
		a.active = b.sequence
	} else {
		a.active = 0
		b.rejectPending(id, "view not active")
	}
	return nil
}
func (b *Broker) SetControl(id string, enabled bool) error {
	b.deliveryMu.Lock()
	defer b.deliveryMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.attachments[id]
	if a == nil {
		return ErrForbidden
	}
	a.snapshot.ControlEnabled = enabled
	if !enabled {
		b.rejectPending(id, "agent control disabled")
	}
	return nil
}
func (b *Broker) binding(ctx context.Context, access Access) (Binding, error) {
	if b.authorize == nil {
		return Binding{}, ErrForbidden
	}
	binding, err := b.authorize(ctx, access)
	if err != nil || !binding.valid() {
		return Binding{}, ErrForbidden
	}
	return binding, nil
}
func (b *Broker) latest(binding Binding) (string, *attachment) {
	var id string
	var best *attachment
	for key, a := range b.attachments {
		if a.binding == binding && a.active > 0 && (best == nil || a.active > best.active) {
			id = key
			best = a
		}
	}
	return id, best
}
func (b *Broker) Get(ctx context.Context) (Snapshot, error) {
	binding, err := b.binding(ctx, Read)
	if err != nil {
		return Snapshot{}, err
	}
	b.mu.Lock()
	_, a := b.latest(binding)
	if a == nil || a.snapshot.Revision == 0 {
		b.mu.Unlock()
		return Snapshot{}, ErrNotVisible
	}
	// A caller must not be able to mutate the store through returned maps/slices.
	raw, _ := json.Marshal(a.snapshot)
	b.mu.Unlock()
	var detached Snapshot
	if err := json.Unmarshal(raw, &detached); err != nil {
		return Snapshot{}, err
	}
	if current, err := b.binding(ctx, Read); err != nil || current != binding {
		return Snapshot{}, ErrForbidden
	}
	return detached, nil
}
func (b *Broker) Command(ctx context.Context, name string, args json.RawMessage) (Ack, error) {
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	binding, err := b.binding(ctx, Control)
	if err != nil {
		return Ack{}, err
	}
	if len(args) == 0 || len(args) > MaxArgumentsBytes {
		return Ack{}, errors.New("ui command: invalid argument size")
	}
	var value any
	if decodeErr := decode(args, &value); decodeErr != nil {
		return Ack{}, decodeErr
	}
	waitCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	commandID, p, err := b.startCommand(waitCtx, binding, name, args, value)
	if err != nil {
		if ctx.Err() != nil {
			return Ack{}, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Ack{}, ErrTimeout
		}
		return Ack{}, err
	}
	defer func() { b.mu.Lock(); delete(b.pending, commandID); b.mu.Unlock() }()
	select {
	case ack := <-p.result:
		if current, err := b.binding(ctx, Control); err != nil || current != binding {
			return Ack{}, ErrForbidden
		}
		return ack, nil
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return Ack{}, ctx.Err()
		}
		return Ack{}, ErrTimeout
	}
}

// Serialize participant invalidation against the selected attachment's send.
// Ack handling does not take deliveryMu, so a sender may acknowledge inline.
func (b *Broker) startCommand(ctx context.Context, binding Binding, name string, args json.RawMessage, value any) (string, pending, error) {
	b.deliveryMu.RLock()
	defer b.deliveryMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return "", pending{}, err
	}
	b.mu.Lock()
	id, a := b.latest(binding)
	if a == nil || a.snapshot.Revision == 0 {
		b.mu.Unlock()
		return "", pending{}, ErrNotVisible
	}
	if !a.snapshot.ControlEnabled {
		b.mu.Unlock()
		return "", pending{}, ErrDisabled
	}
	schema := a.schemas[name]
	if schema == nil {
		b.mu.Unlock()
		return "", pending{}, errors.New("ui command: command not declared by current view")
	}
	if err := schema.Validate(value); err != nil {
		b.mu.Unlock()
		return "", pending{}, errors.New("ui command: arguments do not match declared schema")
	}
	if name == "navigate" && !commandRoute(value.(map[string]any)["route"].(string)) {
		b.mu.Unlock()
		return "", pending{}, errors.New("ui command: navigation must be a local route")
	}
	if len(b.pending) >= MaxPending {
		b.mu.Unlock()
		return "", pending{}, errors.New("ui command: pending limit")
	}
	var scope Scope
	for _, c := range a.snapshot.Descriptor.Commands {
		if c.Name == name {
			scope = c.Scope
			break
		}
	}
	frame := Frame{Type: "ui.command", CommandID: uuid.NewString(), ViewRevision: a.snapshot.Revision, Name: name, Scope: scope, Arguments: append(json.RawMessage(nil), args...)}
	p := pending{attachmentID: id, revision: frame.ViewRevision, result: make(chan Ack, 1)}
	b.pending[frame.CommandID] = p
	send := a.send
	b.mu.Unlock()
	if current, err := b.binding(ctx, Control); err != nil || current != binding {
		b.mu.Lock()
		delete(b.pending, frame.CommandID)
		b.mu.Unlock()
		return "", pending{}, ErrForbidden
	}
	// Trusted senders must respect context and must not synchronously mutate
	// attachment state. The room sender is a bounded socket write.
	if err := send(ctx, frame); err != nil {
		b.mu.Lock()
		delete(b.pending, frame.CommandID)
		b.mu.Unlock()
		return "", pending{}, fmt.Errorf("ui command: delivery failed: %w", err)
	}
	return frame.CommandID, p, nil
}

// Acknowledge accepts only a pending command on the exact chosen attachment
// and view revision. Peer tabs, stale views and duplicate acks cannot resolve it.
func (b *Broker) Acknowledge(id string, ack Ack) error {
	if len(ack.Reason) > 512 || (ack.Status != "applied" && ack.Status != "not_visible" && ack.Status != "rejected") {
		return errors.New("ui command: invalid acknowledgement")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[ack.CommandID]
	a := b.attachments[id]
	if !ok || a == nil || p.attachmentID != id || p.revision != ack.ViewRevision || a.snapshot.Revision != p.revision {
		return ErrStale
	}
	p.result <- ack
	delete(b.pending, ack.CommandID)
	return nil
}
