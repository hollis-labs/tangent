package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/health"
)

// tangent.health_report is the MCP half of CW-20260825-0066's three probes.
//
// It exists because the HTTP probes are not reachable by every consumer that
// needs them. Tether publishes Tangent as an MCP upstream
// (~/.tether/catalog/mcp-servers/tangent.yaml) and Cerberus owns the process;
// an agent reaching Tangent through that upstream speaks JSON-RPC over /sse and
// has no reason to hold an HTTP client. Making it re-derive readiness from a
// failed tool call is how "the database is gone" becomes "the workflow is
// flaky", so readiness is reported over the same channel the work arrives on.
//
// It mirrors the HTTP surface exactly rather than computing anything of its
// own: same reporter, same vocabulary, same bounds. Two health implementations
// that can disagree is the defect this task closed, not one to reintroduce at
// a second transport.

type healthReportInput struct {
	// Kind narrows the capability half to one requested interaction kind. When
	// empty the bounded summary over every kind is returned instead.
	Kind string `json:"kind,omitempty"`
}

type healthReportResult struct {
	HostVersion     string `json:"host_version"`
	ProtocolVersion string `json:"protocol_version"`

	Liveness  health.LivenessReport  `json:"liveness"`
	Readiness health.ReadinessReport `json:"readiness"`

	// Capability is populated when a kind was requested, CapabilitySummary
	// when one was not. Exactly one of them is ever set: a caller asking about
	// one kind should not have to page past eighteen others to find it.
	Capability        *health.CapabilityReport        `json:"capability,omitempty"`
	CapabilitySummary *health.CapabilitySummaryReport `json:"capability_summary,omitempty"`

	// Plugins is which plugins the host loaded and which it refused
	// (CW-20260910-0036). It rides on every report rather than behind a flag:
	// it is three rows in this build, the readiness section already carries the
	// pass/fail, and the whole point is that an operator should not have to
	// infer the answer from a tool list.
	Plugins health.PluginInventory `json:"plugins"`
}

func (s *Server) registerHealthTool() error {
	return addInteractionTool(s, "tangent.health_report",
		"Report this host's operability as three distinct answers: liveness (is the process "+
			"responding), readiness (database, migrations, definition registry, renderer host, "+
			"delivery worker), and capability health for one requested interaction kind or, with "+
			"no kind, a bounded summary over all of them. Also reports which plugins loaded and "+
			"which refused and why, so that is read rather than inferred from a tool list. Every "+
			"non-passing check carries the operator action that fixes it. Payload-bounded; "+
			"carries no participant content, path, or session.",
		s.handleHealthReport)
}

func (s *Server) handleHealthReport(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input healthReportInput,
) (*mcpsdk.CallToolResult, any, error) {
	if s.health == nil {
		// Deliberately an error result rather than a synthesized "unknown"
		// report. A caller that receives a health document assumes something
		// measured it; answering with one that measured nothing is the same
		// class of lie as a 200 from a process whose database is gone.
		return toolErrorResult("health_unavailable",
			"this build serves MCP without a health reporter installed, so readiness and "+
				"capability health cannot be evaluated; deploy a build that wires it"), nil, nil
	}
	result := healthReportResult{
		HostVersion:     HostVersion,
		ProtocolVersion: envelope.ProtocolVersion,
		Liveness:        health.Live(),
		Readiness:       s.health.Readiness(ctx),
		Plugins:         s.health.Plugins(),
	}
	if input.Kind != "" {
		report := s.health.Capability(input.Kind)
		result.Capability = &report
		return nil, result, nil
	}
	summary := s.health.CapabilitySummary()
	result.CapabilitySummary = &summary
	return nil, result, nil
}
