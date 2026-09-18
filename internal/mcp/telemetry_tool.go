package mcp

import (
	"context"
	"time"

	"github.com/hollis-labs/tangent/internal/telemetry"
)

// tangent.telemetry_query is the query surface criterion 4 asks for: audit
// records readable without touching a process log.
//
// It is an MCP tool rather than an HTTP route for the same reason
// tangent.health_report is. Tether publishes Tangent as an MCP upstream and
// Cerberus owns the process; an agent reaching Tangent that way speaks
// JSON-RPC over /sse and has no reason to hold an HTTP client. Making it grep
// stderr — which it cannot reach either — is how "the delivery is lagging"
// becomes "the tool is flaky".
//
// The three questions it answers are the three an operator actually asks:
//
//   - "What happened to this one invocation?" — a trace id, which every
//     non-passing health report already hands over, and which
//     internal/telemetry derives from the durable record so it can be
//     recomputed rather than remembered.
//   - "What is this installation doing?" — the durable aggregate, which
//     survives the restart that emptied the in-process counters.
//   - "What has this process seen?" — the metric snapshot, which is where the
//     latency distributions live.
//
// Everything it returns is an identifier, an enumerated state, a typed code, or
// a number. There is nothing in a response that could carry a payload, because
// there is nothing in the table that could.

type telemetryQueryInput struct {
	// TraceID selects one invocation's whole story. It is the value a health
	// report's `correlation.trace_id` carries.
	TraceID string `json:"trace_id,omitempty"`
	// InteractionID selects one durable interaction. Use it when you have a
	// handle from a pending receipt rather than a trace.
	InteractionID string `json:"interaction_id,omitempty"`
	// RoomID selects one surface's connection lifecycle.
	RoomID string `json:"room_id,omitempty"`
	// EventName narrows to one observation kind, e.g. `presentation.refused`.
	EventName string `json:"event_name,omitempty"`
	// Outcome narrows to ok, refused, or failed.
	Outcome string `json:"outcome,omitempty"`
	// SinceMinutes bounds how far back to look. Zero means no lower bound.
	SinceMinutes int64 `json:"since_minutes,omitempty"`
	// Limit bounds the number of observations returned.
	Limit int `json:"limit,omitempty"`
	// IncludeMetrics adds the in-process metric snapshot. It is off by default
	// because it is large and answers a different question from the events.
	IncludeMetrics bool `json:"include_metrics,omitempty"`
	// IncludeAggregate adds the durable per-event summary.
	IncludeAggregate bool `json:"include_aggregate,omitempty"`
}

type telemetryQueryResult struct {
	// Events are the matching observations. When a trace id was supplied they
	// are ordered oldest first, because a trace is a story; otherwise newest
	// first, because a search is a search.
	Events []telemetry.Record `json:"events"`
	// Truncated says the limit was reached and there is more.
	Truncated bool `json:"truncated"`

	Aggregate []telemetry.AggregateRow `json:"aggregate,omitempty"`
	Metrics   *telemetry.Snapshot      `json:"metrics,omitempty"`

	// Note is present when part of the answer could not be produced, so a
	// caller is never left inferring from an empty list that nothing happened.
	Note string `json:"note,omitempty"`
}

func (s *Server) registerTelemetryTool() error {
	return addInteractionTool(s, "tangent.telemetry_query",
		"Read Tangent's durable interaction telemetry: every observation filed under one "+
			"correlation trace, or under one interaction, room, event name, or outcome. A "+
			"non-passing tangent.health_report carries the trace id to pass here. Optionally "+
			"returns the durable per-event aggregate and this process's metric snapshot "+
			"(presentation and resolution latency, reconnects, stale clients, delivery lag and "+
			"retries, draft conflicts, renderer failures, capability denials). Carries no "+
			"payload, participant text, path, session, effect handle, or URL — by construction, "+
			"not by filtering.",
		s.handleTelemetryQuery)
}

func (s *Server) handleTelemetryQuery(
	ctx context.Context,
	input telemetryQueryInput,
) (any, error) {
	if s.telemetry == nil {
		// An error result rather than an empty one, for the reason
		// tangent.health_report gives: a caller that receives a telemetry
		// document assumes something recorded it, and answering with one that
		// recorded nothing is the same class of lie as a 200 from a process
		// whose database is gone.
		return nil, toolErrorResult("telemetry_unavailable",
			"this build serves MCP without a telemetry recorder installed, so no interaction "+
				"correlation was recorded; deploy a build that wires it")
	}
	result := telemetryQueryResult{Events: []telemetry.Record{}}
	if input.IncludeMetrics {
		snapshot := s.telemetry.Metrics().Snapshot()
		result.Metrics = &snapshot
	}
	if s.telemetryStore == nil {
		result.Note = "this build records metrics in process memory but has no durable telemetry " +
			"store, so no per-invocation observation can be retrieved; the metric snapshot is the " +
			"whole of what it can answer"
		return result, nil
	}

	query := telemetry.Query{
		TraceID:       input.TraceID,
		InteractionID: input.InteractionID,
		RoomID:        input.RoomID,
		EventName:     input.EventName,
		Outcome:       input.Outcome,
		Limit:         input.Limit,
	}
	if input.SinceMinutes > 0 {
		query.Since = time.Now().UTC().Add(-time.Duration(input.SinceMinutes) * time.Minute)
	}

	var (
		records []telemetry.Record
		err     error
	)
	if input.TraceID != "" {
		trace, parsed := telemetry.ParseTraceID(input.TraceID)
		if !parsed {
			return nil, toolErrorResult("telemetry_invalid_trace",
				"a trace id is 32 hexadecimal characters; copy it verbatim from a health "+
					"report's correlation block")
		}
		records, err = s.telemetryStore.Trace(ctx, trace, input.Limit)
	} else {
		records, err = s.telemetryStore.Query(ctx, query)
	}
	if err != nil {
		// The store's errors never carry the driver's message, so this is safe
		// to hand back verbatim — and it is the store's own text rather than a
		// summary, so a reader can tell a query failure from an empty result.
		return nil, toolErrorResult("telemetry_unavailable", err.Error())
	}
	result.Events = records
	result.Truncated = len(records) >= boundedLimit(input.Limit)

	if input.IncludeAggregate {
		aggregate, aggregateErr := s.telemetryStore.Aggregate(ctx, query.Since)
		if aggregateErr != nil {
			result.Note = "the durable aggregate could not be read; the listed observations are " +
				"unaffected"
		} else {
			result.Aggregate = aggregate
		}
	}
	return result, nil
}

// boundedLimit mirrors the store's own clamp so `truncated` means what it says.
func boundedLimit(limit int) int {
	switch {
	case limit <= 0:
		return telemetry.DefaultQueryLimit
	case limit > telemetry.MaxQueryLimit:
		return telemetry.MaxQueryLimit
	}
	return limit
}
