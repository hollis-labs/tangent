package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OperationKind names one retention operation.
//
// The five deletion kinds ADR 0002 and this task's acceptance both require to
// stay distinguishable, plus the one cascade delete the schema obstruction was
// hiding. They are separate verbs because they have separate authority and
// separate consequences, and an operator who cannot tell them apart will reach
// for the wrong one during an incident:
//
//	OperationCapabilityExpiry      Host or user withdraws a live grant. Deletes
//	                               effect_handles rows. Touches no content and
//	                               needs no guard suspension — effect_handles
//	                               was designed deletable (migration 0009).
//	OperationDraftDeletion         The participant's work in progress is
//	                               dropped. Tombstones draft_revisions.payload
//	                               and writes the first rows this schema has
//	                               ever put in draft_revision_tombstones.
//	OperationPayloadRedaction      Content is removed from a record that
//	                               survives. Identity, lifecycle, digests, and
//	                               participant binding are preserved by
//	                               construction, not by care.
//	OperationSurfaceClose          A lifecycle transition. Removes nothing.
//	                               Recorded here so that the audit log shows
//	                               plainly that it removed nothing.
//	OperationSurfacePurge          The cascade delete. Destroys record
//	                               skeletons including idempotency keys, so it
//	                               is never automatic and never a window.
//	OperationExternalSourceDelete  Tangent removes nothing and cannot. The
//	                               content was never here; only a typed
//	                               reference was. Recorded as a refusal so the
//	                               limit is evidence rather than silence.
type OperationKind string

const (
	OperationCapabilityExpiry     OperationKind = "capability-expiry"
	OperationDraftDeletion        OperationKind = "draft-deletion"
	OperationPayloadRedaction     OperationKind = "payload-redaction"
	OperationSurfaceClose         OperationKind = "surface-close"
	OperationSurfacePurge         OperationKind = "surface-purge"
	OperationExternalSourceDelete OperationKind = "external-source-deletion"
)

// Authority is who the operation ran under. `local-user` is the only authority
// ADR 0002 permits for erasing a resolution; `host-policy` is a configured
// window expiring; `maintenance` is a repair that moved no content.
type Authority string

const (
	AuthorityLocalUser   Authority = "local-user"
	AuthorityHostPolicy  Authority = "host-policy"
	AuthorityMaintenance Authority = "maintenance"
)

// Outcome mirrors the CHECK on retention_operations.
type Outcome string

const (
	OutcomeApplied Outcome = "applied"
	OutcomeRefused Outcome = "refused"
	OutcomeFailed  Outcome = "failed"
)

// ErrNoSuchTarget is returned when the named surface or interaction is not in
// the database. It is a refusal, not a failure: asking to erase something that
// is already gone is a reasonable thing for an operator to do twice.
var ErrNoSuchTarget = errors.New("no such retention target")

// RedactionTombstone is the exact shape ADR 0002 §2 specifies for a payload
// column whose content has been removed.
//
// The digest is the point. It is what keeps "was this the payload that
// produced that resolution?" answerable after the content is gone, and it is
// why redaction is not the same as writing null.
type RedactionTombstone struct {
	Redacted      bool   `json:"redacted"`
	PolicyRef     string `json:"policy_ref"`
	At            string `json:"at"`
	ContentDigest string `json:"content_digest"`
	ActorRef      string `json:"actor_ref"`
}

