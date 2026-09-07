package relay

import "time"

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
