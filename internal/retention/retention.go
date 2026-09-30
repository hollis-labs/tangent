// Package retention is the service layer behind tangent.retention_status.
//
// It exists so the MCP transport reports the custody posture without holding a
// database handle or importing internal/db (the transport-boundary rule in
// .golangci.transport.yml): the tool decodes its input, calls Status, and
// encodes the answer. The posture itself is computed by internal/db.Status,
// which is shaped to ADR 0002 §8's floor rather than filtered down to it.
//
// Read-only, and deliberately only the read half. The retention *operations*
// are operator commands on the machine, never tools, because erasure authority
// belongs to the local user and MCP has no authenticated caller identity to
// hold it (see internal/mcp/retention_tool.go).
package retention

import (
	"context"
	"database/sql"
	"errors"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// Reporter answers the custody posture for one database.
type Reporter struct {
	// database is the same handle everything else shares: a custody posture
	// read from a different connection could describe a different
	// transaction.
	database *sql.DB
	// databasePath is used for exactly one thing — probing the single-writer
	// lock sidecar — and never leaves this process: the status says whether
	// the lock is held and by which role, never where it lives.
	databasePath string
	windows      tangentdb.RetentionWindows
}

// NewReporter returns a Reporter over database, using the host's default
// retention windows.
func NewReporter(database *sql.DB, databasePath string) (*Reporter, error) {
	if database == nil {
		return nil, errors.New("retention: database handle is nil")
	}
	return &Reporter{
		database:     database,
		databasePath: databasePath,
		windows:      tangentdb.DefaultRetentionWindows(),
	}, nil
}

// Status reports the custody posture, with at most operationLimit recent
// retention operations. It writes nothing and takes no lock.
func (r *Reporter) Status(ctx context.Context, operationLimit int) (tangentdb.RetentionStatus, error) {
	return tangentdb.Status(ctx, r.database, r.databasePath, r.windows, operationLimit)
}
