package effect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/tangent/internal/authz"
)

// Principal is the acting participant, projected from a resolved session.
//
// It carries a scope and a capability set and deliberately not a session id:
// the session is the only capability material in the system and never reaches
// a decision input (ADR 0004 §6.1). This is the same shape
// `participant.Gate.ResolveBinding` already produces for the WebSocket.
type Principal struct {
	Kind    authz.PrincipalKind
	Scope   string
	Ref     string
	Granted []authz.Capability
}

// Binding is what the definition registry says about the interaction's
// renderer.
//
// Both halves matter and neither is a substitute for the other. Required is
// what the publisher declared in the manifest; Granted is the intersection
// Tangent's host policy produced at materialization (ADR 0003 §2.5). A
// capability in neither is *undeclared* — the renderer is asking for something
// its own definition never claimed. A capability in Required but not Granted
// is *denied* — the publisher asked and this host said no. Those are different
// facts about the world and a single "not allowed" would erase the difference
// exactly where an operator needs it.
//
// Both are copied from the materialized binding by the caller. Neither is ever
// read from the request.
//
// **The manifest's `Capability.scope` object is not carried here and is not
// consumed anywhere.** ADR 0003 §2.5 describes it as "allowed roots, allowed
// origins, byte ceilings", and this model answers two of those three from the
// host instead: roots come from an [Authority] registration, and byte ceilings
// come from the [Root]. That is the stronger arrangement — which directories a
// renderer may reach is the host's answer, not the publisher's — but it means a
// publisher-authored `scope` is currently descriptive rather than enforced, and
// allowed-origins has no enforcement at all. Recorded as an amendment against
// ADR 0003 §2.5 rather than silently reinterpreted.
type Binding struct {
	Kind          string
	Version       string
	BindingDigest string
	Required      []Capability
	Granted       []Capability
}

// Intent is the participant's evidence that they asked for this.
//
// ADR 0001 keeps a participant's act the authoritative one, and an effect with
// no intent is the host acting on a renderer's initiative. Requiring it is
// what makes "selection never implies read" true in the direction that
// matters: a handle can exist for the whole life of an interaction and cause
// nothing until a person presses something.
type Intent struct {
	// ControlID names the participant control that produced the request. It
	// is a renderer-authored label, recorded and never dispatched on — an
	// action identifier is not an effect (acceptance criterion 2).
	ControlID string
	// PresentedRevision pins the projection revision the participant was
	// looking at.
	PresentedRevision int64
	// ConfirmedAt is when they confirmed. A zero time is no intent.
	ConfirmedAt time.Time
}

// present reports whether intent was actually expressed.
func (i Intent) present() bool {
	return strings.TrimSpace(i.ControlID) != "" && !i.ConfirmedAt.IsZero()
}

// Params carries the capability-specific arguments.
//
// It is a small closed struct rather than a `map[string]any` on purpose: a
// free-form parameter bag is how a path string comes back in through a
// different door.
type Params struct {
	// MaxBytes caps a read. Clamped by the root's own ceiling; zero takes it.
	MaxBytes int64
	// Content is what a write puts on disk.
	Content []byte
	// MediaType labels the content of an export or a write.
	MediaType string
	// Origin is the external origin a `network.fetch` would reach.
	//
	// It is recorded on the request fingerprint and never dereferenced here.
	// It is deliberately *not* matched against the manifest's
	// `Capability.scope` allowed-origins list, because this package does not
	// consume that field at all — see the note on Binding.
	Origin string
}

// Request is one effect request. Every field is assembled by the host from
// evidence — the session, the pinned binding, the stored handle — except
// Intent and Params, which are the renderer's and are treated as such.
type Request struct {
	Capability     Capability
	Principal      Principal
	Binding        Binding
	OwnerScope     string
	InteractionID  string
	HandleID       string
	IdempotencyKey string
	Intent         Intent
	Params         Params
}

// Performer executes a capability this broker does not implement itself.
//
// `file.read_scoped` and `file.write_scoped` are native, because scoping them
// *is* the root machinery in this package. Everything else — an evidence
// preview through a registered adapter, an export the host generates — is
// someone else's code, and it runs only after [Broker.Request] has admitted
// it. A performer is never consulted for a refused request.
type Performer interface {
	Perform(ctx context.Context, request Request, handle Handle) (content []byte, mediaType string, err error)
}

