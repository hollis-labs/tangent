package room

import (
	"fmt"
	"strings"
)

const (
	ApprovalQueuePhaseID                = "approval-queue"
	approvalQueueIDKey                  = "queue_id"
	approvalQueueItemsKey               = "items"
	approvalQueueCurrentIndexKey        = "current_index"
	approvalQueueDecisionsKey           = "decisions"
	approvalQueueNotesKey               = "notes"
	approvalQueueUpdatedAtKey           = "updated_at"
	approvalQueueAuditTrailKey          = "audit_trail"
	approvalQueueExportRefsKey          = "export_refs"
	approvalQueueDecisionItemIDKey      = "item_id"
	approvalQueueDecisionValueKey       = "decision"
	approvalQueueDecisionCommentKey     = "comment"
	approvalQueueDecisionActionIDKey    = "action_id"
	approvalQueueDecisionDeferReasonKey = "defer_reason"
	approvalQueueDecisionDecidedAtKey   = "decided_at"
	approvalQueueAuditItemIDKey         = "item_id"
	approvalQueueAuditEventKey          = "event"
	approvalQueueAuditDecisionKey       = "decision"
	approvalQueueAuditCommentKey        = "comment"
	approvalQueueAuditActionIDKey       = "action_id"
	approvalQueueAuditDeferReasonKey    = "defer_reason"
	approvalQueueAuditTimestampKey      = "at"
	approvalQueueExportNameKey          = "name"
	approvalQueueExportCreatedAtKey     = "created_at"
	approvalQueueExportItemCountKey     = "item_count"
	approvalQueueExportDecisionCountKey = "decision_count"
	approvalQueueExportSizeBytesKey     = "size_bytes"
)

type ApprovalQueueDecision struct {
	ItemID      string `json:"item_id"`
	Decision    string `json:"decision"`
	Comment     string `json:"comment,omitempty"`
	ActionID    string `json:"action_id,omitempty"`
	DeferReason string `json:"defer_reason,omitempty"`
	DecidedAt   string `json:"decided_at,omitempty"`
}

type ApprovalQueueAuditEntry struct {
	ItemID      string `json:"item_id,omitempty"`
	Event       string `json:"event"`
	Decision    string `json:"decision,omitempty"`
	Comment     string `json:"comment,omitempty"`
	ActionID    string `json:"action_id,omitempty"`
	DeferReason string `json:"defer_reason,omitempty"`
	At          string `json:"at,omitempty"`
}

type ApprovalQueueExportRef struct {
	Name          string `json:"name"`
	CreatedAt     string `json:"created_at,omitempty"`
	ItemCount     int    `json:"item_count,omitempty"`
	DecisionCount int    `json:"decision_count,omitempty"`
	SizeBytes     int    `json:"size_bytes,omitempty"`
}

type ApprovalQueueStateView struct {
	QueueID      string                    `json:"queue_id"`
	Items        []map[string]any          `json:"items"`
	CurrentIndex int                       `json:"current_index"`
	Decisions    []ApprovalQueueDecision   `json:"decisions"`
	Notes        string                    `json:"notes,omitempty"`
	UpdatedAt    string                    `json:"updated_at,omitempty"`
	AuditTrail   []ApprovalQueueAuditEntry `json:"audit_trail"`
	ExportRefs   []ApprovalQueueExportRef  `json:"export_refs"`
}

type ApprovalQueueSnapshot struct {
	QueueID      string
	Items        []map[string]any
	CurrentIndex int
	Decisions    []ApprovalQueueDecision
	Notes        string
	UpdatedAt    string
	AuditTrail   []ApprovalQueueAuditEntry
	ExportRefs   []ApprovalQueueExportRef
}

