package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type approvalQueueInput struct {
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

type approvalQueueSubmitDraft struct {
	QueueID      string                        `json:"queue_id"`
	CurrentIndex int                           `json:"current_index"`
	Decisions    []room.ApprovalQueueDecision  `json:"decisions"`
	Notes        string                        `json:"notes,omitempty"`
	ExportRefs   []room.ApprovalQueueExportRef `json:"export_refs,omitempty"`
}

type approvalQueueSubmitPayload struct {
	QueueID      string                        `json:"queue_id"`
	CurrentIndex int                           `json:"current_index"`
	Decisions    []room.ApprovalQueueDecision  `json:"decisions"`
	Notes        string                        `json:"notes,omitempty"`
	ExportRefs   []room.ApprovalQueueExportRef `json:"export_refs"`
}

func (s *Server) handleApprovalQueue(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args approvalQueueInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != approvalQueueEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf("tangent.approval-queue rejects envelope type %q; want %q", args.Envelope.Type, approvalQueueEnvelopeType),
		), nil, nil
	}

	roomID, reused := metaRoomID(args.Envelope.Meta)
	if !reused || roomID == "" {
		createRes, _, err := s.handleSessionCreate(ctx, nil, sessionCreateInput{
			Meta: map[string]any{
				"envelopeID":   args.Envelope.ID,
				"envelopeType": args.Envelope.Type,
			},
		})
		if err != nil {
			return nil, nil, err
		}
		if createRes.IsError {
			return createRes, nil, nil
		}
		var created sessionCreateResult
		if err := json.Unmarshal([]byte(extractToolText(createRes)), &created); err != nil {
			return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("decode session_create result: %v", err)), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("approval-queue", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("approval-queue", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := approvalQueueSnapshotFromEnvelope(args.Envelope, room.ProjectApprovalQueueState(phaseState))
	if _, saveErr := s.manager.SaveApprovalQueueSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room approval-queue state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(
		ctx,
		roomID,
		&args.Envelope,
		buildVisibleApprovalQueueEnvelope(&args.Envelope, room.ProjectApprovalQueueState(phaseState)),
		args.Completion,
	)
}

func approvalQueueSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.ApprovalQueueStateView) room.ApprovalQueueSnapshot {
	data := env.Data
	queueID := readStringValue(data, "queue_id")
	if queueID == "" && persisted != nil {
		queueID = persisted.QueueID
	}
	reusePersisted := persisted != nil && persisted.QueueID != "" && persisted.QueueID == queueID

	items := readObjectSliceValue(data["items"])
	if reusePersisted {
		items = persisted.Items
	} else if len(items) == 0 && persisted != nil {
		items = persisted.Items
	}
	currentIndex := readIntValue(data["current_index"])
	if reusePersisted {
		currentIndex = persisted.CurrentIndex
	}
	notes := readStringValue(data, "notes")
	if reusePersisted {
		notes = persisted.Notes
	}
	snapshot := room.ApprovalQueueSnapshot{
		QueueID:      queueID,
		Items:        items,
		CurrentIndex: currentIndex,
		Notes:        notes,
		UpdatedAt:    nowRFC3339(),
	}
	if reusePersisted {
		snapshot.Decisions = persisted.Decisions
		snapshot.AuditTrail = persisted.AuditTrail
		snapshot.ExportRefs = persisted.ExportRefs
	}
	return snapshot
}

func buildVisibleApprovalQueueEnvelope(
	env *envelopes.Envelope,
	view *room.ApprovalQueueStateView,
) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}

	data := cloneAnyMapForDispatch(clone.Data)
	data["queue_id"] = view.QueueID
	data["items"] = cloneObjectSliceForDispatch(view.Items)
	data["current_index"] = view.CurrentIndex
	data["decisions"] = approvalQueueDecisionsAnyForDispatch(view.Decisions)
	data["audit_trail"] = approvalQueueAuditTrailAnyForDispatch(view.AuditTrail)
	data["export_refs"] = approvalQueueExportRefsAnyForDispatch(view.ExportRefs)
	if view.Notes != "" {
		data["notes"] = view.Notes
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	clone.Data = data
	return clone
}