// Store is the durable side of handles and receipts.
type Store interface {
	InsertHandle(ctx context.Context, handle Handle) error
	LookupHandle(ctx context.Context, id string) (Handle, error)
	ConsumeHandleUse(ctx context.Context, id string) error
	RecordReceipt(ctx context.Context, receipt Receipt, requestDigest string) error
	ReceiptForKey(ctx context.Context, key string, capability Capability) (Receipt, string, bool, error)
}

// Broker is the single gate every host-mediated effect passes through.
//
// One gate, not one per transport: ADR 0004 put every object-access refusal
// through `authz.Authorize` for the same reason, and an effect model with two
// entry points has none.
type Broker struct {
	store      Store
	authority  Authority
	performers map[Capability]Performer
	now        func() time.Time
}

// BrokerOption customizes a Broker.
type BrokerOption func(*Broker)

// WithPerformer registers the executor for one capability. Registering one for
// a native capability is a wiring error and is refused at construction.
func WithPerformer(capability Capability, performer Performer) BrokerOption {
	return func(b *Broker) {
		if performer != nil {
			b.performers[capability] = performer
		}
	}
}

// WithClock makes decisions deterministic under test.
func WithClock(now func() time.Time) BrokerOption {
	return func(b *Broker) {
		if now != nil {
			b.now = now
		}
	}
}

// NewBroker constructs a broker. A nil authority is [Standalone], which
// registers no root and grants nothing — the shipped configuration.
func NewBroker(store Store, authority Authority, options ...BrokerOption) (*Broker, error) {
	if store == nil {
		return nil, errors.New("effect: store is required")
	}
	if authority == nil {
		authority = Standalone()
	}
	broker := &Broker{
		store:      store,
		authority:  authority,
		performers: map[Capability]Performer{},
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		if option != nil {
			option(broker)
		}
	}
	for capability := range broker.performers {
		if native(capability) {
			return nil, fmt.Errorf("effect: %s is performed natively and takes no performer", capability)
		}
	}
	return broker, nil
}

// native reports whether the broker performs a capability itself.
func native(capability Capability) bool {
	return capability == FileReadScoped || capability == FileWriteScoped
}

// scoped reports whether a capability must name a handle.
//
// The ones that need not are those with no host-side target to scope: a
// clipboard write names arbitrary text the renderer already holds, and a fetch
// names an origin rather than an artifact. `export.download` *is* scoped even
// though the browser performs it, because an export names a specific artifact
// the host generated and a use budget is the only thing that makes
// "downloadable once" mean anything.
//
// An unscoped capability is not a loophole — it is [MediationDeclared] visible
// from a second angle, and it is why those are audited rather than enforced.
func scoped(capability Capability) bool {
	switch capability {
	case FileReadScoped, FileWriteScoped, EvidencePreview, ExportDownload:
		return true
	default:
		return false
	}
}

