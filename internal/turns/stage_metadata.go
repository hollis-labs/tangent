package turns

import (
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tangent/pkg/plugin"
)

func requestReplyable(r AgentTurnRequest) bool {
	return r.SourceMessage == nil || r.SourceMessage.Origin != "publication"
}

// JSON Schema owns structural limits. These cross-field and encoded-byte
// checks run before the first durable write, over the admitted wire values.
func validateStageMetadata(req AgentTurnRequest, value any) error {
	fields := value.(map[string]any)
	metadata := map[string]any{}
	for _, name := range []string{"annotations", "stage_trace"} {
		if field, ok := fields[name]; ok {
			metadata[name] = field
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > plugin.MaxTurnStageMetadataBytes {
		return fmt.Errorf("%w: encoded stage metadata exceeds 8 KiB", ErrInvalidRequest)
	}
	annotations := map[string]bool{}
	for _, annotation := range req.Annotations {
		if annotations[annotation.StageID] {
			return fmt.Errorf("%w: repeated annotation stage", ErrInvalidRequest)
		}
		annotations[annotation.StageID] = true
		if annotation.Summary.Text != req.Summary {
			return fmt.Errorf("%w: summary annotation must agree with summary", ErrInvalidRequest)
		}
	}
	traces := map[string]bool{}
	for _, trace := range req.StageTrace {
		if traces[trace.StageID] {
			return fmt.Errorf("%w: repeated trace stage", ErrInvalidRequest)
		}
		traces[trace.StageID] = true
		if (trace.Outcome == "timed_out") != (trace.FailureCode == "stage_timeout") {
			return fmt.Errorf("%w: timeout outcome and failure code disagree", ErrInvalidRequest)
		}
	}
	if source := req.SourceMessage; source != nil {
		agentID := source.Attribution.LogicalAgentID
		if agentID == "" {
			agentID = source.SenderURN
		}
		if req.Source.AgentID != agentID {
			return fmt.Errorf("%w: source agent attribution disagrees", ErrInvalidRequest)
		}
	}
	if source := req.SourceMessage; source != nil && source.Origin == "routed" {
		if source.SenderURN != "msg://session/local/"+req.SessionID {
			return fmt.Errorf("%w: routed sender and session disagree", ErrInvalidRequest)
		}
		kind := source.Attribution.Kind
		if kind == "final" {
			kind = "terminal"
		}
		if kind != req.Kind {
			return fmt.Errorf("%w: routed classification and item kind disagree", ErrInvalidRequest)
		}
	}
	return nil
}