// IsRedacted reports whether a stored column value is already a tombstone, so
// a repeated erasure neither double-counts nor digests a digest.
func IsRedacted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var probe struct {
		Redacted bool `json:"redacted"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return false
	}
	return probe.Redacted
}

// RemovedContent is one column's worth of erasure, recorded by digest.
type RemovedContent struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// BackupSurvey is what the operation knew about backups that still hold the
// content it removed. ADR 0002 §7: redaction cannot reach a backup, and the
// operation records what it knows rather than claiming to have cleaned it.
type BackupSurvey struct {
	// State is `not-surveyed` (nobody looked — the default, and not the same
	// as "there are none"), `surveyed`, or `survey-failed`.
	State string `json:"state"`
	// IDs are the backup identifiers from manifests found in a surveyed
	// directory that were taken before this operation ran.
	IDs []string `json:"ids"`
}

const (
	backupSurveyNone   = "not-surveyed"
	backupSurveyDone   = "surveyed"
	backupSurveyFailed = "survey-failed"
)

// RetentionRequest is the operator's ask.
type RetentionRequest struct {
	Kind          OperationKind
	SurfaceID     string
	InteractionID string
	ActorRef      string
	Authority     Authority
	PolicyRef     string
	// DryRun computes and reports everything the operation would remove and
	// writes nothing — not the content change, and not an audit row. A plan is
	// not an act, and logging it as one would make the erasure log lie.
	DryRun bool
	// Backups is what the caller surveyed. Zero value means nobody looked.
	Backups BackupSurvey
	// Now is injectable for tests; zero means time.Now().UTC().
	Now time.Time
}

func (r RetentionRequest) at() time.Time {
	if r.Now.IsZero() {
		return time.Now().UTC()
	}
	return r.Now.UTC()
}

// RetentionResult is what happened, in the same shape the audit row records.
type RetentionResult struct {
	OperationID   string           `json:"operation_id"`
	Kind          OperationKind    `json:"kind"`
	SurfaceID     string           `json:"surface_id,omitempty"`
	InteractionID string           `json:"interaction_id,omitempty"`
	Outcome       Outcome          `json:"outcome"`
	Code          string           `json:"code,omitempty"`
	AffectedRows  int              `json:"affected_rows"`
	Removed       []RemovedContent `json:"removed"`
	// GuardsRestored is false for any kind that never suspended a guard, and
	// is the single most important field on a kind that did.
	GuardsRestored bool `json:"guards_restored"`
	DryRun         bool `json:"dry_run"`
	// Note carries the honest caveat for kinds that could not do what their
	// name suggests — chiefly external-source deletion.
	Note string `json:"note,omitempty"`
}

// ── Guard sets ─────────────────────────────────────────────────────────
//
// Each operation names the smallest set of guards it must suspend. They are
// package constants rather than a computed "all triggers on these tables"
// because the point of §6's mechanism is that the hole is narrow and stated.

// redactionGuards are suspended to write a tombstone into a payload column.
// Three shapes appear: outright immutability (resolutions, drafts, open
// requests), identity immutability (interactions), and the revision/terminal
// pair that fires on any UPDATE to a lifecycle-bearing row. Redaction is not a
// lifecycle change, so it does not advance a revision, which is exactly why
// the revision guards have to be suspended rather than satisfied.
var redactionGuards = []string{
	"interactions_identity_immutable",
	"interactions_revision_must_advance",
	"interactions_terminal_immutable",
	"resolutions_immutable_update",
	"draft_revisions_immutable_update",
	"surface_open_requests_immutable_update",
	"resolution_deliveries_revision_must_advance",
	"resolution_deliveries_terminal_immutable",
	"terminal_notifications_revision_must_advance",
	"terminal_notifications_terminal_immutable",
	"delivery_attempts_valid_seal_update",
	"surfaces_revision_must_advance",
	"surfaces_terminal_immutable",
}

// draftGuards are the subset a draft deletion needs. It is deliberately not
// redactionGuards: dropping ten triggers to touch one table would make the
// window wider than the work.
var draftGuards = []string{
	"draft_revisions_immutable_update",
}

// purgeGuards are the eleven delete guards a surface cascade reaches, plus the
// interactions update guards, because a purge first has to break the
// self-reference `interactions.replacement_interaction_id`, which is
// ON DELETE NO ACTION and would otherwise abort the cascade.
//
// definition_manifests_immutable_delete is absent on purpose: manifests are
// registry-wide, not per-surface, and a purge has no business reaching them.
var purgeGuards = []string{
	"surface_events_immutable_delete",
	"surface_open_requests_immutable_delete",
	"interaction_events_immutable_delete",
	"definition_bindings_immutable_delete",
	"draft_revisions_immutable_delete",
	"resolutions_immutable_delete",
	"delivery_attempts_immutable_delete",
	"delivery_events_immutable_delete",
	"terminal_outcome_retrievals_immutable_delete",
	"terminal_outcome_acknowledgements_immutable_delete",
	"interactions_identity_immutable",
	"interactions_revision_must_advance",
	"interactions_terminal_immutable",
}

// ── Redaction targets ──────────────────────────────────────────────────

// redactionTarget is one column that carries removable content, with the
// predicate that selects the rows belonging to one scope.
//
// Every `?` in Where binds the same scope identifier. The Ref expression is
// what the audit row names the removed content by; it is never the content.
type redactionTarget struct {
	Table   string
	Column  string
	Where   string
	RefExpr string
}

// interactionRedactionTargets are the columns ADR 0002 §10 classifies as
// caller payload, participant resolution, participant draft, or adapter
// freeform text, for one interaction.
//
// Absent by design: `interactions.policy`, `interactions.external_refs`,
// `resolutions.integrity_digest`, `resolutions.participant_ref`,
// `definition_bindings.*`, every `*_events.metadata`, and every identity or
// timestamp column. Those are the identity-and-lifecycle field class, and §2
// exempts them from the clamp. They are what survives, and criterion 2's
// preservation properties are exactly them.
var interactionRedactionTargets = []redactionTarget{
	{Table: "interactions", Column: "request_snapshot", Where: "id = ?", RefExpr: "id"},
	{Table: "interactions", Column: "terminal_reason", Where: "id = ?", RefExpr: "id"},
	{Table: "resolutions", Column: "response_payload", Where: "interaction_id = ?", RefExpr: "id"},
	{Table: "draft_revisions", Column: "payload", Where: "interaction_id = ?",
		RefExpr: "interaction_id || '#' || revision"},
	{Table: "terminal_notifications", Column: "terminal_reason", Where: "interaction_id = ?", RefExpr: "id"},
	{Table: "resolution_deliveries", Column: "terminal_reason",
		Where:   "resolution_id IN (SELECT id FROM resolutions WHERE interaction_id = ?)",
		RefExpr: "id"},
	{Table: "delivery_attempts", Column: "error_message",
		Where: "resolution_delivery_id IN (SELECT rd.id FROM resolution_deliveries rd " +
			"JOIN resolutions r ON r.id = rd.resolution_id WHERE r.interaction_id = ?) " +
			"OR terminal_notification_id IN (SELECT id FROM terminal_notifications WHERE interaction_id = ?)",
		RefExpr: "id"},
}

// surfaceRedactionTargets are the surface-scoped columns, run once per surface
// in addition to the interaction-scoped targets for each of its interactions.
//
// `surfaces.metadata` is deliberately absent. §10 classifies it as
// presentation state whose default follows the surface class, and removing it
// from a surface that is still being rendered breaks the projection restore
// without removing anything a participant said. Erasing it is a separate
// decision that this work does not make silently — see the limitation note in
// docs/database-operations.md.
//
// The legacy `rooms`/`envelopes` pair is here because ADR 0002 §10 records
// that it holds the same payloads, and redacting only the canonical copy would
// leave the content in the compatibility projection. This is the half of
// "deletability is exactly backwards" that was already deletable; it now goes
// in the same transaction as the half that was not.
var surfaceRedactionTargets = []redactionTarget{
	{Table: "surfaces", Column: "close_reason", Where: "id = ?", RefExpr: "id"},
	{Table: "surface_open_requests", Column: "request_snapshot", Where: "surface_id = ?", RefExpr: "surface_id"},
	{Table: "rooms", Column: "meta",
		Where:   "id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "id"},
	{Table: "rooms", Column: "phase_outputs",
		Where:   "id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "id"},
	{Table: "rooms", Column: "closed_reason",
		Where:   "id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "id"},
	{Table: "envelopes", Column: "request_payload",
		Where:   "room_id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "room_id || '#' || envelope_id"},
	{Table: "envelopes", Column: "response_payload",
		Where:   "room_id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "room_id || '#' || envelope_id"},
	{Table: "envelopes", Column: "error_message",
		Where:   "room_id IN (SELECT legacy_room_id FROM surfaces WHERE id = ? AND legacy_room_id IS NOT NULL)",
		RefExpr: "room_id || '#' || envelope_id"},
}

var draftRedactionTargets = []redactionTarget{
	{Table: "draft_revisions", Column: "payload", Where: "interaction_id = ?",
		RefExpr: "interaction_id || '#' || revision"},
}

// ── Operations ─────────────────────────────────────────────────────────

// RedactInteraction removes the caller payload, participant drafts,
// participant resolution, and adapter freeform text of one interaction,
// preserving its identity, lifecycle, digests, and participant binding.
func RedactInteraction(ctx context.Context, database *sql.DB, req RetentionRequest) (RetentionResult, error) {
	req.Kind = OperationPayloadRedaction
	if req.InteractionID == "" {
		return RetentionResult{}, fmt.Errorf("redact interaction: %w: no interaction id", ErrNoSuchTarget)
	}
	exists, err := rowExists(ctx, database, `SELECT 1 FROM interactions WHERE id = ?`, req.InteractionID)
	if err != nil {
		return RetentionResult{}, err
	}
	if !exists {
		return refuse(ctx, database, req, "unknown_interaction",
			"no interaction with that id exists in this database")
	}
	return applyRedaction(ctx, database, req, redactionGuards, func(scope scopeBinder) []plannedTarget {
		return scope.interaction(req.InteractionID)
	})
}

// RedactSurface removes content from every interaction on a surface, plus the
// surface-scoped columns and the legacy room projection.
func RedactSurface(ctx context.Context, database *sql.DB, req RetentionRequest) (RetentionResult, error) {
	req.Kind = OperationPayloadRedaction
	if req.SurfaceID == "" {
		return RetentionResult{}, fmt.Errorf("redact surface: %w: no surface id", ErrNoSuchTarget)
	}
	exists, err := rowExists(ctx, database, `SELECT 1 FROM surfaces WHERE id = ?`, req.SurfaceID)
	if err != nil {
		return RetentionResult{}, err
	}
	if !exists {
		return refuse(ctx, database, req, "unknown_surface",
			"no surface with that id exists in this database")
	}
	interactionIDs, err := surfaceInteractionIDs(ctx, database, req.SurfaceID)
	if err != nil {
		return RetentionResult{}, err
	}
	return applyRedaction(ctx, database, req, redactionGuards, func(scope scopeBinder) []plannedTarget {
		planned := scope.surface(req.SurfaceID)
		for _, id := range interactionIDs {
			planned = append(planned, scope.interaction(id)...)
		}
		return planned
	})
}

// DeleteDrafts drops one interaction's participant drafts and writes the
// tombstone rows draft_revision_tombstones has been waiting for since 0003.
//
// It is a separate kind from payload redaction because the authority is
// different: a draft is the participant's own unfinished work, and dropping it
// destroys nothing anyone relied on. A resolution is an answer someone gave.
func DeleteDrafts(ctx context.Context, database *sql.DB, req RetentionRequest) (RetentionResult, error) {
	req.Kind = OperationDraftDeletion
	if req.InteractionID == "" {
		return RetentionResult{}, fmt.Errorf("delete drafts: %w: no interaction id", ErrNoSuchTarget)
	}
	exists, err := rowExists(ctx, database, `SELECT 1 FROM interactions WHERE id = ?`, req.InteractionID)
	if err != nil {
		return RetentionResult{}, err
	}
	if !exists {
		return refuse(ctx, database, req, "unknown_interaction",
			"no interaction with that id exists in this database")
	}

	revisions, err := draftRevisionNumbers(ctx, database, req.InteractionID)
	if err != nil {
		return RetentionResult{}, err
	}
	if len(revisions) == 0 {
		return refuse(ctx, database, req, "no_drafts",
			"the interaction has no draft revisions; the only draft custody that runs today is the browser's")
	}

	result, err := applyRedactionWithExtra(ctx, database, req, draftGuards,
		func(scope scopeBinder) []plannedTarget {
			return scope.drafts(req.InteractionID)
		},
		func(ctx context.Context, tx *sql.Tx, at time.Time) error {
			for _, revision := range revisions {
				_, insertErr := tx.ExecContext(ctx, `
INSERT INTO draft_revision_tombstones (interaction_id, draft_revision, reason, actor_ref, recorded_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (interaction_id, draft_revision) DO NOTHING`,
					req.InteractionID, revision, string(OperationDraftDeletion), req.ActorRef, at)
				if insertErr != nil {
					return fmt.Errorf("write draft tombstone: %w", insertErr)
				}
			}
			return nil
		})
	return result, err
}

// ExpireCapabilities withdraws live effect handles. It is the one deletion
// kind that needs no guard suspension at all: migration 0009 made
// effect_handles deletable on purpose, because a handle is a live grant rather
// than a record of anything.
//
// With an interaction id it removes that interaction's handles; with none, it
// sweeps every handle whose window closed or was revoked before the cutoff.
//
// It does not call effect.SQLStore.RevokeHandlesForInteraction, which is still
// without a caller. Two reasons, and neither is that it was overlooked.
// internal/db sits below internal/effect and must not import it. And that
// method's own documentation names its call site correctly — "what a terminal
// transition calls" — which a retention operation is not; wiring it here would
// give it a caller without giving it the one it needs, and would mark rows
// revoked an instant before deleting them.
func ExpireCapabilities(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	cutoff time.Time,
) (RetentionResult, error) {
	req.Kind = OperationCapabilityExpiry
	at := req.at()

	var (
		query string
		args  []any
	)
	if req.InteractionID != "" {
		query = `DELETE FROM effect_handles WHERE interaction_id = ?`
		args = []any{req.InteractionID}
	} else {
		query = `DELETE FROM effect_handles WHERE expires_at < ? OR revoked_at IS NOT NULL`
		args = []any{cutoff.UTC()}
	}

	if req.DryRun {
		countQuery := strings.Replace(query, "DELETE FROM effect_handles", "SELECT COUNT(*) FROM effect_handles", 1)
		var count int
		if err := database.QueryRowContext(ctx, countQuery, args...).Scan(&count); err != nil {
			return RetentionResult{}, fmt.Errorf("plan capability expiry: %w", err)
		}
		return RetentionResult{
			OperationID: newOperationID(), Kind: req.Kind, InteractionID: req.InteractionID,
			Outcome: OutcomeApplied, AffectedRows: count, DryRun: true, Removed: []RemovedContent{},
		}, nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("begin capability expiry: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	execResult, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("expire capabilities: %w", err)
	}
	affected, err := execResult.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("expire capabilities: %w", err)
	}

	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, InteractionID: req.InteractionID,
		SurfaceID: req.SurfaceID, Outcome: OutcomeApplied, AffectedRows: int(affected),
		Removed: []RemovedContent{},
		// No guard was suspended, so there is nothing to have restored. Saying
		// "true" here would let a reader of the audit log believe the
		// maintenance path ran when it did not.
		GuardsRestored: false,
	}
	if err := writeOperationRow(ctx, tx, req, result, at); err != nil {
		return RetentionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, fmt.Errorf("commit capability expiry: %w", err)
	}
	return result, nil
}

// PurgeSurface deletes a surface and everything the schema cascades from it.
//
// This is the operation the eleven `*_immutable_delete` triggers made
// impossible and the `surface_open_requests` RESTRICT reference made
// impossible twice. It destroys record skeletons — including idempotency keys,
// which is why ADR 0002 rejected purging as the *default* custody mechanism —
// so it is only ever an explicit, named, user-initiated act.
//
// Foreign keys are deferred for the transaction because the cascade order
// SQLite chooses cannot satisfy the composite NO ACTION reference from
// resolutions into draft_revisions while rows are disappearing; deferring
// moves enforcement to commit, by which point every row on both sides is gone
// and the constraint is satisfied.
func PurgeSurface(ctx context.Context, database *sql.DB, req RetentionRequest) (RetentionResult, error) {
	req.Kind = OperationSurfacePurge
	if req.SurfaceID == "" {
		return RetentionResult{}, fmt.Errorf("purge surface: %w: no surface id", ErrNoSuchTarget)
	}
	exists, err := rowExists(ctx, database, `SELECT 1 FROM surfaces WHERE id = ?`, req.SurfaceID)
	if err != nil {
		return RetentionResult{}, err
	}
	if !exists {
		return refuse(ctx, database, req, "unknown_surface",
			"no surface with that id exists in this database")
	}

	footprint, err := purgeFootprint(ctx, database, req.SurfaceID)
	if err != nil {
		return RetentionResult{}, err
	}
	if req.DryRun {
		return RetentionResult{
			OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
			Outcome: OutcomeApplied, AffectedRows: footprint.rows, DryRun: true,
			Removed: []RemovedContent{},
		}, nil
	}

	at := req.at()
	var affected int
	restored, err := withGuardsSuspended(ctx, database, purgeGuards,
		func(ctx context.Context, tx *sql.Tx) error {
			if _, pragmaErr := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON;`); pragmaErr != nil {
				return fmt.Errorf("defer foreign keys for purge: %w", pragmaErr)
			}
			// Break the self-reference first. It is ON DELETE NO ACTION, and a
			// superseded chain whose replacement lives on another surface
			// would abort the cascade at commit.
			if _, updateErr := tx.ExecContext(ctx, `
UPDATE interactions SET replacement_interaction_id = NULL
WHERE replacement_interaction_id IN (SELECT id FROM interactions WHERE surface_id = ?)`,
				req.SurfaceID); updateErr != nil {
				return fmt.Errorf("clear replacement references: %w", updateErr)
			}
			// Live grants go with the interactions they were issued for.
			// effect_receipts stay: they are audit that carries a byte count
			// and a digest, never content, and they are the record that a
			// host-mediated effect happened at all.
			if _, handleErr := tx.ExecContext(ctx, `
DELETE FROM effect_handles
WHERE interaction_id IN (SELECT id FROM interactions WHERE surface_id = ?)`,
				req.SurfaceID); handleErr != nil {
				return fmt.Errorf("revoke handles for purge: %w", handleErr)
			}

			execResult, deleteErr := tx.ExecContext(ctx, `DELETE FROM surfaces WHERE id = ?`, req.SurfaceID)
			if deleteErr != nil {
				return fmt.Errorf("purge surface: %w", deleteErr)
			}
			rows, rowsErr := execResult.RowsAffected()
			if rowsErr != nil {
				return fmt.Errorf("purge surface: %w", rowsErr)
			}
			affected = int(rows)

			// The legacy room goes after the surface, not before: while the
			// surface still exists, deleting the room fires the
			// ON DELETE SET NULL back-reference, which is an UPDATE on a
			// possibly-terminal surface row.
			if _, roomErr := tx.ExecContext(ctx,
				`DELETE FROM rooms WHERE id = ?`, footprint.legacyRoomID); roomErr != nil {
				return fmt.Errorf("purge legacy room: %w", roomErr)
			}
			return nil
		})
	if err != nil {
		failed, _ := recordFailure(ctx, database, req, "purge_failed", err)
		return failed, err
	}

	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
		Outcome: OutcomeApplied, AffectedRows: affected + footprint.rows, Removed: []RemovedContent{},
		GuardsRestored: restored,
	}
	// The audit row is written after the purge commits rather than inside it,
	// because retention_operations has no foreign keys and therefore nothing to
	// lose by being a second transaction — and because writing it inside would
	// mean an audit row that a rollback erases along with the thing it records.
	if err := writeOperationRowDB(ctx, database, req, result, at); err != nil {
		return result, err
	}
	return result, nil
}

