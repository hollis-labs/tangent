package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
)

// Envelope constants for approval.* events. Each is stamped by Tangent from its
// own records and never read from a payload.
const (
	// App is the producer name.
	App = "tangent"
	// SubjectType is the outbox subject_type of every approval event.
	SubjectType = "approval"
	// RetentionClass is the contract retention class for approval events
	// (400 days, no tombstone). The durable log applies it; it is not an
	// envelope field.
	RetentionClass = "R-audit"
	// Visibility is the class.visibility of approval events. D9 (operator
	// visibility for approval detail) is still an owner call, so it lives in
	// this one constant.
	Visibility = "operator"
	// ActorVerified states how the actor was identified. Approvals are not a
	// security boundary yet (CW-20261016-0174): the actor is whoever held the
	// operator session or loopback caller, and nothing may gate on these events.
	ActorVerified = "operator-session"

	payloadSchemaRequested = "approval.requested@1"
	payloadSchemaResolved  = "approval.resolved@1"
)

// Approval outcomes carried by approval.resolved.
const (
	OutcomeApproved  = "approved"
	OutcomeDenied    = "denied"
	OutcomeWithdrawn = "withdrawn"
	OutcomeExpired   = "expired"
	OutcomeFailed    = "failed"
)

// Principal is a requester or resolver. Kind and ID go into payloads (opaque
// identifiers, never display names); URN and Role fill the envelope actor.
type Principal struct {
	Kind string // operator, agent or service
	ID   string
	URN  string
	Role string // operator or system
}

// Approval describes one approval lifecycle transition.
type Approval struct {
	Kind      string // catalog.KindApprovalRequested or catalog.KindApprovalResolved
	ItemID    string
	Class     string // the approval class, from the request's own policy fields
	Requester Principal
	ExpiresAt string // RFC 3339, requested only
	Outcome   string // resolved only
	Resolver  Principal
	At        time.Time

	// Extra holds further candidate payload keys. They pass through the same
	// allowlist as the typed fields, so a key outside it is dropped and counted.
	Extra map[string]any
}