func (r *Room) SaveApprovalQueueSnapshot(snapshot ApprovalQueueSnapshot) error {
	normalized, err := normalizeApprovalQueueSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob, err := approvalQueueBlobFromSnapshot(nextOutputs[ApprovalQueuePhaseID], normalized)
	if err != nil {
		return err
	}
	nextOutputs[ApprovalQueuePhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveApprovalQueueSnapshot(roomID string, snapshot ApprovalQueueSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveApprovalQueueSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectApprovalQueueState(state PhaseState) *ApprovalQueueStateView {
	return projectApprovalQueueStateFromBlob(state.PhaseOutputs[ApprovalQueuePhaseID])
}

func projectApprovalQueueStateFromBlob(blob PhaseOutput) *ApprovalQueueStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	queueID := readString(blob.Data, approvalQueueIDKey)
	if queueID == "" {
		return nil
	}
	view := &ApprovalQueueStateView{
		QueueID:      queueID,
		Items:        readObjectSlice(blob.Data[approvalQueueItemsKey]),
		CurrentIndex: int(readNumber(blob.Data, approvalQueueCurrentIndexKey)),
		Decisions:    readApprovalQueueDecisions(blob.Data[approvalQueueDecisionsKey]),
		Notes:        readString(blob.Data, approvalQueueNotesKey),
		UpdatedAt:    readString(blob.Data, approvalQueueUpdatedAtKey),
		AuditTrail:   readApprovalQueueAuditTrail(blob.Data[approvalQueueAuditTrailKey]),
		ExportRefs:   readApprovalQueueExportRefs(blob.Data[approvalQueueExportRefsKey]),
	}
	if view.Items == nil {
		view.Items = []map[string]any{}
	}
	if len(view.Items) == 0 {
		view.CurrentIndex = 0
	} else if view.CurrentIndex < 0 || view.CurrentIndex >= len(view.Items) {
		view.CurrentIndex = len(view.Items) - 1
	}
	if view.Decisions == nil {
		view.Decisions = []ApprovalQueueDecision{}
	}
	if view.AuditTrail == nil {
		view.AuditTrail = []ApprovalQueueAuditEntry{}
	}
	if view.ExportRefs == nil {
		view.ExportRefs = []ApprovalQueueExportRef{}
	}
	return view
}

func normalizeApprovalQueueSnapshot(snapshot ApprovalQueueSnapshot) (ApprovalQueueSnapshot, error) {
	queueID := strings.TrimSpace(snapshot.QueueID)
	if queueID == "" {
		return ApprovalQueueSnapshot{}, ErrInvalidApprovalQueueID
	}
	items, err := normalizeSpreadsheetObjectSlice(snapshot.Items, ErrInvalidApprovalQueueItem)
	if err != nil {
		return ApprovalQueueSnapshot{}, err
	}
	itemIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		itemID := strings.TrimSpace(readString(item, "id"))
		if itemID == "" {
			return ApprovalQueueSnapshot{}, ErrInvalidApprovalQueueItem
		}
		if _, exists := itemIDs[itemID]; exists {
			return ApprovalQueueSnapshot{}, fmt.Errorf("%w: duplicate %q", ErrInvalidApprovalQueueItem, itemID)
		}
		itemIDs[itemID] = struct{}{}
	}
	decisions, err := normalizeApprovalQueueDecisions(snapshot.Decisions, itemIDs)
	if err != nil {
		return ApprovalQueueSnapshot{}, err
	}
	auditTrail, err := normalizeApprovalQueueAuditTrail(snapshot.AuditTrail)
	if err != nil {
		return ApprovalQueueSnapshot{}, err
	}
	exportRefs, err := normalizeApprovalQueueExportRefs(snapshot.ExportRefs)
	if err != nil {
		return ApprovalQueueSnapshot{}, err
	}
	currentIndex := snapshot.CurrentIndex
	switch {
	case len(items) == 0:
		currentIndex = 0
	case currentIndex < 0:
		currentIndex = 0
	case currentIndex >= len(items):
		currentIndex = len(items) - 1
	}
	return ApprovalQueueSnapshot{
		QueueID:      queueID,
		Items:        items,
		CurrentIndex: currentIndex,
		Decisions:    decisions,
		Notes:        strings.TrimSpace(snapshot.Notes),
		UpdatedAt:    strings.TrimSpace(snapshot.UpdatedAt),
		AuditTrail:   auditTrail,
		ExportRefs:   exportRefs,
	}, nil
}

func approvalQueueBlobFromSnapshot(blob PhaseOutput, snapshot ApprovalQueueSnapshot) (PhaseOutput, error) {
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return PhaseOutput{}, fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, ApprovalQueuePhaseID)
	}
	blob.Data = map[string]any{
		approvalQueueIDKey:           snapshot.QueueID,
		approvalQueueItemsKey:        cloneObjectSlice(snapshot.Items),
		approvalQueueCurrentIndexKey: snapshot.CurrentIndex,
		approvalQueueDecisionsKey:    approvalQueueDecisionsAny(snapshot.Decisions),
		approvalQueueNotesKey:        snapshot.Notes,
		approvalQueueUpdatedAtKey:    snapshot.UpdatedAt,
		approvalQueueAuditTrailKey:   approvalQueueAuditTrailAny(snapshot.AuditTrail),
		approvalQueueExportRefsKey:   approvalQueueExportRefsAny(snapshot.ExportRefs),
	}
	return blob, nil
}

