// Package sqlstore implements the go-hitl Store (libs/mesh/core/hitl) over
// Tangent's interaction tables, so a go-hitl Service and Tangent's own inbox
// see the same rows. Tangent stays the queue and UI; this is only the
// persistence boundary the lib's Service needs, held to the lib's hitltest
// storecontract.
//
// Mapping (lib Record -> interactions):
//
//	ItemID, CallerScope, IdempotencyKey, Request, State, CreatedAt, UpdatedAt
//	    -> id, caller_scope, idempotency_key, request_snapshot (verbatim),
//	       lifecycle_state, created_at, updated_at
//	Digest    -> policy.request_digest   (same member Tangent's hitl service writes)
//	ExpiresAt -> policy.expires_at / policy.expires_at_ns
//	Kind      -> external_refs.hitl_kind
//	Revision  -> revision - revisionBase (a row is staged at revision 3, so a
//	             record presented at lib revision 1 is interactions revision 4)
//	Outcome   -> terminal_* columns, plus a resolutions row for Resolved; the
//	             exact lib outcome rides in the terminal event's metadata so a
//	             Resolved participant (responder, proof) round-trips losslessly.
//
// The CHECK constraints, lifecycle triggers and revision guards of the
// interactions table apply unchanged underneath.
package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tangent/internal/events"
	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	// revisionBase is interactions.revision of a staged row; lib revisions
	// count from the presented state at 1.
	revisionBase int64 = 3

	// DefaultSurfaceID matches internal/hitl.DefaultSurfaceID so rows land in
	// the surface Tangent's inbox already shows.
	DefaultSurfaceID = "surface_hitl_default"

	ownerScope      = "operator:local"
	callerAuthority = "go-hitl-store"
	callerAssurance = "asserted"
	timeLayout      = time.RFC3339Nano
)

// Store is a hitl.Store backed by the interactions tables.
type Store struct {
	db        *sql.DB
	surfaces  *interaction.Store
	surfaceID string
}

var _ hitl.Store = (*Store)(nil)

// New returns a Store over a migrated Tangent database.
func New(db *sql.DB) *Store {
	return &Store{db: db, surfaces: interaction.NewStore(db), surfaceID: DefaultSurfaceID}
}

// terminalStatesJSON is a JSON array of the lib's terminal states, derived from
// hitl.AllStates so a lib change can't leave the expiry scan behind.
var terminalStatesJSON = func() string {
	var states []string
	for _, state := range hitl.AllStates() {
		if state.IsTerminal() {
			states = append(states, string(state))
		}
	}
	raw, _ := json.Marshal(states)
	return string(raw)
}()