// RecordSurfaceClose files the audit row for a surface close performed by the
// lifecycle layer.
//
// Closing a surface is not a deletion, and this function removes nothing. It
// exists because criterion 3 requires the five kinds to stay distinguishable,
// and the way to keep an operator from mistaking a close for an erasure is for
// the erasure log to contain the close, stating plainly that it removed
// nothing.
func RecordSurfaceClose(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	canceledInteractions int,
) (RetentionResult, error) {
	req.Kind = OperationSurfaceClose
	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
		Outcome: OutcomeApplied, Code: "closed", AffectedRows: 0, Removed: []RemovedContent{},
		Note: fmt.Sprintf(
			"surface closed and %d outstanding interaction(s) canceled; no content was removed, "+
				"and every payload the surface holds is still present", canceledInteractions),
	}
	if req.DryRun {
		result.DryRun = true
		return result, nil
	}
	if err := writeOperationRowDB(ctx, database, req, result, req.at()); err != nil {
		return RetentionResult{}, err
	}
	return result, nil
}

// ExternalReference is one typed reference Tangent holds but whose content it
// never did.
type ExternalReference struct {
	InteractionID string          `json:"interaction_id"`
	Refs          json.RawMessage `json:"external_refs"`
}

// ReportExternalSourceDeletion enumerates what Tangent would have to ask
// someone else to delete, and records a refusal.
//
// This is the honest half of ADR 0002's external-reference class. Tangent
// stores an authority, an artifact id, a revision, and a digest; it never
// stored the bytes, so it cannot remove them, and §4 keeps the reference
// itself as `durable-record` because the reference is what survives redaction.
// The operation therefore removes nothing on purpose and says so, rather than
// deleting the reference and leaving the user believing the content is gone.
func ReportExternalSourceDeletion(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
) (RetentionResult, []ExternalReference, error) {
	req.Kind = OperationExternalSourceDelete

	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case req.InteractionID != "":
		rows, err = database.QueryContext(ctx,
			`SELECT id, external_refs FROM interactions WHERE id = ? AND external_refs NOT IN ('{}', '[]', '')`,
			req.InteractionID)
	case req.SurfaceID != "":
		rows, err = database.QueryContext(ctx,
			`SELECT id, external_refs FROM interactions WHERE surface_id = ? AND external_refs NOT IN ('{}', '[]', '')`,
			req.SurfaceID)
	default:
		return RetentionResult{}, nil, fmt.Errorf(
			"external source deletion: %w: name a surface or an interaction", ErrNoSuchTarget)
	}
	if err != nil {
		return RetentionResult{}, nil, fmt.Errorf("read external references: %w", err)
	}
	defer func() { _ = rows.Close() }()

	references := []ExternalReference{}
	for rows.Next() {
		var ref ExternalReference
		var raw string
		if scanErr := rows.Scan(&ref.InteractionID, &raw); scanErr != nil {
			return RetentionResult{}, nil, fmt.Errorf("scan external references: %w", scanErr)
		}
		ref.Refs = json.RawMessage(raw)
		references = append(references, ref)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return RetentionResult{}, nil, fmt.Errorf("iterate external references: %w", rowsErr)
	}

	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind,
		SurfaceID: req.SurfaceID, InteractionID: req.InteractionID,
		Outcome: OutcomeRefused, Code: "external_source_unreachable",
		AffectedRows: 0, Removed: []RemovedContent{},
		Note: fmt.Sprintf(
			"Tangent holds %d typed reference set(s) and none of the referenced content. It cannot "+
				"delete at the source and does not claim to; the references are kept because a digest "+
				"is what remains answerable after the source is gone. Delete at the naming authority.",
			len(references)),
	}
	if req.DryRun {
		result.DryRun = true
		return result, references, nil
	}
	if err := writeOperationRowDB(ctx, database, req, result, req.at()); err != nil {
		return RetentionResult{}, references, err
	}
	return result, references, nil
}

