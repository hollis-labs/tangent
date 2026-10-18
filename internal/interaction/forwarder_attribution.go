package interaction

import "context"

// ForwarderAttribution is bounded evidence asserted by a credential-authenticated
// host-trusted forwarder. It is kept separately from request/metadata labels and
// never participates in authorization or idempotency. Session authority is
// already bound by the transport before this context is installed.
type ForwarderAttribution struct {
	Forwarder      string `json:"forwarder"`
	SessionID      string `json:"session_id"`
	AgentURN       string `json:"agent_urn,omitempty"`
	WorkstreamID   string `json:"workstream_id,omitempty"`
	LaunchID       string `json:"launch_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	LogicalAgentID string `json:"logical_agent_id,omitempty"`
}

type forwarderAttributionKey struct{}

// WithForwarderAttribution carries host-owned evidence into admission audit
// events. A tool argument cannot install this private context value.
func WithForwarderAttribution(ctx context.Context, value ForwarderAttribution) context.Context {
	return context.WithValue(ctx, forwarderAttributionKey{}, value)
}

func forwarderAdmissionMetadata(ctx context.Context, metadata map[string]any) string {
	if metadata == nil {
		metadata = map[string]any{}
	}
	value, ok := ctx.Value(forwarderAttributionKey{}).(ForwarderAttribution)
	if ok {
		metadata["forwarder_attribution"] = value
	}
	return mustJSON(metadata)
}