func ts(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTS(v any) (time.Time, error) {
	var s string
	switch typed := v.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		s = typed
	case []byte:
		s = string(typed)
	default:
		return time.Time{}, fmt.Errorf("sqlstore: unsupported time %T", v)
	}
	for _, layout := range []string{timeLayout, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("sqlstore: unsupported time %q", s)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const rowSelect = `SELECT id, caller_scope, idempotency_key, request_snapshot, policy, external_refs,
lifecycle_state, revision, created_at, updated_at, terminal_at, terminal_cause, terminal_reason,
terminal_policy_ref, terminal_error_code, replacement_interaction_id
FROM interactions`

func (s *Store) load(ctx context.Context, q queryer, where string, args ...any) (hitl.Record, int64, error) {
	var (
		rec                                      hitl.Record
		request, policy, refs, state             string
		dbRev                                    int64
		created, updated, terminalAt             any
		cause, reason, policyRef, code, replaceI sql.NullString
	)
	// Only this store's surface: interactions also holds docs and turns rows
	// that must not be reachable (or replayed) through the HITL store.
	err := q.QueryRowContext(ctx, rowSelect+` WHERE surface_id = ? AND (`+where+`)`, append([]any{s.surfaceID}, args...)...).Scan(&rec.ItemID, &rec.CallerScope,
		&rec.IdempotencyKey, &request, &policy, &refs, &state, &dbRev, &created, &updated, &terminalAt,
		&cause, &reason, &policyRef, &code, &replaceI)
	if errors.Is(err, sql.ErrNoRows) {
		return hitl.Record{}, 0, hitl.ErrNotFound
	}
	if err != nil {
		return hitl.Record{}, 0, err
	}
	rec.Request, rec.State, rec.Revision = []byte(request), hitl.State(state), dbRev-revisionBase
	if rec.CreatedAt, err = parseTS(created); err != nil {
		return hitl.Record{}, 0, err
	}
	if rec.UpdatedAt, err = parseTS(updated); err != nil {
		return hitl.Record{}, 0, err
	}
	var p struct {
		Digest    string `json:"request_digest"`
		ExpiresAt string `json:"expires_at"`
	}
	if err = json.Unmarshal([]byte(policy), &p); err != nil {
		return hitl.Record{}, 0, fmt.Errorf("sqlstore: policy: %w", err)
	}
	rec.Digest = p.Digest
	if p.ExpiresAt != "" {
		t, perr := time.Parse(timeLayout, p.ExpiresAt)
		if perr != nil {
			return hitl.Record{}, 0, fmt.Errorf("sqlstore: expires_at: %w", perr)
		}
		t = t.UTC()
		rec.ExpiresAt = &t
	}
	var r struct {
		Kind string `json:"hitl_kind"`
	}
	if err = json.Unmarshal([]byte(refs), &r); err != nil {
		return hitl.Record{}, 0, fmt.Errorf("sqlstore: external_refs: %w", err)
	}
	rec.Kind = r.Kind
	if rec.State.IsTerminal() {
		at := rec.UpdatedAt
		if terminalAt != nil {
			if at, err = parseTS(terminalAt); err != nil {
				return hitl.Record{}, 0, err
			}
		}
		if rec.Outcome, err = s.outcome(ctx, q, rec, at, cause.String, reason.String, policyRef.String, code.String, replaceI.String); err != nil {
			return hitl.Record{}, 0, err
		}
	}
	return rec, dbRev, nil
}

func (s *Store) outcome(ctx context.Context, q queryer, rec hitl.Record, at time.Time, cause, reason, policyRef, code, replacement string) (hitl.Outcome, error) {
	switch rec.State {
	case hitl.StateCanceled:
		return hitl.Canceled{ItemID: rec.ItemID, InteractionRevision: rec.Revision, Cause: hitl.CancelCause(cause), Reason: reason, TerminatedAt: at}, nil
	case hitl.StateExpired:
		return hitl.Expired{ItemID: rec.ItemID, InteractionRevision: rec.Revision, PolicyRef: policyRef, TerminatedAt: at}, nil
	case hitl.StateFailed:
		return hitl.Failed{ItemID: rec.ItemID, InteractionRevision: rec.Revision, ErrorCode: code, Message: reason, TerminatedAt: at}, nil
	case hitl.StateSuperseded:
		return hitl.Superseded{ItemID: rec.ItemID, InteractionRevision: rec.Revision, ReplacementItemID: replacement, TerminatedAt: at}, nil
	case hitl.StateResolved:
		res, err := s.resolution(ctx, q, rec)
		if err != nil {
			return nil, err
		}
		return hitl.Resolved{ItemID: rec.ItemID, InteractionRevision: rec.Revision, Resolution: res}, nil
	}
	return nil, fmt.Errorf("sqlstore: state %q is not terminal", rec.State)
}

// resolution returns the exact lib ResolutionRecord when this store wrote the
// terminal event, and otherwise rebuilds it from the resolutions row (rows
// resolved through Tangent's own service).
func (s *Store) resolution(ctx context.Context, q queryer, rec hitl.Record) (hitl.ResolutionRecord, error) {
	var metadata string
	err := q.QueryRowContext(ctx, `SELECT metadata FROM interaction_events
WHERE interaction_id = ? AND event_type = 'interaction.resolved' ORDER BY rowid DESC LIMIT 1`, rec.ItemID).Scan(&metadata)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return hitl.ResolutionRecord{}, err
	}
	if err == nil {
		var m struct {
			Resolution *hitl.ResolutionRecord `json:"hitl_resolution"`
		}
		if json.Unmarshal([]byte(metadata), &m) == nil && m.Resolution != nil {
			return *m.Resolution, nil
		}
	}
	var (
		id, ref, authority, assurance, kind, payload string
		presented                                    int64
		recorded                                     any
	)
	if err = q.QueryRowContext(ctx, `SELECT id, participant_ref, participant_authority, participant_assurance,
response_kind, response_payload, presented_projection_revision, recorded_at
FROM resolutions WHERE interaction_id = ?`, rec.ItemID).Scan(&id, &ref, &authority, &assurance, &kind, &payload, &presented, &recorded); err != nil {
		return hitl.ResolutionRecord{}, fmt.Errorf("sqlstore: resolution of %s: %w", rec.ItemID, err)
	}
	at, err := parseTS(recorded)
	if err != nil {
		return hitl.ResolutionRecord{}, err
	}
	var response hitl.Response
	if err = json.Unmarshal([]byte(payload), &response); err != nil {
		return hitl.ResolutionRecord{}, fmt.Errorf("sqlstore: response payload: %w", err)
	}
	if response.Kind == "" {
		response.Kind = kind
	}
	return hitl.ResolutionRecord{
		ResolutionID: id, Response: response,
		Participant: hitl.Participant{PrincipalRef: ref, Authority: authority, Assurance: assurance},
		ResolvedAt:  at, InteractionRevision: rec.Revision, PresentedProjectionRevision: &presented,
	}, nil
}

// ensureSurface creates and activates the default surface once.
func (s *Store) ensureSurface(ctx context.Context) error {
	surface, err := s.surfaces.GetSurface(ctx, s.surfaceID)
	if errors.Is(err, interaction.ErrNotFound) {
		surface, err = s.surfaces.CreateSurface(ctx, interaction.CreateSurfaceParams{
			ID: s.surfaceID, OwnerScope: ownerScope, Authority: callerAuthority,
			Metadata: json.RawMessage(`{"kind":"hitl_inbox","contract_version":"1.0"}`),
			Policy:   json.RawMessage(`{"ordering":"fifo","visibility":"operator"}`),
		})
		if err != nil { // lost a creation race: the surface exists now
			surface, err = s.surfaces.GetSurface(ctx, s.surfaceID)
		}
	}
	if err != nil {
		return err
	}
	if surface.State == interaction.SurfaceStateCreated {
		if _, err = s.surfaces.AdvanceSurface(ctx, interaction.AdvanceSurfaceParams{
			SurfaceID: s.surfaceID, ExpectedRevision: surface.Revision, To: interaction.SurfaceStateActive, Authority: callerAuthority,
		}); err != nil && !errors.Is(err, interaction.ErrRevisionConflict) && !errors.Is(err, interaction.ErrNotRespondable) {
			return err
		}
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{hitl.ErrInvalidRecord}, args...)...)
}

