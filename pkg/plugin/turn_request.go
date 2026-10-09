package plugin

// AgentTurnSource is attribution supplied by the caller, not sender authority.
type AgentTurnSource struct {
	AgentID       string `json:"agent_id"`
	ApplicationID string `json:"application_id,omitempty"`
	AgentLabel    string `json:"agent_label,omitempty"`
}

type AgentTurnOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// AgentTurnRequest is the public turns_enqueue payload. Contract 1.1 permits
// publication-origin checkpoints without invented runtime IDs; the host validates
// this data against TurnsEnqueueInputSchema and its cross-field admission rules.
type AgentTurnRequest struct {
	ContractVersion string             `json:"contract_version"`
	TurnID          string             `json:"turn_id,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`
	IdempotencyKey  string             `json:"idempotency_key"`
	Kind            string             `json:"kind"`
	Source          AgentTurnSource    `json:"source"`
	Title           string             `json:"title"`
	Summary         string             `json:"summary,omitempty"`
	Content         string             `json:"content"`
	Annotations     []TurnAnnotation   `json:"annotations,omitempty"`
	StageTrace      []TurnStageTrace   `json:"stage_trace,omitempty"`
	SourceMessage   *TurnSourceMessage `json:"source_message,omitempty"`
	Options         []AgentTurnOption  `json:"options,omitempty"`
	Correlations    map[string]any     `json:"correlations,omitempty"`
	ExpiresAt       string             `json:"expires_at,omitempty"`
}