func normalizeApprovalQueueDecisions(items []ApprovalQueueDecision, validItemIDs map[string]struct{}) ([]ApprovalQueueDecision, error) {
	if len(items) == 0 {
		return []ApprovalQueueDecision{}, nil
	}
	out := make([]ApprovalQueueDecision, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		itemID := strings.TrimSpace(item.ItemID)
		if itemID == "" {
			return nil, ErrInvalidApprovalQueueDecision
		}
		if len(validItemIDs) > 0 {
			if _, ok := validItemIDs[itemID]; !ok {
				return nil, fmt.Errorf("%w: unknown item_id %q", ErrInvalidApprovalQueueDecision, itemID)
			}
		}
		if _, exists := seen[itemID]; exists {
			return nil, fmt.Errorf("%w: duplicate item_id %q", ErrInvalidApprovalQueueDecision, itemID)
		}
		seen[itemID] = struct{}{}
		decision := normalizeApprovalQueueDecisionValue(item.Decision)
		if decision == "" {
			return nil, ErrInvalidApprovalQueueDecision
		}
		deferReason := strings.TrimSpace(item.DeferReason)
		if decision == "defer" && deferReason == "" {
			return nil, ErrInvalidApprovalQueueDecision
		}
		out = append(out, ApprovalQueueDecision{
			ItemID:      itemID,
			Decision:    decision,
			Comment:     strings.TrimSpace(item.Comment),
			ActionID:    strings.TrimSpace(item.ActionID),
			DeferReason: deferReason,
			DecidedAt:   strings.TrimSpace(item.DecidedAt),
		})
	}
	return out, nil
}

func normalizeApprovalQueueAuditTrail(items []ApprovalQueueAuditEntry) ([]ApprovalQueueAuditEntry, error) {
	if len(items) == 0 {
		return []ApprovalQueueAuditEntry{}, nil
	}
	out := make([]ApprovalQueueAuditEntry, 0, len(items))
	for _, item := range items {
		event := strings.TrimSpace(item.Event)
		if event == "" {
			return nil, ErrInvalidApprovalQueueAuditEntry
		}
		decision := normalizeApprovalQueueDecisionValue(item.Decision)
		if item.Decision != "" && decision == "" {
			return nil, ErrInvalidApprovalQueueAuditEntry
		}
		if decision == "defer" && strings.TrimSpace(item.DeferReason) == "" {
			return nil, ErrInvalidApprovalQueueAuditEntry
		}
		out = append(out, ApprovalQueueAuditEntry{
			ItemID:      strings.TrimSpace(item.ItemID),
			Event:       event,
			Decision:    decision,
			Comment:     strings.TrimSpace(item.Comment),
			ActionID:    strings.TrimSpace(item.ActionID),
			DeferReason: strings.TrimSpace(item.DeferReason),
			At:          strings.TrimSpace(item.At),
		})
	}
	return out, nil
}

func normalizeApprovalQueueExportRefs(items []ApprovalQueueExportRef) ([]ApprovalQueueExportRef, error) {
	if len(items) == 0 {
		return []ApprovalQueueExportRef{}, nil
	}
	out := make([]ApprovalQueueExportRef, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" || item.ItemCount < 0 || item.DecisionCount < 0 || item.SizeBytes < 0 {
			return nil, ErrInvalidApprovalQueueExportRef
		}
		out = append(out, ApprovalQueueExportRef{
			Name:          name,
			CreatedAt:     strings.TrimSpace(item.CreatedAt),
			ItemCount:     item.ItemCount,
			DecisionCount: item.DecisionCount,
			SizeBytes:     item.SizeBytes,
		})
	}
	return out, nil
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

func readApprovalQueueDecisions(raw any) []ApprovalQueueDecision {
	records := readObjectSlice(raw)
	out := make([]ApprovalQueueDecision, 0, len(records))
	for _, record := range records {
		decision := normalizeApprovalQueueDecisionValue(readString(record, approvalQueueDecisionValueKey))
		itemID := strings.TrimSpace(readString(record, approvalQueueDecisionItemIDKey))
		if itemID == "" || decision == "" {
			continue
		}
		out = append(out, ApprovalQueueDecision{
			ItemID:      itemID,
			Decision:    decision,
			Comment:     strings.TrimSpace(readString(record, approvalQueueDecisionCommentKey)),
			ActionID:    strings.TrimSpace(readString(record, approvalQueueDecisionActionIDKey)),
			DeferReason: strings.TrimSpace(readString(record, approvalQueueDecisionDeferReasonKey)),
			DecidedAt:   strings.TrimSpace(readString(record, approvalQueueDecisionDecidedAtKey)),
		})
	}
	return out
}

