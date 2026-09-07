package channel

import (
	"encoding/json"
	"time"
)

// Channel is a persistent collaboration context, independent of any runtime
// session (ADR 0006 §3, "Channel"). It is not an agent session, a room, a
// project, or a task.
//
// OwnerScope and ProjectRef are navigation metadata, not isolation
// boundaries — ADR 0006 §3 and internal/authz's AdvisoryPartitionNotice make
// the same call about caller partitions. Nothing in this package enforces
// either as access control.
type Channel struct {
	ID         string
	Title      string
	OwnerScope string
	ProjectRef string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt *time.Time
}

// SubjectType names what a channel subject correlates.
type SubjectType string

const (
	// SubjectInteraction correlates the canonical durable interaction
	// substrate (ADR 0001, internal/interaction).
	SubjectInteraction SubjectType = "interaction"
	// SubjectSurface correlates a surface directly, for a subject that is
	// not itself one interaction.
	SubjectSurface SubjectType = "surface"
	// SubjectArtifact correlates a show-me artifact reference. Tangent does
	// not resolve or authorize the artifact here; ExternalRef is opaque.
	SubjectArtifact SubjectType = "artifact"
	// SubjectFreeform correlates nothing canonical — a discussion with no
	// backing interaction, surface, or artifact.
	SubjectFreeform SubjectType = "freeform"
)

// SubjectStatus is the subject's own lifecycle projection. It is distinct
// from the interaction lifecycle it may correlate: reading never resolves,
// and a subject can be archived without its interaction being terminal.
type SubjectStatus string

const (
	SubjectPending  SubjectStatus = "pending"
	SubjectResolved SubjectStatus = "resolved"
	SubjectArchived SubjectStatus = "archived"
)

// Subject is a thread: a correlation for one discussion, artifact, or
// interaction inside a channel (ADR 0006 §3, "Thread / subject"). It is not
// a task graph or a workflow phase.
//
// A channel may hold several pending subjects at once — unlike a room,
// which admits one pending envelope.
//
// WorkerProvenance is deliberately opaque and carries no foreign key to
// participants: ADR 0006 §5 refuses a worker topology API, so a conductor's
// workers may appear here as provenance and never become channel members or
// recipients automatically.
type Subject struct {
	ID               string
	ChannelID        string
	Type             SubjectType
	InteractionID    string // set only when Type == SubjectInteraction, empty once purged
	SurfaceID        string // set only when Type == SubjectSurface, empty once purged
	ExternalRef      string
	Title            string
	Status           SubjectStatus
	WorkerProvenance json.RawMessage
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReferentPurged reports whether this subject's canonical association has
// been removed by a surface purge (internal/db.PurgeSurface, ADR 0002) since
// the subject was created. It is true only for a SubjectInteraction or
// SubjectSurface subject whose foreign key went NULL through the schema's
// ON DELETE SET NULL — nothing in this package ever clears the field any
// other way, and AddSubject refuses to create one already in this state. The
// row, its type, and its title all survive a purge; only the canonical
// backing is gone.
func (s Subject) ReferentPurged() bool {
	switch s.Type {
	case SubjectInteraction:
		return s.InteractionID == ""
	case SubjectSurface:
		return s.SurfaceID == ""
	default:
		return false
	}
}

// ParticipantKind names the kind of identity a participant reference names.
type ParticipantKind string

const (
	ParticipantOperator ParticipantKind = "operator"
	ParticipantAgent    ParticipantKind = "agent"
)

// OperatorExternalAuthority and OperatorExternalRef are the fixed asserted
// identity OpenChannel resolves its canonical operator participant under.
// They are deliberately stable and shared across every channel, so "one
// participant across channels" (ADR 0006 §3) applies to the operator the
// same way it applies to an agent that re-registers on every launch: there
// is exactly one operator participant, not one per channel.
const (
	OperatorExternalAuthority = "operator"
	OperatorExternalRef       = "local"
)

// Participant is an explicit user-facing communication identity (ADR 0006
// §3, "Participant reference"). External identity remains authoritative:
// ExternalAuthority and ExternalRef are self-asserted by the peer, never
// verified here — the cooperative-loop spike (CW-20260907-0016) found every
// launched session improvised these differently.
type Participant struct {
	ID                string
	Kind              ParticipantKind
	ExternalAuthority string
	ExternalRef       string
	Label             string
	Metadata          json.RawMessage
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Membership is one participant's binding to one channel. A participant may
// hold membership in several channels at once; LeftAt ends membership
// without deleting the row, so RuntimeBinding history keeps its parent.
type Membership struct {
	ChannelID     string
	ParticipantID string
	JoinedAt      time.Time
	LeftAt        *time.Time
}

// RuntimeBinding is the exact external authority, endpoint/session, adapter
// capabilities, and generation a channel participant's destination resolves
// to (ADR 0006 §3, "Runtime binding"). It is not the agent: it is one row of
// an append-only history, and rebinding is always explicit — there is no
// automatic choice of the newest session sharing a name.
//
// Leaving a channel is also a supersession, not just a rebind: see
// Store.RemoveParticipant's doc comment for why "current" and "member" are
// kept consistent by construction rather than by convention.
type RuntimeBinding struct {
	ID                  string
	ChannelID           string
	ParticipantID       string
	Generation          int64
	RuntimeAuthority    string
	RuntimeEndpointRef  string
	AdapterCapabilities json.RawMessage
	BoundAt             time.Time
	SupersededAt        *time.Time
}

// Current reports whether this is the live binding for its (channel,
// participant) pair.
func (b RuntimeBinding) Current() bool { return b.SupersededAt == nil }

// ViewFocus is one view's current focus inside a channel (ADR 0006 §3,
// "View"). ViewRef is an opaque, caller-supplied identity for the window or
// pane; Tangent does not interpret it. Two views on the same channel focus
// independently — there is no single channel-level "current subject".
type ViewFocus struct {
	ChannelID            string
	ViewRef              string
	FocusedSubjectID     string
	FocusedParticipantID string
	UpdatedAt            time.Time
}