// Mint issues a handle. Every field comes from the host; there is no path in
// this signature a renderer could have supplied.
//
// Minting deliberately does not check the target exists. A handle is a scope,
// not a promise: checking at mint time would be a stat whose answer is stale
// by the time anyone uses it, and would leak existence to whoever can cause a
// mint. Existence is settled at [Broker.Request], atomically, by the open.
func (b *Broker) Mint(ctx context.Context, mint Mint) (Handle, View, error) {
	relative := mint.RelativePath
	if mint.Class == ClassWorkspaceFile || mint.Class == ClassWorkspaceDir {
		normalized, err := NormalizeRelative(mint.RelativePath)
		if err != nil {
			return Handle{}, View{}, err
		}
		relative = normalized
		roots, rootErr := b.rootSet(ctx, mint.ParticipantScope)
		if rootErr != nil {
			return Handle{}, View{}, rootErr
		}
		if _, ok := roots.Lookup(mint.RootID); !ok {
			return Handle{}, View{}, ErrRootUnmediated
		}
	}
	capabilities := ParseCapabilities(FormatCapabilities(mint.Capabilities))
	if len(capabilities) == 0 {
		return Handle{}, View{}, errors.New("effect: a handle that covers no capability is not a handle")
	}
	lifetime := mint.Lifetime
	if lifetime <= 0 {
		lifetime = DefaultHandleLifetime
	}
	now := b.now()
	handle := Handle{
		ID:               "eh_" + uuid.NewString(),
		Class:            mint.Class,
		RootID:           strings.TrimSpace(mint.RootID),
		RelativePath:     relative,
		InteractionID:    strings.TrimSpace(mint.InteractionID),
		ParticipantScope: strings.TrimSpace(mint.ParticipantScope),
		BindingDigest:    strings.TrimSpace(mint.BindingDigest),
		Capabilities:     capabilities,
		IssuedAt:         now,
		ExpiresAt:        now.Add(lifetime),
		MaxUses:          mint.MaxUses,
	}
	if handle.InteractionID == "" || handle.ParticipantScope == "" {
		return Handle{}, View{}, errors.New("effect: a handle must name an interaction and a participant scope")
	}
	if err := b.store.InsertHandle(ctx, handle); err != nil {
		return Handle{}, View{}, err
	}
	return handle, View{
		HandleID:  handle.ID,
		Class:     handle.Class,
		Label:     mint.Label,
		MediaType: mint.MediaType,
		SizeBytes: mint.SizeBytes,
		ExpiresAt: handle.ExpiresAt,
	}, nil
}