// ── Redaction engine ───────────────────────────────────────────────────

type plannedTarget struct {
	target redactionTarget
	scope  string
}

type scopeBinder struct{}

func (scopeBinder) interaction(id string) []plannedTarget {
	planned := make([]plannedTarget, 0, len(interactionRedactionTargets))
	for _, target := range interactionRedactionTargets {
		planned = append(planned, plannedTarget{target: target, scope: id})
	}
	return planned
}

func (scopeBinder) surface(id string) []plannedTarget {
	planned := make([]plannedTarget, 0, len(surfaceRedactionTargets))
	for _, target := range surfaceRedactionTargets {
		planned = append(planned, plannedTarget{target: target, scope: id})
	}
	return planned
}

func (scopeBinder) drafts(id string) []plannedTarget {
	planned := make([]plannedTarget, 0, len(draftRedactionTargets))
	for _, target := range draftRedactionTargets {
		planned = append(planned, plannedTarget{target: target, scope: id})
	}
	return planned
}

func applyRedaction(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	guards []string,
	plan func(scopeBinder) []plannedTarget,
) (RetentionResult, error) {
	return applyRedactionWithExtra(ctx, database, req, guards, plan, nil)
}

func applyRedactionWithExtra(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	guards []string,
	plan func(scopeBinder) []plannedTarget,
	extra func(context.Context, *sql.Tx, time.Time) error,
) (RetentionResult, error) {
	at := req.at()
	targets := plan(scopeBinder{})

	if req.DryRun {
		removed, err := surveyRedaction(ctx, database, targets)
		if err != nil {
			return RetentionResult{}, err
		}
		return RetentionResult{
			OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
			InteractionID: req.InteractionID, Outcome: OutcomeApplied,
			AffectedRows: len(removed), Removed: removed, DryRun: true,
		}, nil
	}

	var removed []RemovedContent
	restored, err := withGuardsSuspended(ctx, database, guards,
		func(ctx context.Context, tx *sql.Tx) error {
			var writeErr error
			removed, writeErr = redactTargets(ctx, tx, targets, req, at)
			if writeErr != nil {
				return writeErr
			}
			if extra != nil {
				return extra(ctx, tx, at)
			}
			return nil
		})
	if err != nil {
		failed, _ := recordFailure(ctx, database, req, "redaction_failed", err)
		return failed, err
	}

	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
		InteractionID: req.InteractionID, Outcome: OutcomeApplied,
		AffectedRows: len(removed), Removed: removed, GuardsRestored: restored,
	}
	if len(removed) == 0 {
		result.Code = "already_redacted"
		result.Note = "no unredacted content remained in scope"
	}
	if err := writeOperationRowDB(ctx, database, req, result, at); err != nil {
		return result, err
	}
	return result, nil
}

