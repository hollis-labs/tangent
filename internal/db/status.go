package db

import (
	"context"
	"database/sql"
	"time"
)

// RetentionStatus is the payload-bounded custody posture of one installation.
//
// It is a separate type from CheckReport rather than a filtered view of it,
// because the two have different audiences and different rules. CheckReport is
// read by an operator at a terminal on the machine, and naming the process that
// holds the lock is useful there. This one is answerable over MCP, so it obeys
// ADR 0002 §8's floor by construction: no path, no hostname, no command line,
// no pid, no payload, no participant text. Every field here is a Tangent
// identifier, an enumerated state, a count, or a duration.
type RetentionStatus struct {
	Migrations MigrationStatus `json:"migrations"`

	// GuardsIntact is the single most important bit in this report. False means
	// this database is missing an immutability trigger, which means something
	// that should be impossible is currently possible.
	GuardsIntact     bool     `json:"guards_intact"`
	GuardCount       int      `json:"guard_count"`
	MissingGuards    []string `json:"missing_guards"`
	UnexpectedGuards []string `json:"unexpected_guards"`

	IntegrityProblems    int `json:"integrity_problems"`
	ForeignKeyViolations int `json:"foreign_key_violations"`

	// OwnershipHeld and OwnershipRole report the single-writer contract without
	// naming the process: whether something owns the database, and whether it is
	// a server or a maintenance command.
	OwnershipHeld bool   `json:"ownership_held"`
	OwnershipRole string `json:"ownership_role,omitempty"`

	Storage StorageReport `json:"storage"`

	// Windows are the host retention windows in force.
	InteractionWindowHours float64 `json:"interaction_window_hours"`
	SurfaceWindowHours     float64 `json:"surface_window_hours"`
	BrowserDraftTTLHours   float64 `json:"browser_draft_ttl_hours"`

	// Eligible counts what a window sweep would act on right now.
	EligibleInteractions            int `json:"eligible_interactions"`
	EligibleSurfaces                int `json:"eligible_surfaces"`
	EligibleInteractionsWithContent int `json:"eligible_interactions_with_content"`
	EligibleSurfacesWithContent     int `json:"eligible_surfaces_with_content"`

	// RecentOperations is the erasure log, newest first, bounded.
	RecentOperations []RetentionOperationRow `json:"recent_operations"`

	// Limitations are the standing statements this installation has to make
	// about what its retention operations cannot reach. They are returned with
	// every status rather than buried in a document, because the moment someone
	// asks about retention is the moment they need to know.
	Limitations []string `json:"limitations"`
}

// Status reports the custody posture. It writes nothing and takes no lock.
func Status(
	ctx context.Context,
	database *sql.DB,
	databasePath string,
	windows RetentionWindows,
	operationLimit int,
) (RetentionStatus, error) {
	status := RetentionStatus{
		MissingGuards: []string{}, UnexpectedGuards: []string{},
		RecentOperations:       []RetentionOperationRow{},
		InteractionWindowHours: windows.Interaction.Hours(),
		SurfaceWindowHours:     windows.Surface.Hours(),
		BrowserDraftTTLHours:   windows.BrowserTTL.Hours(),
		Limitations: []string{
			"Redaction cannot reach a backup. A backup taken before an erasure still holds the " +
				"content, and the operation records which backups it knew about rather than " +
				"claiming to have cleaned them.",
			"Per-caller-scope deletion is unavailable for the standalone-local scope. Every local " +
				"caller shares it and any caller can assert any partition, so a partition filter is " +
				"a convenience for the local user and never an isolation guarantee (ADR 0002 §6, " +
				"ADR 0004).",
			"Tangent cannot delete content at an external source. It stores an authority, an " +
				"artifact id, and a digest; the bytes were never here.",
			"Browser drafts live in the participant's own storage and are outside the server's " +
				"deletion boundary. The server cannot reach them.",
			"Deletion does not reach payloads already delivered to a caller, exported artifacts, " +
				"or anything a participant copied elsewhere.",
		},
	}

	verification, err := VerifyDatabase(ctx, database)
	if err != nil {
		return status, err
	}
	status.Migrations = verification.Migrations
	status.IntegrityProblems = len(verification.IntegrityProblems)
	status.ForeignKeyViolations = int(verification.ForeignKeyViolations)
	status.GuardsIntact = verification.GuardDrift.Intact()
	if verification.GuardDrift.Missing != nil {
		status.MissingGuards = verification.GuardDrift.Missing
	}
	if verification.GuardDrift.Unexpected != nil {
		status.UnexpectedGuards = verification.GuardDrift.Unexpected
	}

	installed, err := ReadGuards(ctx, database)
	if err != nil {
		return status, err
	}
	status.GuardCount = len(installed.Guards)

	// The lock probe can fail on an unusual filesystem; a status report that
	// cannot answer that one question should still answer the rest.
	if holder, held, ownershipErr := InspectOwnership(databasePath); ownershipErr == nil {
		status.OwnershipHeld = held
		if held {
			status.OwnershipRole = string(holder.Role)
		}
	}

	storage, err := readStorage(ctx, database, databasePath)
	if err != nil {
		return status, err
	}
	status.Storage = storage

	if !verification.Migrations.UpToDate() {
		// Eligibility and the erasure log both read tables whose shape depends
		// on the schema. Reporting the posture of a schema this binary does not
		// know would be reporting a guess.
		return status, nil
	}

	plan, err := PlanRetention(ctx, database, windows, time.Now().UTC())
	if err != nil {
		return status, err
	}
	status.EligibleInteractions = len(plan.Interactions)
	status.EligibleSurfaces = len(plan.Surfaces)
	for _, candidate := range plan.Interactions {
		if candidate.UnredactedColumns > 0 {
			status.EligibleInteractionsWithContent++
		}
	}
	for _, candidate := range plan.Surfaces {
		if candidate.UnredactedColumns > 0 {
			status.EligibleSurfacesWithContent++
		}
	}

	history, err := RetentionHistory(ctx, database, operationLimit)
	if err != nil {
		return status, err
	}
	status.RecentOperations = history
	return status, nil
}
