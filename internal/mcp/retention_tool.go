package mcp

import (
	"context"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// tangent.retention_status is the read half of CW-20260825-0072, and it is
// deliberately the only half.
//
// The six retention operations — capability expiry, draft deletion, payload
// redaction, surface close, surface purge, and the external-source refusal —
// are CLI commands and nothing else. ADR 0002 §4 puts erasure authority with
// the local user ("explicit, audited erasure operation initiated by the local
// user"), and every one of these commands either removes a participant's answer
// or destroys a record skeleton. An MCP tool is reachable by any agent that can
// reach this host, over a transport with no authenticated caller identity
// (ADR 0001 and ADR 0004 both defer that), so exposing them here would hand
// deletion authority to whatever is on the other end of the socket.
//
// What an agent legitimately needs is the posture: is this database still
// enforcing its own immutability, what is eligible for a window sweep, what has
// been erased recently, and what can retention not reach. That is answerable
// without any authority at all, and it is what this tool returns.
//
// It carries no payload, no participant text, no filesystem path, no hostname,
// and no process identity — internal/db.Status is shaped to ADR 0002 §8's floor
// rather than filtered down to it.

type retentionStatusInput struct {
	// OperationLimit bounds how many recent retention operations come back.
	// Zero takes the default.
	OperationLimit int `json:"operation_limit,omitempty"`
}

func (s *Server) registerRetentionTool() error {
	return addInteractionTool(s, "tangent.retention_status",
		"Report this host's data-custody posture: whether the database still enforces its "+
			"immutability guards, the schema and storage state, whether a process holds the "+
			"single-writer lock, the host retention windows, how many interactions and surfaces "+
			"are past them, the recent erasure log, and the standing limits on what deletion can "+
			"reach. Read-only: the retention operations themselves are operator commands on the "+
			"machine, not tools. Carries no payload, participant text, path, or process identity.",
		s.handleRetentionStatus)
}

func (s *Server) handleRetentionStatus(
	ctx context.Context,
	input retentionStatusInput,
) (any, error) {
	if s.maintenanceDB == nil {
		// An error result rather than a synthesized empty one, for the reason
		// tangent.health_report gives: a caller that receives a custody report
		// assumes something measured it.
		return nil, toolErrorResult("retention_unavailable",
			"this build serves MCP without a database handle for maintenance reporting, so the "+
				"custody posture cannot be measured; deploy a build that wires it")
	}
	limit := input.OperationLimit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	status, err := tangentdb.Status(ctx, s.maintenanceDB, s.maintenanceDBPath,
		tangentdb.DefaultRetentionWindows(), limit)
	if err != nil {
		return nil, toolErrorResult("retention_unavailable", err.Error())
	}
	return status, nil
}