// surveyRedaction reads what would be removed without removing it. It runs
// against the database rather than a transaction because a plan takes no
// locks and changes nothing.
func surveyRedaction(
	ctx context.Context,
	database *sql.DB,
	targets []plannedTarget,
) ([]RemovedContent, error) {
	removed := []RemovedContent{}
	for _, planned := range targets {
		rows, err := readRedactableRows(ctx, database, planned)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			removed = append(removed, RemovedContent{
				Table: planned.target.Table, Column: planned.target.Column,
				Ref: row.ref, SHA256: digestOf(row.value), Bytes: len(row.value),
			})
		}
	}
	return removed, nil
}

func redactTargets(
	ctx context.Context,
	tx *sql.Tx,
	targets []plannedTarget,
	req RetentionRequest,
	at time.Time,
) ([]RemovedContent, error) {
	removed := []RemovedContent{}
	for _, planned := range targets {
		rows, err := readRedactableRows(ctx, tx, planned)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			digest := digestOf(row.value)
			tombstone, marshalErr := json.Marshal(RedactionTombstone{
				Redacted:      true,
				PolicyRef:     req.PolicyRef,
				At:            at.Format(time.RFC3339),
				ContentDigest: digest,
				ActorRef:      req.ActorRef,
			})
			if marshalErr != nil {
				return nil, fmt.Errorf("encode redaction tombstone: %w", marshalErr)
			}
			//nolint:gosec // table and column come from package-level constants
			update := `UPDATE ` + quoteIdentifier(planned.target.Table) +
				` SET ` + quoteIdentifier(planned.target.Column) + ` = ? WHERE rowid = ?`
			if _, execErr := tx.ExecContext(ctx, update, string(tombstone), row.rowID); execErr != nil {
				return nil, fmt.Errorf("redact %s.%s: %w",
					planned.target.Table, planned.target.Column, execErr)
			}
			removed = append(removed, RemovedContent{
				Table: planned.target.Table, Column: planned.target.Column,
				Ref: row.ref, SHA256: digest, Bytes: len(row.value),
			})
		}
	}
	return removed, nil
}