// Request evaluates one effect request and, if it is admitted, performs it.
//
// The order is the decision, exactly as it is in `authz.Authorize`, and the
// cheapest-and-least-disclosing checks come first:
//
//  1. **Well-formedness.** An unknown capability id is refused before
//     anything else looks at it, so a renderer cannot probe the namespace.
//  2. **Replay.** An idempotency key already used returns its stored receipt
//     rather than acting twice.
//  3. **Object access.** `authz.Authorize` against the capability's
//     [ObjectPrecondition]. This is the other namespace's question, asked
//     first, because a principal who may not even *view* the object has no
//     business having its definition's grants examined.
//  4. **Declaration, then grant.** The manifest must have asked, and host
//     policy must have said yes. Two codes, because they are two facts.
//  5. **Mediation.** A capability with no executor is refused here rather
//     than discovered missing later.
//  6. **Intent.** No participant act, no effect.
//  7. **Handle.** Live, in-realm, on this interaction, under this binding, and
//     covering this capability.
//  8. **Perform**, then consume a use, then write the receipt.
//
// A refusal at any step still writes a receipt. An audit trail that records
// only what succeeded cannot answer the question anyone actually asks it.
func (b *Broker) Request(ctx context.Context, request Request) (Result, error) {
	now := b.now()
	key := strings.TrimSpace(request.IdempotencyKey)
	digest := requestDigest(request)

	refuse := func(code string) (Result, error) {
		receipt := b.receipt(request, now, DecisionRefused, code)
		if err := b.store.RecordReceipt(ctx, receipt, digest); err != nil {
			return Result{}, err
		}
		return Result{Receipt: receipt}, nil
	}

	if !Known(request.Capability) || key == "" || strings.TrimSpace(request.InteractionID) == "" {
		// Nothing is recorded for a malformed request: with no capability id
		// and no idempotency key there is no row shape to record, and writing
		// one anyway would let an unauthenticated shape flood the audit table.
		return Result{Receipt: Receipt{
			Capability: request.Capability,
			Decision:   DecisionRefused,
			Code:       CodeInvalidRequest,
			Mediation:  MediationOf(request.Capability),
			IssuedAt:   now,
		}}, nil
	}

	// 2. Replay.
	//
	// A replayed request returns the stored receipt and no content, whether
	// the original was granted or refused. That is the honest meaning of an
	// idempotency key on an effect: "this already happened, here is what
	// happened" — not "do it again and hope it matches". A renderer that lost
	// a read's response and genuinely wants the bytes asks again under a new
	// key, which is safe precisely because a read is repeatable; a write does
	// not get that option, which is the point.
	if stored, storedDigest, found, err := b.store.ReceiptForKey(ctx, key, request.Capability); err != nil {
		return Result{}, err
	} else if found {
		if storedDigest != digest {
			// Deliberately not recorded. The row already written under this
			// key *is* the record for it, and a second row would violate the
			// uniqueness that makes the key idempotent in the first place. The
			// refusal is returned, not persisted.
			receipt := b.receipt(request, now, DecisionRefused, CodeIdempotencyConflict)
			return Result{Receipt: receipt}, nil
		}
		stored.Replayed = true
		return Result{Receipt: stored}, nil
	}

	// 3. Object access — the other namespace, asked first.
	if err := authz.Authorize(authz.Request{
		Kind:       request.Principal.Kind,
		Scope:      request.Principal.Scope,
		Granted:    request.Principal.Granted,
		OwnerScope: request.OwnerScope,
		Capability: ObjectPrecondition(request.Capability),
	}); err != nil {
		return refuse(CodeNotAuthorized)
	}

	// 4. Declaration, then grant.
	if !Holds(request.Binding.Required, request.Capability) {
		return refuse(CodeCapabilityUndeclared)
	}
	if !Holds(request.Binding.Granted, request.Capability) {
		return refuse(CodeCapabilityDenied)
	}

	// 5. Mediation.
	mediation := MediationOf(request.Capability)
	if mediation == MediationUnimplemented {
		return refuse(CodeUnavailable)
	}
	if !native(request.Capability) && b.performers[request.Capability] == nil && mediation == MediationHost {
		return refuse(CodeUnavailable)
	}

	// 6. Intent.
	if !request.Intent.present() {
		return refuse(CodeIntentMissing)
	}

	// 7. Handle.
	var handle Handle
	if scoped(request.Capability) {
		resolved, err := b.resolveHandle(ctx, request, now)
		if err != nil {
			return refuse(refusalFor(err))
		}
		handle = resolved
	} else if strings.TrimSpace(request.HandleID) != "" {
		// An unscoped capability that names a handle is a confused request,
		// not a permissive one.
		return refuse(CodeInvalidRequest)
	}

	// 8. Perform.
	content, mediaType, err := b.perform(ctx, request, handle)
	if err != nil {
		return refuse(refusalFor(err))
	}
	if handle.ID != "" && handle.MaxUses > 0 {
		if consumeErr := b.store.ConsumeHandleUse(ctx, handle.ID); consumeErr != nil {
			return refuse(refusalFor(consumeErr))
		}
	}

	receipt := b.receipt(request, now, DecisionGranted, "")
	receipt.Bytes = int64(len(content))
	if len(content) > 0 {
		sum := sha256.Sum256(content)
		receipt.ContentSHA256 = hex.EncodeToString(sum[:])
	}
	if mediaType != "" {
		receipt.MediaType = mediaType
	}
	if err := b.store.RecordReceipt(ctx, receipt, digest); err != nil {
		return Result{}, err
	}
	return Result{Receipt: receipt, Content: content}, nil
}

// resolveHandle loads and validates the handle a scoped request names.
//
// Every mismatch — wrong realm, wrong interaction, wrong binding, capability
// not covered — returns [ErrHandleUnknown], the same error a nonexistent
// handle returns. A refusal that distinguishes them is an enumeration oracle.
func (b *Broker) resolveHandle(ctx context.Context, request Request, now time.Time) (Handle, error) {
	id := strings.TrimSpace(request.HandleID)
	if id == "" {
		return Handle{}, ErrHandleUnknown
	}
	handle, err := b.store.LookupHandle(ctx, id)
	if err != nil {
		return Handle{}, err
	}
	if !authz.SameAuthority(handle.ParticipantScope, request.Principal.Scope) {
		return Handle{}, ErrHandleUnknown
	}
	if handle.InteractionID != strings.TrimSpace(request.InteractionID) {
		return Handle{}, ErrHandleUnknown
	}
	if handle.BindingDigest != "" && handle.BindingDigest != request.Binding.BindingDigest {
		// The definition was re-materialized under a different grant since
		// this handle was minted. Refusing rather than re-checking is the
		// fail-closed reading of ADR 0003's pin: a handle is scoped to the
		// binding that admitted it, and a new binding mints new handles.
		return Handle{}, ErrHandleUnknown
	}
	if !handle.Covers(request.Capability) {
		return Handle{}, ErrHandleUnknown
	}
	// Expiry and budget come last so a stale handle belonging to someone else
	// still reports `unknown` rather than `expired`.
	if err := handle.Live(now); err != nil {
		return Handle{}, err
	}
	return handle, nil
}

