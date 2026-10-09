package plugin

// TurnAnnotation is a bounded, versioned stage result. Summary is plain text,
// never a command, response option, or authority to act.
type TurnAnnotation struct {
	SchemaVersion int         `json:"schema_version"`
	StageID       string      `json:"stage_id"`
	StageVersion  string      `json:"stage_version"`
	Kind          string      `json:"kind"`
	Summary       TurnSummary `json:"summary"`
}

type TurnSummary struct {
	Text string `json:"text"`
}

// TurnStageTrace carries operational metadata, not raw diagnostics or reasoning.
type TurnStageTrace struct {
	StageID      string `json:"stage_id"`
	StageVersion string `json:"stage_version"`
	Outcome      string `json:"outcome"`
	DurationMS   int64  `json:"duration_ms"`
	FailureCode  string `json:"failure_code,omitempty"`
}

// TurnSourceMessage identifies a source publication without authenticating its
// sender. Ordinary publications have no runtime reply target.
type TurnSourceMessage struct {
	SchemaVersion int                   `json:"schema_version"`
	Origin        string                `json:"origin"`
	EndpointRef   string                `json:"endpoint_ref"`
	Channel       string                `json:"channel"`
	MessageID     string                `json:"message_id"`
	Sequence      int64                 `json:"sequence"`
	SenderURN     string                `json:"sender_urn"`
	OutputID      string                `json:"output_id,omitempty"`
	Attribution   TurnSourceAttribution `json:"attribution,omitempty"`
}

type TurnSourceAttribution struct {
	Kind              string `json:"kind,omitempty"`
	LogicalAgentID    string `json:"logical_agent_id,omitempty"`
	ProjectID         string `json:"project_id,omitempty"`
	WorkstreamID      string `json:"workstream_id,omitempty"`
	LaunchID          string `json:"launch_id,omitempty"`
	LaunchDisplayName string `json:"launch_display_name,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	StopReason        string `json:"stop_reason,omitempty"`
	Confidence        string `json:"confidence,omitempty"`
}

const MaxTurnStageMetadataBytes = 8 * 1024