type redactableRow struct {
	rowID int64
	ref   string
	value string
}

func readRedactableRows(ctx context.Context, q queryer, planned plannedTarget) ([]redactableRow, error) {
	target := planned.target
	//nolint:gosec // every fragment is a package-level constant
	query := `SELECT rowid, ` + target.RefExpr + `, ` + quoteIdentifier(target.Column) +
		` FROM ` + quoteIdentifier(target.Table) + ` WHERE ` + target.Where

	args := make([]any, strings.Count(target.Where, "?"))
	for i := range args {
		args[i] = planned.scope
	}

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read %s.%s for redaction: %w", target.Table, target.Column, err)
	}
	defer func() { _ = rows.Close() }()

	found := []redactableRow{}
	for rows.Next() {
		var row redactableRow
		var ref sql.NullString
		var value sql.NullString
		if err := rows.Scan(&row.rowID, &ref, &value); err != nil {
			return nil, fmt.Errorf("scan %s.%s for redaction: %w", target.Table, target.Column, err)
		}
		row.ref = ref.String
		row.value = value.String
		// Skip empty columns and columns already carrying a tombstone. A
		// second erasure of the same content must not digest a digest, and
		// must not report content removed that was already gone.
		if strings.TrimSpace(row.value) == "" || IsRedacted(row.value) {
			continue
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s.%s for redaction: %w", target.Table, target.Column, err)
	}
	return found, nil
}

// ── Audit rows ─────────────────────────────────────────────────────────

func writeOperationRowDB(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	result RetentionResult,
	at time.Time,
) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retention audit: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := writeOperationRow(ctx, tx, req, result, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retention audit: %w", err)
	}
	return nil
}

func writeOperationRow(
	ctx context.Context,
	tx *sql.Tx,
	req RetentionRequest,
	result RetentionResult,
	at time.Time,
) error {
	digests, err := json.Marshal(result.Removed)
	if err != nil {
		return fmt.Errorf("encode removed digests: %w", err)
	}
	survey := req.Backups
	if survey.State == "" {
		survey.State = backupSurveyNone
	}
	if survey.IDs == nil {
		survey.IDs = []string{}
	}
	backups, err := json.Marshal(survey.IDs)
	if err != nil {
		return fmt.Errorf("encode known backups: %w", err)
	}
	var schemaVersion int64
	// Reading the applied version from inside the transaction keeps the audit
	// row's claim about the schema true of the schema the operation ran on.
	if scanErr := tx.QueryRowContext(ctx,
		`SELECT version FROM `+migrationsTable+` LIMIT 1;`).Scan(&schemaVersion); scanErr != nil &&
		!errors.Is(scanErr, sql.ErrNoRows) {
		return fmt.Errorf("read schema version for retention audit: %w", scanErr)
	}

	guards := 0
	if result.GuardsRestored {
		guards = 1
	}
	actor := req.ActorRef
	if actor == "" {
		actor = "operator"
	}
	authority := req.Authority
	if authority == "" {
		authority = AuthorityLocalUser
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO retention_operations (
  operation_id, kind, target_surface_id, target_interaction_id,
  actor_ref, authority, policy_ref,
  requested_at, completed_at, outcome, code, affected_rows,
  removed_digests, known_backups, backup_survey, schema_version, guards_restored
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		result.OperationID, string(result.Kind), req.SurfaceID, req.InteractionID,
		actor, string(authority), req.PolicyRef,
		at, at, string(result.Outcome), result.Code, result.AffectedRows,
		string(digests), string(backups), survey.State, schemaVersion, guards)
	if err != nil {
		return fmt.Errorf("write retention operation: %w", err)
	}
	return nil
}

func refuse(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	code, note string,
) (RetentionResult, error) {
	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
		InteractionID: req.InteractionID, Outcome: OutcomeRefused, Code: code,
		Removed: []RemovedContent{}, Note: note,
	}
	if req.DryRun {
		result.DryRun = true
		return result, nil
	}
	if err := writeOperationRowDB(ctx, database, req, result, req.at()); err != nil {
		return result, err
	}
	return result, nil
}

// recordFailure files the audit row for an operation that errored. The content
// change rolled back with the transaction; the record of the attempt does not.
func recordFailure(
	ctx context.Context,
	database *sql.DB,
	req RetentionRequest,
	code string,
	cause error,
) (RetentionResult, error) {
	_ = cause
	result := RetentionResult{
		OperationID: newOperationID(), Kind: req.Kind, SurfaceID: req.SurfaceID,
		InteractionID: req.InteractionID, Outcome: OutcomeFailed, Code: code,
		Removed: []RemovedContent{},
		// The transaction rolled back, and SQLite DDL rolls back with it, so
		// the guards are in place. It is reported as false anyway: this field
		// means "verified restored inside the committing transaction", and
		// nothing committed.
		GuardsRestored: false,
	}
	err := writeOperationRowDB(ctx, database, req, result, req.at())
	return result, err
}

// ── Reading the log ────────────────────────────────────────────────────

