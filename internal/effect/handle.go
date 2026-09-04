package effect

import (
	"errors"
	"time"
)

// HandleClass names what kind of thing a handle points at. It is part of the
// handle rather than inferred from the path, so a directory handle can never
// be presented where a file handle is required.
type HandleClass string

const (
	// ClassWorkspaceFile is one file inside a host-registered workspace root.
	ClassWorkspaceFile HandleClass = "workspace_file"
	// ClassWorkspaceDir is one directory inside a host-registered workspace
	// root. It scopes a listing, never a read.
	ClassWorkspaceDir HandleClass = "workspace_dir"
	// ClassEvidence is one durable evidence entry on one inbox item. Its
	// "path" is an item id and an index, not a filesystem location.
	ClassEvidence HandleClass = "evidence"
	// ClassExport is one host-generated export the participant may download
	// exactly as many times as its use budget allows.
	ClassExport HandleClass = "export"
)

// Handle is a scoped, expiring, use-counted reference that replaces a path
// string or an action identifier as the way a renderer names the target of an
// effect.
//
// Three properties are the whole point:
//
//  1. **A handle is minted, never asserted.** Only the host creates one, from
//     a root an authority registered. A renderer that invents an id, or that
//     sends a path where an id belongs, reaches nothing.
//
//  2. **A handle is a locator, not a credential.** This is the same shape ADR
//     0004 §5 chose for room URLs, and it is what keeps ADR 0002's rule — no
//     short-lived grant in a renderer payload or browser storage — satisfiable
//     while handles still travel in envelopes. Authority to *use* a handle
//     comes from the participant session cookie plus the interaction's
//     definition binding, both re-checked on every request. An id that leaks
//     into a transcript grants its reader nothing, because the reader has
//     neither.
//
//  3. **A handle carries no authority of its own.** It narrows an effect the
//     principal could already have requested; it never widens one. Selecting a
//     file mints a handle, and a handle is not permission to read — the read
//     is a separate mediated request that the same policy can refuse.
//
// The struct is host-side. [View] is what a renderer is allowed to see.
type Handle struct {
	// ID is the opaque locator. It carries no path material, so a leaked id
	// does not disclose where the file is, only that a handle existed.
	ID string
	// Class constrains what the handle may be used for.
	Class HandleClass
	// RootID names the host-registered workspace root. It is a key into the
	// authority's root set, never a path.
	RootID string
	// RelativePath is the already-normalized, root-relative path. It exists
	// host-side only: it is never returned to a renderer and never accepted
	// from one. ADR 0002 §8 keeps it out of every log line.
	RelativePath string
	// InteractionID is the object this handle is attached to. A handle minted
	// for one interaction is not usable on another, so an id that escapes one
	// room does not become an effect in the next.
	InteractionID string
	// ParticipantScope is the realm the minting session belonged to. It is a
	// scope, not a session id: the session is capability material and never
	// leaves the session store (ADR 0004 §6.1).
	ParticipantScope string
	// BindingDigest pins the exact definition binding whose granted
	// capabilities admitted this handle. Re-materializing a definition with a
	// narrower grant invalidates every handle minted under the old one.
	BindingDigest string
	// Capabilities is exactly what this handle may be used for. It is the
	// intersection the mint applied, not the definition's whole grant.
	Capabilities []Capability
	// IssuedAt and ExpiresAt bound the grant in time.
	IssuedAt  time.Time
	ExpiresAt time.Time
	// MaxUses is the granted-use budget. Zero means unlimited within the
	// expiry window; a positive value is consumed on every *granted* request
	// and never on a refused one, so a denial cannot burn a participant's
	// budget.
	MaxUses int
	// Used is how much of the budget is spent.
	Used int
	// RevokedAt is set when a handle was withdrawn before its expiry.
	RevokedAt time.Time
}

// View is the renderer-visible projection of a handle.
//
// It is deliberately not the handle. A renderer receives an id, a class, a
// display label, and the expiry it needs in order to stop offering a stale
// control — and no root id, no path, no capability set, and no binding digest.
// A renderer that knows what it is allowed to do is a renderer that will try;
// the host answers that question, once, at request time.
type View struct {
	HandleID  string      `json:"handle_id"`
	Class     HandleClass `json:"class"`
	Label     string      `json:"label,omitempty"`
	MediaType string      `json:"media_type,omitempty"`
	SizeBytes int64       `json:"size_bytes,omitempty"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// DefaultHandleLifetime is how long a minted handle stays usable.
//
// Fifteen minutes is chosen against the participant's working rhythm, not
// against an attacker's: a person who picked a file and then read the diff
// before previewing it must not find the preview button dead, and a handle
// left in a tab overnight must not still be live in the morning. It is
// deliberately far shorter than the 30-day participant session, because the
// session is the authority and the handle is only the scope.
const DefaultHandleLifetime = 15 * time.Minute

// Errors that describe a handle rather than a request. Each maps to exactly
// one refusal code in [refusalFor], so a transport never invents its own.
var (
	// ErrHandleUnknown means no live handle has that id. It is also what a
	// handle belonging to another participant realm or another interaction
	// returns: a caller must not be able to probe for a handle's existence by
	// the shape of the refusal.
	ErrHandleUnknown = errors.New("effect: handle is not known")
	// ErrHandleExpired means the handle's window has closed.
	ErrHandleExpired = errors.New("effect: handle has expired")
	// ErrHandleExhausted means the granted-use budget is spent.
	ErrHandleExhausted = errors.New("effect: handle has no remaining uses")
	// ErrHandleMismatch means the handle is live but was not minted for this
	// capability or this class of use.
	ErrHandleMismatch = errors.New("effect: handle does not cover this effect")
)

// Live reports whether a handle may still be used at now, before any
// capability or binding question is asked.
func (h Handle) Live(now time.Time) error {
	if !h.RevokedAt.IsZero() {
		return ErrHandleUnknown
	}
	if !h.ExpiresAt.IsZero() && !now.Before(h.ExpiresAt) {
		return ErrHandleExpired
	}
	if h.MaxUses > 0 && h.Used >= h.MaxUses {
		return ErrHandleExhausted
	}
	return nil
}

// Covers reports whether a handle was minted for this capability.
func (h Handle) Covers(capability Capability) bool {
	return Holds(h.Capabilities, capability)
}

// Mint describes a handle a host component wants issued. Every field is
// host-supplied; there is no constructor that takes a renderer's word for any
// of them.
type Mint struct {
	Class            HandleClass
	RootID           string
	RelativePath     string
	InteractionID    string
	ParticipantScope string
	BindingDigest    string
	Capabilities     []Capability
	Lifetime         time.Duration
	MaxUses          int
	// Label, MediaType, and SizeBytes are display material carried into the
	// renderer's [View]. They are never used in a decision.
	Label     string
	MediaType string
	SizeBytes int64
}