func (s *Server) normalizeApprovalQueueSubmitResponse(
	roomID string,
	_ *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, approvalQueueResponseValidationError("response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, approvalQueueResponseValidationError("kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, approvalQueueResponseValidationError("status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("approval-queue submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectApprovalQueueState(phaseState)
	if persisted == nil {
		return nil, approvalQueueResponseValidationError("room %q has no persisted approval-queue state", roomID)
	}

	draft, err := decodeApprovalQueueSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(draft.QueueID) == "" {
		return nil, approvalQueueResponseValidationError("payload.queue_id is required")
	}
	if draft.QueueID != persisted.QueueID {
		return nil, approvalQueueResponseValidationError("payload.queue_id %q does not match room queue_id %q", draft.QueueID, persisted.QueueID)
	}

	validItemIDs := make(map[string]struct{}, len(persisted.Items))
	itemOrder := make([]string, 0, len(persisted.Items))
	for _, item := range persisted.Items {
		itemID := strings.TrimSpace(readStringValue(item, "id"))
		if itemID == "" {
			continue
		}
		validItemIDs[itemID] = struct{}{}
		itemOrder = append(itemOrder, itemID)
	}
	decisions, err := normalizeApprovalQueueDecisionsForSubmit(draft.Decisions, validItemIDs)
	if err != nil {
		return nil, err
	}
	if len(itemOrder) > 0 && len(decisions) != len(itemOrder) {
		return nil, approvalQueueResponseValidationError("all queue items must be decided before submit")
	}
	currentIndex := draft.CurrentIndex
	if len(persisted.Items) == 0 {
		currentIndex = 0
	} else if currentIndex < 0 || currentIndex >= len(persisted.Items) {
		currentIndex = persisted.CurrentIndex
	}

	auditTrail := buildApprovalQueueAuditTrail(persisted.Decisions, persisted.AuditTrail, decisions, draft.ExportRefs)
	exportRefs := draft.ExportRefs
	if len(exportRefs) == 0 {
		exportRefs = persisted.ExportRefs
	}

	snapshot := room.ApprovalQueueSnapshot{
		QueueID:      persisted.QueueID,
		Items:        persisted.Items,
		CurrentIndex: currentIndex,
		Decisions:    decisions,
		Notes:        strings.TrimSpace(draft.Notes),
		UpdatedAt:    nowRFC3339(),
		AuditTrail:   auditTrail,
		ExportRefs:   exportRefs,
	}
	savedPhaseState, err := s.manager.SaveApprovalQueueSnapshot(roomID, snapshot)
	if err != nil {
		return nil, err
	}
	saved := room.ProjectApprovalQueueState(savedPhaseState)
	if saved == nil {
		return nil, approvalQueueResponseValidationError("room %q has no saved approval-queue state", roomID)
	}

	normalized := &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  resp.EnvelopeID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		CompletedAt: resp.CompletedAt,
		Payload: approvalQueueSubmitPayload{
			QueueID:      saved.QueueID,
			CurrentIndex: saved.CurrentIndex,
			Decisions:    saved.Decisions,
			Notes:        saved.Notes,
			ExportRefs:   saved.ExportRefs,
		},
	}
	if normalized.CompletedAt == "" {
		normalized.CompletedAt = nowRFC3339()
	}
	return normalized, nil
}

func decodeApprovalQueueSubmitDraft(raw any) (approvalQueueSubmitDraft, error) {
	if raw == nil {
		return approvalQueueSubmitDraft{}, approvalQueueResponseValidationError("payload is required")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		return approvalQueueSubmitDraft{}, approvalQueueResponseValidationError("payload must be JSON-shaped")
	}
	var draft approvalQueueSubmitDraft
	encoded, err := json.Marshal(record)
	if err != nil {
		return approvalQueueSubmitDraft{}, approvalQueueResponseValidationError("payload is invalid: %v", err)
	}
	if err := json.Unmarshal(encoded, &draft); err != nil {
		return approvalQueueSubmitDraft{}, approvalQueueResponseValidationError("payload is invalid: %v", err)
	}
	return draft, nil
}

func approvalQueueResponseValidationError(format string, args ...any) error {
	return fmt.Errorf(
		"approval-queue submit response: %w: %s",
		envelopes.ErrSchemaValidation,
		fmt.Sprintf(format, args...),
	)
}

func normalizeApprovalQueueDecisionsForSubmit(
	items []room.ApprovalQueueDecision,
	validItemIDs map[string]struct{},
) ([]room.ApprovalQueueDecision, error) {
	if len(items) == 0 {
		return []room.ApprovalQueueDecision{}, nil
	}
	out := make([]room.ApprovalQueueDecision, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		itemID := strings.TrimSpace(item.ItemID)
		if itemID == "" {
			return nil, approvalQueueResponseValidationError("payload.decisions.item_id is required")
		}
		if _, ok := validItemIDs[itemID]; !ok {
			return nil, approvalQueueResponseValidationError("payload.decisions contains unknown item_id %q", itemID)
		}
		if _, exists := seen[itemID]; exists {
			return nil, approvalQueueResponseValidationError("payload.decisions contains duplicate item_id %q", itemID)
		}
		seen[itemID] = struct{}{}
		decision := normalizeApprovalQueueDecisionValue(item.Decision)
		if decision == "" {
			return nil, approvalQueueResponseValidationError("payload.decisions[%q].decision is invalid", itemID)
		}
		deferReason := strings.TrimSpace(item.DeferReason)
		if decision == "defer" && deferReason == "" {
			return nil, approvalQueueResponseValidationError("payload.decisions[%q].defer_reason is required", itemID)
		}
		out = append(out, room.ApprovalQueueDecision{
			ItemID:      itemID,
			Decision:    decision,
			Comment:     strings.TrimSpace(item.Comment),
			ActionID:    strings.TrimSpace(item.ActionID),
			DeferReason: deferReason,
			DecidedAt:   strings.TrimSpace(item.DecidedAt),
		})
	}
	now := nowRFC3339()
	for i := range out {
		if out[i].DecidedAt == "" {
			out[i].DecidedAt = now
		}
	}
	return out, nil
}

func buildApprovalQueueAuditTrail(
	previous []room.ApprovalQueueDecision,
	existing []room.ApprovalQueueAuditEntry,
	decisions []room.ApprovalQueueDecision,
	exportRefs []room.ApprovalQueueExportRef,
) []room.ApprovalQueueAuditEntry {
	out := append([]room.ApprovalQueueAuditEntry{}, existing...)
	prevByID := map[string]room.ApprovalQueueDecision{}
	for _, item := range previous {
		prevByID[item.ItemID] = item
	}
	for _, item := range decisions {
		prev, ok := prevByID[item.ItemID]
		if ok &&
			prev.Decision == item.Decision &&
			prev.Comment == item.Comment &&
			prev.ActionID == item.ActionID &&
			prev.DeferReason == item.DeferReason {
			continue
		}
		out = append(out, room.ApprovalQueueAuditEntry{
			ItemID:      item.ItemID,
			Event:       "decision_set",
			Decision:    item.Decision,
			Comment:     item.Comment,
			ActionID:    item.ActionID,
			DeferReason: item.DeferReason,
			At:          item.DecidedAt,
		})
	}
	for _, item := range exportRefs {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		found := false
		for _, existingRef := range out {
			if existingRef.Event == "audit_exported" && existingRef.Comment == item.Name && existingRef.At == item.CreatedAt {
				found = true
				break
			}
		}
		if found {
			continue
		}
		out = append(out, room.ApprovalQueueAuditEntry{
			Event:   "audit_exported",
			Comment: item.Name,
			At:      item.CreatedAt,
		})
	}
	return out
}

func approvalQueueDecisionsAnyForDispatch(items []room.ApprovalQueueDecision) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"item_id":  item.ItemID,
			"decision": item.Decision,
		}
		if item.Comment != "" {
			record["comment"] = item.Comment
		}
		if item.ActionID != "" {
			record["action_id"] = item.ActionID
		}
		if item.DeferReason != "" {
			record["defer_reason"] = item.DeferReason
		}
		if item.DecidedAt != "" {
			record["decided_at"] = item.DecidedAt
		}
		out = append(out, record)
	}
	return out
}