// Tx is the slice of *sql.Tx the emitter needs. It must be the producer's open
// transaction so the event row commits with the journal row.
type Tx interface {
	Execer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var _ Tx = (*sql.Tx)(nil)

// Emitter builds approval envelopes. The zero value is usable.
type Emitter struct {
	// Host stamps scope.host. It defaults to the OS host name.
	Host string
}

// HitlRef is the opaque reference a consumer resolves through hitl_get under
// its existing auth. It is also the event subject.
func HitlRef(itemID string) string { return "msg://approval/tangent/" + itemID }

// Emit builds the envelope for a and inserts its outbox row in tx. A failure
// must fail the caller's transaction so the transition and its event stay
// atomic.
func (e Emitter) Emit(ctx context.Context, tx Tx, a Approval) (Row, error) {
	row, err := e.Build(ctx, tx, a)
	if err != nil {
		return Row{}, err
	}
	if _, err = Insert(ctx, tx, row); err != nil {
		return Row{}, err
	}
	return row, nil
}

// Build mints the evt_ id, sanitizes the payload, stamps the blocks and
// validates the envelope. The sequence is the next outbox rowid.
func (e Emitter) Build(ctx context.Context, tx Tx, a Approval) (Row, error) {
	schema := payloadSchemaRequested
	switch a.Kind {
	case catalog.KindApprovalRequested:
	case catalog.KindApprovalResolved:
		schema = payloadSchemaResolved
	default:
		return Row{}, fmt.Errorf("events: %q is not an approval kind", a.Kind)
	}
	if a.ItemID == "" {
		return Row{}, fmt.Errorf("events: approval event needs an item id")
	}
	at := a.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	candidate := map[string]any{"request_id": a.ItemID, "hitl_ref": HitlRef(a.ItemID)}
	actor := a.Requester
	severity := "info"
	if a.Kind == catalog.KindApprovalRequested {
		setIf(candidate, "class", a.Class)
		setIf(candidate, "requester", a.Requester.ID)
		setIf(candidate, "expires_at", a.ExpiresAt)
	} else {
		actor = a.Resolver
		candidate["outcome"] = a.Outcome
		if a.Resolver.ID != "" || a.Resolver.Kind != "" {
			candidate["resolver"] = map[string]any{"kind": a.Resolver.Kind, "id": a.Resolver.ID}
		}
		if a.Outcome == OutcomeExpired || a.Outcome == OutcomeFailed {
			severity = "warning"
		}
	}
	for k, v := range a.Extra {
		candidate[k] = v
	}
	payload, dropped, sanErr := Sanitize(a.Kind, candidate)

	var seq uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid), 0) + 1 FROM event_outbox`).Scan(&seq); err != nil {
		return Row{}, fmt.Errorf("events: next sequence: %w", err)
	}
	host := e.Host
	if host == "" {
		host, _ = os.Hostname()
	}
	id := runtimeevents.NewEventID()
	ev := runtimeevents.Event{
		SchemaVersion: runtimeevents.SchemaVersion,
		ID:            id,
		Kind:          runtimeevents.EventKind(a.Kind),
		Time:          at,
		App:           App,
		Sequence:      seq,
		Generation:    1,
		Source:        runtimeevents.Source{Channel: runtimeevents.ChannelHook, Confidence: runtimeevents.Confidence("exact")},
		Subject:       HitlRef(a.ItemID),
		Payload:       payload,
		ContentType:   "application/json",
		Scope:         &runtimeevents.Scope{Host: host},
		Corr:          &runtimeevents.Corr{CorrelationID: a.ItemID},
		Class: &runtimeevents.Class{
			Category: "approval", Severity: severity, Sensitivity: "private", Visibility: Visibility,
			PayloadSchema: schema, Sanitized: sanErr == "", Redactions: redactions(dropped),
		},
	}
	if actor.URN != "" && actor.Kind != "" {
		ev.Actor = &runtimeevents.Actor{URN: actor.URN, Kind: actor.Kind, Role: actor.Role, Verified: ActorVerified}
	}
	if err := ev.Validate(); err != nil {
		return Row{}, err
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return Row{}, fmt.Errorf("events: marshal envelope: %w", err)
	}
	return Row{
		EventID: id, Stream: DefaultStream, Kind: a.Kind, SubjectType: SubjectType, SubjectID: a.ItemID,
		EnvelopeJSON: raw, CreatedAt: at,
	}, nil
}

// redactions clamps the dropped-key count into the envelope's uint32 counter.
func redactions(dropped int) uint32 {
	if dropped < 0 {
		return 0
	}
	if dropped > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(dropped) //nolint:gosec // clamped above
}

func setIf(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// --- sanitize ---------------------------------------------------------------

const (
	maxIDLen   = 128
	maxTimeLen = 40
)

var (
	classVocab    = map[string]bool{"approval": true}
	outcomeVocab  = map[string]bool{OutcomeApproved: true, OutcomeDenied: true, OutcomeWithdrawn: true, OutcomeExpired: true, OutcomeFailed: true}
	principalKind = map[string]bool{"operator": true, "agent": true, "service": true}
)

type field struct {
	required bool
	clean    func(any) (any, string)
}

// allowlists are per kind. A key outside the list is dropped and counted; a
// value that breaks its bounds drops the whole payload to {"sanitize_error"}.
var allowlists = map[string]map[string]field{
	catalog.KindApprovalRequested: {
		"request_id": {true, boundedString(maxIDLen, nil)},
		"hitl_ref":   {true, boundedString(maxIDLen+32, nil)},
		"class":      {false, boundedString(maxIDLen, classVocab)},
		"requester":  {false, boundedString(maxIDLen, nil)},
		"subject":    {false, boundedString(maxIDLen+32, nil)},
		"expires_at": {false, timestamp},
	},
	catalog.KindApprovalResolved: {
		"request_id": {true, boundedString(maxIDLen, nil)},
		"hitl_ref":   {true, boundedString(maxIDLen+32, nil)},
		"outcome":    {true, boundedString(maxIDLen, outcomeVocab)},
		"resolver":   {false, resolver},
	},
}

// Sanitize reduces candidate to the kind's allowlist. It returns the payload
// JSON, how many keys it dropped, and a non-empty class when it failed closed
// (the payload is then {"sanitize_error": class} and nothing else).
func Sanitize(kind string, candidate map[string]any) (json.RawMessage, int, string) {
	allow, ok := allowlists[kind]
	if !ok {
		return failClosed("kind"), 0, "kind"
	}
	out := make(map[string]any, len(allow))
	dropped := 0
	for key := range candidate {
		if _, listed := allow[key]; !listed {
			dropped++
		}
	}
	for key, value := range candidate {
		f, listed := allow[key]
		if !listed {
			continue
		}
		cleaned, bad := f.clean(value)
		if bad != "" {
			return failClosed(bad), dropped, bad
		}
		out[key] = cleaned
	}
	for key, f := range allow {
		if _, present := out[key]; f.required && !present {
			return failClosed("missing"), dropped, "missing"
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return failClosed("marshal"), dropped, "marshal"
	}
	return raw, dropped, ""
}

func failClosed(class string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"sanitize_error": class})
	return raw
}

func boundedString(limit int, vocab map[string]bool) func(any) (any, string) {
	return func(v any) (any, string) {
		s, ok := v.(string)
		switch {
		case !ok:
			return nil, "opaque"
		case s == "":
			return nil, "missing"
		case len(s) > limit:
			return nil, "oversized"
		case !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }):
			return nil, "opaque"
		case vocab != nil && !vocab[s]:
			return nil, "vocabulary"
		}
		return s, ""
	}
}

func timestamp(v any) (any, string) {
	s, bad := boundedString(maxTimeLen, nil)(v)
	if bad != "" {
		return nil, bad
	}
	if _, err := time.Parse(time.RFC3339, s.(string)); err != nil {
		return nil, "opaque"
	}
	return s, ""
}

func resolver(v any) (any, string) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, "opaque"
	}
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "kind":
			cleaned, bad := boundedString(maxIDLen, principalKind)(value)
			if bad != "" {
				return nil, bad
			}
			out[key] = cleaned
		case "id":
			cleaned, bad := boundedString(maxIDLen, nil)(value)
			if bad != "" {
				return nil, bad
			}
			out[key] = cleaned
		default:
			return nil, "opaque" // an unexpected nested key is not silently trusted
		}
	}
	if out["kind"] == nil || out["id"] == nil {
		return nil, "missing"
	}
	return out, ""
}