// perform executes an admitted request.
func (b *Broker) perform(ctx context.Context, request Request, handle Handle) ([]byte, string, error) {
	switch request.Capability {
	case FileReadScoped:
		roots, err := b.rootSet(ctx, request.Principal.Scope)
		if err != nil {
			return nil, "", err
		}
		root, ok := roots.Lookup(handle.RootID)
		if !ok {
			return nil, "", ErrRootUnmediated
		}
		limit := root.MaxReadBytes
		if request.Params.MaxBytes > 0 && request.Params.MaxBytes < limit {
			limit = request.Params.MaxBytes
		}
		content, readErr := ReadInRoot(root.Path, handle.RelativePath, limit)
		if readErr != nil {
			return nil, "", readErr
		}
		return content, request.Params.MediaType, nil

	case FileWriteScoped:
		roots, err := b.rootSet(ctx, request.Principal.Scope)
		if err != nil {
			return nil, "", err
		}
		root, ok := roots.Lookup(handle.RootID)
		if !ok {
			return nil, "", ErrRootUnmediated
		}
		if !root.Writable {
			return nil, "", ErrRootUnmediated
		}
		if int64(len(request.Params.Content)) > root.MaxReadBytes {
			return nil, "", ErrTooLarge
		}
		if err := writeInRoot(root.Path, handle.RelativePath, request.Params.Content); err != nil {
			return nil, "", err
		}
		return nil, request.Params.MediaType, nil

	default:
		if performer := b.performers[request.Capability]; performer != nil {
			return performer.Perform(ctx, request, handle)
		}
		// A MediationDeclared capability with no performer is admitted and
		// performed by the browser. The receipt records the admission; it must
		// never be read as a record that the host did anything.
		return nil, request.Params.MediaType, nil
	}
}

// rootSet asks the authority which roots this participant may reach.
func (b *Broker) rootSet(ctx context.Context, participantScope string) (RootSet, error) {
	roots, err := b.authority.WorkspaceRoots(ctx, participantScope)
	if err != nil {
		return RootSet{}, ErrRootUnmediated
	}
	return NewRootSet(roots), nil
}

// receipt builds the row for one decision.
func (b *Broker) receipt(request Request, now time.Time, decision Decision, code string) Receipt {
	return Receipt{
		ID:               "er_" + uuid.NewString(),
		Capability:       request.Capability,
		Decision:         decision,
		Code:             code,
		Mediation:        MediationOf(request.Capability),
		HandleID:         strings.TrimSpace(request.HandleID),
		InteractionID:    strings.TrimSpace(request.InteractionID),
		ParticipantScope: authz.Normalize(request.Principal.Scope),
		BindingDigest:    request.Binding.BindingDigest,
		IdempotencyKey:   strings.TrimSpace(request.IdempotencyKey),
		IntentControlID:  strings.TrimSpace(request.Intent.ControlID),
		IntentRevision:   request.Intent.PresentedRevision,
		IssuedAt:         now,
	}
}

// requestDigest fingerprints the parts of a request that must not change under
// one idempotency key.
//
// It covers the content by digest rather than by value, so the fingerprint of
// a write is stable without the audit path ever holding the payload.
func requestDigest(request Request) string {
	parts := []string{
		string(request.Capability),
		authz.Normalize(request.Principal.Scope),
		strings.TrimSpace(request.InteractionID),
		strings.TrimSpace(request.HandleID),
		request.Binding.BindingDigest,
		strconv.FormatInt(request.Params.MaxBytes, 10),
		request.Params.MediaType,
		request.Params.Origin,
	}
	sum := sha256.Sum256(request.Params.Content)
	parts = append(parts, hex.EncodeToString(sum[:]))
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(digest[:])
}