// Create implements hitl.Store.
func (s *Store) Create(ctx context.Context, rec hitl.Record) (hitl.Record, bool, error) {
	if err := rec.Validate(); err != nil {
		return hitl.Record{}, false, err
	}
	if rec.State.IsTerminal() {
		return hitl.Record{}, false, invalid("a record is created nonterminal, got %s", rec.State)
	}
	if !json.Valid(rec.Request) {
		return hitl.Record{}, false, invalid("request is not JSON")
	}
	if err := s.ensureSurface(ctx); err != nil {
		return hitl.Record{}, false, err
	}
	policy := map[string]any{"request_digest": rec.Digest}
	if rec.ExpiresAt != nil {
		policy["expires_at"] = ts(*rec.ExpiresAt)
		policy["expires_at_ns"] = rec.ExpiresAt.UnixNano()
		policy["policy_ref"] = "request.expires_at"
	}
	policyJSON, _ := json.Marshal(policy)
	refsJSON, _ := json.Marshal(map[string]string{"hitl_kind": rec.Kind})

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return hitl.Record{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, _, getErr := s.load(ctx, tx, `caller_scope = ? AND idempotency_key = ?`, rec.CallerScope, rec.IdempotencyKey); getErr == nil {
		return existing, false, nil
	} else if !errors.Is(getErr, hitl.ErrNotFound) {
		return hitl.Record{}, false, getErr
	}
	var sequence, surfaceRev int64
	if err = tx.QueryRowContext(ctx, `SELECT next_interaction_sequence, revision FROM surfaces WHERE id = ?`, s.surfaceID).Scan(&sequence, &surfaceRev); err != nil {
		return hitl.Record{}, false, err
	}
	now := ts(rec.UpdatedAt)
	if _, err = tx.ExecContext(ctx, `UPDATE surfaces SET next_interaction_sequence = next_interaction_sequence + 1,
revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ?`, now, s.surfaceID, surfaceRev); err != nil {
		return hitl.Record{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO interactions (id, surface_id, caller_scope, caller_authority, caller_assurance, idempotency_key,
  surface_sequence, request_snapshot, external_refs, policy, lifecycle_state, revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ItemID, s.surfaceID, rec.CallerScope, callerAuthority, callerAssurance, rec.IdempotencyKey,
		sequence, string(rec.Request), string(refsJSON), string(policyJSON), string(rec.State),
		rec.Revision+revisionBase, ts(rec.CreatedAt), now); err != nil {
		return hitl.Record{}, false, invalid("%v", err)
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO definition_bindings (interaction_id, publisher, kind, version, revision, source, assurance, bound_at)
VALUES (?, 'tangent', 'hitl_item', 'v1', '1', 'go-hitl-store', ?, ?)`, rec.ItemID, callerAssurance, now); err != nil {
		return hitl.Record{}, false, err
	}
	if err = insertEvent(ctx, tx, s.surfaceID, rec.ItemID, "interaction."+string(rec.State), nil, rec.Revision+revisionBase, now, `{}`); err != nil {
		return hitl.Record{}, false, err
	}
	if err = emitRequested(ctx, tx, rec); err != nil {
		return hitl.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return hitl.Record{}, false, err
	}
	got, _, err := s.load(ctx, s.db, `id = ?`, rec.ItemID)
	return got, true, err
}

func insertEvent(ctx context.Context, tx *sql.Tx, surfaceID, itemID, eventType string, from *int64, to int64, at, metadata string) error {
	var fromRev any
	if from != nil {
		fromRev = *from
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO interaction_events (event_id, surface_id, interaction_id, event_type, authority, from_revision, to_revision, metadata, recorded_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, uuid.NewString(), surfaceID, itemID, eventType, callerAuthority, fromRev, to, metadata, at)
	return err
}

// Get implements hitl.Store.
func (s *Store) Get(ctx context.Context, itemID string) (hitl.Record, error) {
	rec, _, err := s.load(ctx, s.db, `id = ?`, itemID)
	return rec, err
}

// Swap implements hitl.Store. The UPDATE is conditional on the stored revision;
// the table's own triggers refuse any change to a terminal row.
func (s *Store) Swap(ctx context.Context, itemID string, expected int64, next hitl.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	prev, prevRev, err := s.load(ctx, tx, `id = ?`, itemID)
	if err != nil {
		return err
	}
	if err = hitl.CheckSwap(prev, expected, next); err != nil {
		return err
	}
	var surfaceID string
	if err = tx.QueryRowContext(ctx, `SELECT surface_id FROM interactions WHERE id = ?`, itemID).Scan(&surfaceID); err != nil {
		return err
	}
	at := ts(next.UpdatedAt)
	nextRev := next.Revision + revisionBase
	metadata := `{}`
	var res sql.Result
	if next.Outcome == nil {
		res, err = tx.ExecContext(ctx, `UPDATE interactions SET lifecycle_state = ?, revision = ?, updated_at = ?
WHERE id = ? AND revision = ? AND surface_id = ?`, string(next.State), nextRev, at, itemID, prevRev, s.surfaceID)
	} else {
		var cause, reason, policyRef, code, replacement any
		switch o := next.Outcome.(type) {
		case hitl.Canceled:
			cause, reason = string(o.Cause), nullable(o.Reason)
		case hitl.Expired:
			policyRef = o.PolicyRef
		case hitl.Failed:
			code, reason = o.ErrorCode, o.Message
		case hitl.Superseded:
			replacement = o.ReplacementItemID
		case hitl.Resolved:
			raw, mErr := json.Marshal(map[string]any{"hitl_resolution": o.Resolution})
			if mErr != nil {
				return invalid("resolution: %v", mErr)
			}
			metadata = string(raw)
		default:
			return invalid("unknown outcome %T", next.Outcome)
		}
		res, err = tx.ExecContext(ctx, `
UPDATE interactions SET lifecycle_state = ?, revision = ?, updated_at = ?, terminal_at = ?,
  terminal_cause = ?, terminal_reason = ?, terminal_policy_ref = ?, terminal_error_code = ?, replacement_interaction_id = ?
WHERE id = ? AND revision = ? AND surface_id = ?`, string(next.State), nextRev, at, ts(next.Outcome.OutcomeTime()),
			cause, reason, policyRef, code, replacement, itemID, prevRev, s.surfaceID)
	}
	if err != nil {
		return invalid("%v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return hitl.ErrRevisionMismatch
	}
	if o, ok := next.Outcome.(hitl.Resolved); ok {
		if err = insertResolution(ctx, tx, itemID, prevRev, o.Resolution); err != nil {
			return invalid("resolution: %v", err)
		}
	}
	if err = insertEvent(ctx, tx, surfaceID, itemID, "interaction."+string(next.State), &prevRev, nextRev, at, metadata); err != nil {
		return err
	}
	if err = emitResolved(ctx, tx, prev, next); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		// A concurrent writer got there first; report what is stored now.
		return s.classifyLoss(ctx, itemID, err)
	}
	return nil
}

// classifyLoss turns a failed commit into the Store error for a lost race.
func (s *Store) classifyLoss(ctx context.Context, itemID string, cause error) error {
	cur, err := s.Get(ctx, itemID)
	if err != nil {
		return cause
	}
	if cur.State.IsTerminal() {
		return hitl.ErrTerminalRecord
	}
	return hitl.ErrRevisionMismatch
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func insertResolution(ctx context.Context, tx *sql.Tx, itemID string, expectedRev int64, r hitl.ResolutionRecord) error {
	payload, err := json.Marshal(r.Response)
	if err != nil {
		return err
	}
	ref := r.Participant.PrincipalRef
	if ref == "" && r.Participant.Responder != nil {
		ref = r.Participant.Responder.Ref
	}
	presented := int64(1)
	if r.PresentedProjectionRevision != nil {
		presented = *r.PresentedProjectionRevision
	}
	whole, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(whole)
	at := ts(r.ResolvedAt)
	_, err = tx.ExecContext(ctx, `
INSERT INTO resolutions (id, interaction_id, expected_interaction_revision, presented_projection_revision,
  participant_scope, participant_ref, participant_authority, participant_assurance, response_kind,
  response_payload, integrity_digest, submitted_at, validated_at, recorded_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ResolutionID, itemID, expectedRev, presented, "go-hitl", ref, r.Participant.Authority,
		r.Participant.Assurance, r.Response.Kind, string(payload), hex.EncodeToString(sum[:]), at, at, at)
	return err
}

// limitOrAll maps the Store contract's "no limit" (limit <= 0) to SQLite's -1.
func limitOrAll(limit int) int {
	if limit > 0 {
		return limit
	}
	return -1
}

// DueForExpiry implements hitl.Store.
func (s *Store) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]hitl.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM interactions
WHERE lifecycle_state NOT IN (SELECT value FROM json_each(?)) AND surface_id = ?
  AND json_extract(policy, '$.expires_at_ns') IS NOT NULL AND json_extract(policy, '$.expires_at_ns') <= ?
ORDER BY json_extract(policy, '$.expires_at_ns'), id
LIMIT ?`, terminalStatesJSON, s.surfaceID, now.UnixNano(), limitOrAll(limit))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	due := make([]hitl.Record, 0, len(ids))
	for _, id := range ids {
		rec, getErr := s.Get(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		due = append(due, rec)
	}
	return due, rows.Err()
}

// approvalKind is the go-hitl item kind that maps to approval.* events.
const approvalKind = "approval"

// emitRequested writes the approval.requested outbox row for a newly created
// approval, in the creating transaction.
func emitRequested(ctx context.Context, tx *sql.Tx, rec hitl.Record) error {
	if rec.Kind != approvalKind {
		return nil
	}
	a := events.Approval{
		Kind: "approval.requested", ItemID: rec.ItemID, Class: approvalKind, At: rec.UpdatedAt,
		Requester: callerPrincipal(rec.CallerScope),
	}
	if rec.ExpiresAt != nil {
		a.ExpiresAt = ts(*rec.ExpiresAt)
	}
	_, err := events.Emitter{}.Emit(ctx, tx, a)
	return err
}

// emitResolved writes the single approval.resolved row for the terminal swap of
// an approval. Nonterminal swaps (presentation) emit nothing.
func emitResolved(ctx context.Context, tx *sql.Tx, prev, next hitl.Record) error {
	if prev.Kind != approvalKind || next.Outcome == nil {
		return nil
	}
	requester := callerPrincipal(prev.CallerScope)
	a := events.Approval{Kind: "approval.resolved", ItemID: prev.ItemID, At: next.Outcome.OutcomeTime()}
	switch o := next.Outcome.(type) {
	case hitl.Resolved:
		a.Outcome = events.OutcomeDenied
		if o.Resolution.Response.Decision == "approved" {
			a.Outcome = events.OutcomeApproved
		}
		a.Resolver = participantPrincipal(o.Resolution.Participant)
	case hitl.Expired:
		a.Outcome = events.OutcomeExpired
		a.Resolver = events.Principal{Kind: "service", ID: "hitl-expiry", URN: "msg://service/tangent/hitl-expiry", Role: "system"}
	case hitl.Failed:
		a.Outcome, a.Resolver = events.OutcomeFailed, requester
	case hitl.Canceled:
		a.Outcome, a.Resolver = events.OutcomeWithdrawn, requester
	default: // superseded
		a.Outcome, a.Resolver = events.OutcomeWithdrawn, requester
	}
	_, err := events.Emitter{}.Emit(ctx, tx, a)
	return err
}

func callerPrincipal(scope string) events.Principal {
	return events.Principal{Kind: "agent", ID: scope, URN: "msg://agent/tangent/" + urnSegment(scope)}
}

func participantPrincipal(p hitl.Participant) events.Principal {
	ref := p.PrincipalRef
	if ref == "" && p.Responder != nil {
		ref = p.Responder.Ref
	}
	if ref == "" {
		return events.Principal{}
	}
	return events.Principal{Kind: "operator", ID: ref, URN: "msg://user/local/" + urnSegment(ref), Role: "operator"}
}

func urnSegment(s string) string {
	out := []rune(s)
	for i, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
		default:
			out[i] = '_'
		}
	}
	return string(out)
}