func approvalQueueAuditTrailAnyForDispatch(items []room.ApprovalQueueAuditEntry) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"event": item.Event,
		}
		if item.ItemID != "" {
			record["item_id"] = item.ItemID
		}
		if item.Decision != "" {
			record["decision"] = item.Decision
		}
		if item.Comment != "" {
			record["comment"] = item.Comment
		}
		if item.ActionID != "" {
			record["action_id"] = item.ActionID
		}
		if item.DeferReason != "" {
			record["defer_reason"] = item.DeferReason
		}
		if item.At != "" {
			record["at"] = item.At
		}
		out = append(out, record)
	}
	return out
}

func approvalQueueExportRefsAnyForDispatch(items []room.ApprovalQueueExportRef) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			"name": item.Name,
		}
		if item.CreatedAt != "" {
			record["created_at"] = item.CreatedAt
		}
		if item.ItemCount > 0 {
			record["item_count"] = item.ItemCount
		}
		if item.DecisionCount > 0 {
			record["decision_count"] = item.DecisionCount
		}
		if item.SizeBytes > 0 {
			record["size_bytes"] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}

func readIntValue(raw any) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func normalizeApprovalQueueDecisionValue(raw string) string {
	switch strings.TrimSpace(raw) {
	case "accept":
		return "accept"
	case "reject":
		return "reject"
	case "defer":
		return "defer"
	default:
		return ""
	}
}