// RetentionOperationRow is one audit row, as an operator reads it back.
type RetentionOperationRow struct {
	OperationID    string    `json:"operation_id"`
	Kind           string    `json:"kind"`
	SurfaceID      string    `json:"surface_id,omitempty"`
	InteractionID  string    `json:"interaction_id,omitempty"`
	ActorRef       string    `json:"actor_ref"`
	Authority      string    `json:"authority"`
	PolicyRef      string    `json:"policy_ref,omitempty"`
	RequestedAt    time.Time `json:"requested_at"`
	Outcome        string    `json:"outcome"`
	Code           string    `json:"code,omitempty"`
	AffectedRows   int       `json:"affected_rows"`
	RemovedCount   int       `json:"removed_count"`
	BackupSurvey   string    `json:"backup_survey"`
	SchemaVersion  int64     `json:"schema_version"`
	GuardsRestored bool      `json:"guards_restored"`
}

// RetentionHistory reads the most recent operations, newest first.
func RetentionHistory(ctx context.Context, database *sql.DB, limit int) ([]RetentionOperationRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := database.QueryContext(ctx, `
SELECT operation_id, kind, target_surface_id, target_interaction_id, actor_ref, authority,
       policy_ref, requested_at, outcome, code, affected_rows, removed_digests,
       backup_survey, schema_version, guards_restored
FROM retention_operations ORDER BY requested_at DESC, operation_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read retention history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	history := []RetentionOperationRow{}
	for rows.Next() {
		var row RetentionOperationRow
		var digests string
		var guards int
		if scanErr := rows.Scan(&row.OperationID, &row.Kind, &row.SurfaceID, &row.InteractionID,
			&row.ActorRef, &row.Authority, &row.PolicyRef, &row.RequestedAt, &row.Outcome,
			&row.Code, &row.AffectedRows, &digests, &row.BackupSurvey, &row.SchemaVersion,
			&guards); scanErr != nil {
			return nil, fmt.Errorf("scan retention history: %w", scanErr)
		}
		row.GuardsRestored = guards == 1
		var removed []RemovedContent
		if json.Unmarshal([]byte(digests), &removed) == nil {
			row.RemovedCount = len(removed)
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention history: %w", err)
	}
	return history, nil
}

// ── Windows and eligibility ────────────────────────────────────────────

// RetentionWindows are ADR 0002 §4's host-configurable defaults.
//
// They are host configuration, not per-interaction policy. Per-interaction
// pinned custody lives in `interactions.policy`, and nothing writes a custody
// object there yet: the precedence engine of §3 is a separate piece of work.
// Until it lands, eligibility is computed from the host window alone and the
// plan says so, rather than pretending to read a declaration that is not there.
type RetentionWindows struct {
	Interaction time.Duration
	Surface     time.Duration
	BrowserTTL  time.Duration
}

// DefaultRetentionWindows are §4's table: 30 days after an interaction reaches
// a terminal state, 90 days after a surface closes or expires, 14 days for a
// browser draft, and nothing at all for ephemeral content because nothing is
// written.
func DefaultRetentionWindows() RetentionWindows {
	return RetentionWindows{
		Interaction: 30 * 24 * time.Hour,
		Surface:     90 * 24 * time.Hour,
		BrowserTTL:  14 * 24 * time.Hour,
	}
}

// RetentionCandidate is one record past its window.
type RetentionCandidate struct {
	Scope         string    `json:"scope"`
	SurfaceID     string    `json:"surface_id,omitempty"`
	InteractionID string    `json:"interaction_id,omitempty"`
	TerminalAt    time.Time `json:"terminal_at"`
	// UnredactedColumns is how much content is actually still there. A
	// candidate with zero is eligible but already clean.
	UnredactedColumns int `json:"unredacted_columns"`
}

// RetentionPlan is what a window sweep would do.
type RetentionPlan struct {
	Windows           RetentionWindows     `json:"-"`
	InteractionWindow string               `json:"interaction_window"`
	SurfaceWindow     string               `json:"surface_window"`
	Interactions      []RetentionCandidate `json:"interactions"`
	Surfaces          []RetentionCandidate `json:"surfaces"`
	Note              string               `json:"note"`
}

// PlanRetention lists what is past its window. It writes nothing.
//
// A non-terminal interaction is never eligible, which is ADR 0002's
// requirement that a window sweep can never race a resolution: eligibility is
// measured from `terminal_at`, and a row without one is not in the answer.
func PlanRetention(
	ctx context.Context,
	database *sql.DB,
	windows RetentionWindows,
	now time.Time,
) (RetentionPlan, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	plan := RetentionPlan{
		Windows:           windows,
		InteractionWindow: windows.Interaction.String(),
		SurfaceWindow:     windows.Surface.String(),
		Interactions:      []RetentionCandidate{},
		Surfaces:          []RetentionCandidate{},
		Note: "eligibility is computed from the host window alone. Per-interaction custody is " +
			"pinned into interactions.policy by ADR 0002 §3, and nothing writes that object yet, " +
			"so a caller or definition that asked for longer retention is not yet honored here.",
	}

	interactionCutoff := now.Add(-windows.Interaction).UTC()
	rows, err := database.QueryContext(ctx, `
SELECT id, surface_id, terminal_at FROM interactions
WHERE terminal_at IS NOT NULL AND terminal_at < ?
ORDER BY terminal_at`, interactionCutoff)
	if err != nil {
		return RetentionPlan{}, fmt.Errorf("plan interaction retention: %w", err)
	}
	for rows.Next() {
		var candidate RetentionCandidate
		if scanErr := rows.Scan(&candidate.InteractionID, &candidate.SurfaceID,
			&candidate.TerminalAt); scanErr != nil {
			_ = rows.Close()
			return RetentionPlan{}, fmt.Errorf("scan interaction retention plan: %w", scanErr)
		}
		candidate.Scope = "interaction"
		plan.Interactions = append(plan.Interactions, candidate)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return RetentionPlan{}, fmt.Errorf("iterate interaction retention plan: %w", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return RetentionPlan{}, fmt.Errorf("close interaction retention plan: %w", closeErr)
	}

	surfaceCutoff := now.Add(-windows.Surface).UTC()
	surfaceRows, err := database.QueryContext(ctx, `
SELECT id, closed_at FROM surfaces
WHERE lifecycle_state IN ('closed', 'expired') AND closed_at IS NOT NULL AND closed_at < ?
ORDER BY closed_at`, surfaceCutoff)
	if err != nil {
		return RetentionPlan{}, fmt.Errorf("plan surface retention: %w", err)
	}
	defer func() { _ = surfaceRows.Close() }()
	for surfaceRows.Next() {
		var candidate RetentionCandidate
		if scanErr := surfaceRows.Scan(&candidate.SurfaceID, &candidate.TerminalAt); scanErr != nil {
			return RetentionPlan{}, fmt.Errorf("scan surface retention plan: %w", scanErr)
		}
		candidate.Scope = "surface"
		plan.Surfaces = append(plan.Surfaces, candidate)
	}
	if err := surfaceRows.Err(); err != nil {
		return RetentionPlan{}, fmt.Errorf("iterate surface retention plan: %w", err)
	}

	// Count what is actually still there, so a plan of two hundred eligible
	// interactions that are all already redacted reads as the no-op it is.
	binder := scopeBinder{}
	for i := range plan.Interactions {
		removable, surveyErr := surveyRedaction(ctx, database, binder.interaction(plan.Interactions[i].InteractionID))
		if surveyErr != nil {
			return RetentionPlan{}, surveyErr
		}
		plan.Interactions[i].UnredactedColumns = len(removable)
	}
	for i := range plan.Surfaces {
		removable, surveyErr := surveyRedaction(ctx, database, binder.surface(plan.Surfaces[i].SurfaceID))
		if surveyErr != nil {
			return RetentionPlan{}, surveyErr
		}
		plan.Surfaces[i].UnredactedColumns = len(removable)
	}
	return plan, nil
}

// ApplyRetention redacts every candidate the plan found.
//
// It is idempotent — a second run finds tombstones and removes nothing — and
// restart-safe, because each candidate is its own transaction and a partially
// completed sweep leaves the rest eligible. It never deletes a row: a window
// expiring is not the operator asking to destroy a record, and ADR 0002
// rejected row deletion as the custody mechanism precisely because it takes
// the idempotency key with it.
func ApplyRetention(
	ctx context.Context,
	database *sql.DB,
	windows RetentionWindows,
	req RetentionRequest,
	now time.Time,
) ([]RetentionResult, error) {
	plan, err := PlanRetention(ctx, database, windows, now)
	if err != nil {
		return nil, err
	}
	req.Authority = AuthorityHostPolicy
	if req.PolicyRef == "" {
		req.PolicyRef = "adr-0002-window"
	}

	results := []RetentionResult{}
	for _, candidate := range plan.Interactions {
		if candidate.UnredactedColumns == 0 {
			continue
		}
		scoped := req
		scoped.InteractionID = candidate.InteractionID
		scoped.SurfaceID = ""
		result, redactErr := RedactInteraction(ctx, database, scoped)
		if redactErr != nil {
			return results, redactErr
		}
		results = append(results, result)
	}
	for _, candidate := range plan.Surfaces {
		if candidate.UnredactedColumns == 0 {
			continue
		}
		scoped := req
		scoped.SurfaceID = candidate.SurfaceID
		scoped.InteractionID = ""
		result, redactErr := RedactSurface(ctx, database, scoped)
		if redactErr != nil {
			return results, redactErr
		}
		results = append(results, result)
	}
	return results, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func rowExists(ctx context.Context, database *sql.DB, query string, args ...any) (bool, error) {
	var one int
	err := database.QueryRowContext(ctx, query, args...).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("check retention target: %w", err)
	}
	return true, nil
}

func surfaceInteractionIDs(ctx context.Context, database *sql.DB, surfaceID string) ([]string, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT id FROM interactions WHERE surface_id = ? ORDER BY surface_sequence`, surfaceID)
	if err != nil {
		return nil, fmt.Errorf("list surface interactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan surface interactions: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate surface interactions: %w", err)
	}
	return ids, nil
}