func readApprovalQueueAuditTrail(raw any) []ApprovalQueueAuditEntry {
	records := readObjectSlice(raw)
	out := make([]ApprovalQueueAuditEntry, 0, len(records))
	for _, record := range records {
		event := strings.TrimSpace(readString(record, approvalQueueAuditEventKey))
		if event == "" {
			continue
		}
		out = append(out, ApprovalQueueAuditEntry{
			ItemID:      strings.TrimSpace(readString(record, approvalQueueAuditItemIDKey)),
			Event:       event,
			Decision:    normalizeApprovalQueueDecisionValue(readString(record, approvalQueueAuditDecisionKey)),
			Comment:     strings.TrimSpace(readString(record, approvalQueueAuditCommentKey)),
			ActionID:    strings.TrimSpace(readString(record, approvalQueueAuditActionIDKey)),
			DeferReason: strings.TrimSpace(readString(record, approvalQueueAuditDeferReasonKey)),
			At:          strings.TrimSpace(readString(record, approvalQueueAuditTimestampKey)),
		})
	}
	return out
}

func readApprovalQueueExportRefs(raw any) []ApprovalQueueExportRef {
	records := readObjectSlice(raw)
	out := make([]ApprovalQueueExportRef, 0, len(records))
	for _, record := range records {
		name := strings.TrimSpace(readString(record, approvalQueueExportNameKey))
		if name == "" {
			continue
		}
		out = append(out, ApprovalQueueExportRef{
			Name:          name,
			CreatedAt:     strings.TrimSpace(readString(record, approvalQueueExportCreatedAtKey)),
			ItemCount:     int(readNumber(record, approvalQueueExportItemCountKey)),
			DecisionCount: int(readNumber(record, approvalQueueExportDecisionCountKey)),
			SizeBytes:     int(readNumber(record, approvalQueueExportSizeBytesKey)),
		})
	}
	return out
}

func approvalQueueDecisionsAny(items []ApprovalQueueDecision) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			approvalQueueDecisionItemIDKey:   item.ItemID,
			approvalQueueDecisionValueKey:    item.Decision,
			approvalQueueDecisionCommentKey:  item.Comment,
			approvalQueueDecisionActionIDKey: item.ActionID,
		}
		if item.DeferReason != "" {
			record[approvalQueueDecisionDeferReasonKey] = item.DeferReason
		}
		if item.DecidedAt != "" {
			record[approvalQueueDecisionDecidedAtKey] = item.DecidedAt
		}
		out = append(out, record)
	}
	return out
}

func approvalQueueAuditTrailAny(items []ApprovalQueueAuditEntry) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			approvalQueueAuditEventKey: item.Event,
		}
		if item.ItemID != "" {
			record[approvalQueueAuditItemIDKey] = item.ItemID
		}
		if item.Decision != "" {
			record[approvalQueueAuditDecisionKey] = item.Decision
		}
		if item.Comment != "" {
			record[approvalQueueAuditCommentKey] = item.Comment
		}
		if item.ActionID != "" {
			record[approvalQueueAuditActionIDKey] = item.ActionID
		}
		if item.DeferReason != "" {
			record[approvalQueueAuditDeferReasonKey] = item.DeferReason
		}
		if item.At != "" {
			record[approvalQueueAuditTimestampKey] = item.At
		}
		out = append(out, record)
	}
	return out
}

func approvalQueueExportRefsAny(items []ApprovalQueueExportRef) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			approvalQueueExportNameKey: item.Name,
		}
		if item.CreatedAt != "" {
			record[approvalQueueExportCreatedAtKey] = item.CreatedAt
		}
		if item.ItemCount > 0 {
			record[approvalQueueExportItemCountKey] = item.ItemCount
		}
		if item.DecisionCount > 0 {
			record[approvalQueueExportDecisionCountKey] = item.DecisionCount
		}
		if item.SizeBytes > 0 {
			record[approvalQueueExportSizeBytesKey] = item.SizeBytes
		}
		out = append(out, record)
	}
	return out
}
