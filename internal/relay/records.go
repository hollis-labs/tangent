package relay

import (
	"time"

	"github.com/hollis-labs/tangent/internal/db"
)

// Exchange is one message plus its frozen recipient binding, thread, and
// correlation ids (ADR 0006 §3, "Exchange"). It is immutable once accepted —
// the schema itself refuses UPDATE and DELETE — so every field here is a
// fact about acceptance time, never a live pointer that drifts.
type Exchange struct {
	ID                     string
	IdempotencyKey         string
	ChannelID              string
	SubjectID              string // empty: this exchange correlates no thread
	SenderParticipantID    string
	RecipientParticipantID string
	// RecipientBindingID is the destination snapshot: the exact
	// channel_participant_bindings row live for the recipient at accept
	// time, frozen. Empty means no live binding existed yet — a legitimate
	// "queued, awaiting-peer" exchange, not an error.
	RecipientBindingID string
	ReplyToExchangeID  string
	Body               string
	// Sequence is the replay cursor: monotonic per
	// RecipientParticipantID. A caller's next receive resumes from
	// `sequence > cursor`.
	Sequence  int64
	CreatedAt time.Time
}

// BodyRedacted reports whether Body currently holds a redaction tombstone
// rather than what the sender wrote — the same computed-from-an-already-
// persisted-fact shape as channel.Subject.ReferentPurged, so a caller can
// tell "this resolved to an existing exchange whose content has since been
// erased" without string-matching the tombstone itself.
func (e Exchange) BodyRedacted() bool {
	return db.IsRedacted(e.Body)
}

// OutboxStatus is exchange_outbox's current delivery-workflow state.
type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "pending"
	OutboxLeased    OutboxStatus = "leased"
	OutboxDelivered OutboxStatus = "delivered"
	OutboxFailed    OutboxStatus = "failed"
)

// OutboxItem is the mutable delivery-workflow row for one exchange. Unlike
// Exchange, every field here can change — that separation (immutable fact,
// mutable workflow state over it) is why outbox and exchange are two tables
// rather than lifecycle columns bolted onto the journal.
type OutboxItem struct {
	ExchangeID     string
	Status         OutboxStatus
	Attempts       int64
	LeasedBy       string
	LeasedAt       *time.Time
	LeaseExpiresAt *time.Time
	NextAttemptAt  *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DeliveryOutcome is what one delivery attempt actually resolved to.
// "Uncertain" is deliberately not "failed": a timeout or a transport error
// with no confirmation either way must never be recorded as a definite
// negative, because that is exactly the fact a caller would use to decide
// whether replaying a non-idempotent command is safe.
type DeliveryOutcome string

const (
	DeliveryDelivered DeliveryOutcome = "delivered"
	DeliveryFailed    DeliveryOutcome = "failed"
	DeliveryUncertain DeliveryOutcome = "uncertain"
)

// DeliveryReceipt is one append-only reconciliation record: what happened
// when Tangent attempted to deliver one exchange, once. It is transport
// evidence, separate from the outbox's current-state pointer and separate
// from whether the recipient ever read the message (ReadReceipt).
type DeliveryReceipt struct {
	ID                 string
	ExchangeID         string
	AttemptNumber      int64
	Outcome            DeliveryOutcome
	RuntimeAuthority   string
	RuntimeEndpointRef string
	ErrorCode          string
	ErrorMessage       string
	AttemptedAt        time.Time
}

// ReadReceipt is the read fact: a participant consumed this exchange.
// Retrieval is not acknowledgement — the same line internal/hitl already
// draws for interactions — so this is recorded only by an explicit ack,
// never implied by a receive.
type ReadReceipt struct {
	ExchangeID    string
	ParticipantID string
	AckedAt       time.Time
}

// Presence is the CW-20260907-0016 spike's item 3, split into its two
// honest halves. Open is process-local and in-memory: true only while a
// Receive call is actually blocked in a bounded wait for this participant
// right now, in this process. It is never persisted and is always false
// immediately after a restart — Tangent cannot know whether anyone
// reconnected across a restart it did not observe, and claiming otherwise
// is exactly the false continuity ADR 0006 §6 rules out ("an agent that is
// not awaiting is, honestly, awaiting-peer"). LastSeenAt is durable
// (participant_presence) and survives a restart: nil means this
// participant has never completed a receive or an ack.
type Presence struct {
	ParticipantID string
	Open          bool
	LastSeenAt    *time.Time
}

// ReceiveParams are the fields a caller supplies to receive(cursor,
// wait_ms) for one destination in one channel.
type ReceiveParams struct {
	ChannelID     string
	ParticipantID string
	Cursor        int64
	// Wait is the bounded long-poll duration. Zero returns immediately with
	// whatever is already new. Capped at MaximumReceiveWait.
	Wait  time.Duration
	Limit int
	// UnackedOnly excludes anything already acked by ParticipantID. See
	// Store.ListForDestination's doc comment for why a relaunched, cursor-
	// less session needs this rather than cursor=0 alone.
	UnackedOnly bool
}

// ReceiveResult is one receive call's answer. TimedOut is never an error
// and never cancels anything — Receive only ever reads; nothing about the
// outbox or a delivery claim is touched by it — so a caller resumes with
// NextCursor exactly as if nothing had happened.
type ReceiveResult struct {
	Items      []Exchange
	NextCursor int64
	TimedOut   bool
	Presence   Presence
}