func draftRevisionNumbers(ctx context.Context, database *sql.DB, interactionID string) ([]int64, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT revision FROM draft_revisions WHERE interaction_id = ? ORDER BY revision`, interactionID)
	if err != nil {
		return nil, fmt.Errorf("list draft revisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	revisions := []int64{}
	for rows.Next() {
		var revision int64
		if scanErr := rows.Scan(&revision); scanErr != nil {
			return nil, fmt.Errorf("scan draft revisions: %w", scanErr)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate draft revisions: %w", err)
	}
	return revisions, nil
}

// purgeFootprintResult carries the row footprint a purge would destroy, plus
// the legacy room it also has to remove.
func purgeFootprint(ctx context.Context, database *sql.DB, surfaceID string) (purgeFootprintResult, error) {
	var result purgeFootprintResult
	var legacyRoom sql.NullString
	if err := database.QueryRowContext(ctx,
		`SELECT legacy_room_id FROM surfaces WHERE id = ?`, surfaceID).Scan(&legacyRoom); err != nil {
		return result, fmt.Errorf("read surface legacy room: %w", err)
	}
	result.legacyRoomID = legacyRoom.String

	counts := []struct {
		query string
		args  []any
	}{
		{`SELECT COUNT(*) FROM interactions WHERE surface_id = ?`, []any{surfaceID}},
		{`SELECT COUNT(*) FROM surface_events WHERE surface_id = ?`, []any{surfaceID}},
		{`SELECT COUNT(*) FROM interaction_events WHERE surface_id = ?`, []any{surfaceID}},
		{`SELECT COUNT(*) FROM resolutions WHERE interaction_id IN (SELECT id FROM interactions WHERE surface_id = ?)`,
			[]any{surfaceID}},
		{`SELECT COUNT(*) FROM draft_revisions WHERE interaction_id IN (SELECT id FROM interactions WHERE surface_id = ?)`,
			[]any{surfaceID}},
		{`SELECT COUNT(*) FROM surface_open_requests WHERE surface_id = ?`, []any{surfaceID}},
	}
	for _, count := range counts {
		var n int
		if err := database.QueryRowContext(ctx, count.query, count.args...).Scan(&n); err != nil {
			return result, fmt.Errorf("count purge footprint: %w", err)
		}
		result.rows += n
	}
	return result, nil
}

type purgeFootprintResult struct {
	rows         int
	legacyRoomID string
}

func digestOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newOperationID() string {
	return "ret_" + uuid.NewString()
}
