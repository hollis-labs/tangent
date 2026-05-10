package room

import (
	"fmt"
	"strings"
)

const (
	WizardPhaseID = "wizard"

	wizardIDKey               = "wizard_id"
	wizardTitleKey            = "title"
	wizardDescriptionKey      = "description"
	wizardStepsKey            = "steps"
	wizardCurrentStepIDKey    = "current_step_id"
	wizardProgressKey         = "progress"
	wizardBranchSelectionsKey = "branch_selections"
	wizardSummaryKey          = "summary"
	wizardUpdatedAtKey        = "updated_at"

	wizardStepIDKey          = "step_id"
	wizardStepTitleKey       = "title"
	wizardStepDescriptionKey = "description"
	wizardStepKindKey        = "kind"
	wizardStepOptionalKey    = "optional"
	wizardStepFieldsKey      = "fields"
	wizardStepBranchesKey    = "branches"
	wizardStepMetadataKey    = "metadata"

	wizardBranchIDKey          = "branch_id"
	wizardBranchLabelKey       = "label"
	wizardBranchDescriptionKey = "description"
	wizardBranchTargetStepKey  = "target_step_id"
	wizardBranchMetadataKey    = "metadata"

	wizardProgressStatusKey      = "status"
	wizardProgressRevisionIDKey  = "revision_id"
	wizardProgressResponseKey    = "response"
	wizardProgressSummaryKey     = "summary"
	wizardProgressCompletedAtKey = "completed_at"
	wizardProgressUpdatedAtKey   = "updated_at"

	wizardSelectionOptionIDKey   = "option_id"
	wizardSelectionSelectedAtKey = "selected_at"

	wizardSummaryStatusKey            = "status"
	wizardSummaryHeadlineKey          = "headline"
	wizardSummaryDetailKey            = "detail"
	wizardSummaryCompletedCountKey    = "completed_step_count"
	wizardSummaryTotalCountKey        = "total_step_count"
	wizardSummaryCompletedAtKey       = "completed_at"
	wizardSummaryLastCompletedStepKey = "last_completed_step_id"
	wizardSummaryCurrentStepIDKey     = "current_step_id"
)

var allowedWizardProgressStatuses = map[string]struct{}{
	"pending":     {},
	"in_progress": {},
	"completed":   {},
	"skipped":     {},
	"blocked":     {},
}

var allowedWizardSummaryStatuses = map[string]struct{}{
	"not_started": {},
	"in_progress": {},
	"completed":   {},
	"blocked":     {},
}

type WizardStepBranch struct {
	BranchID     string         `json:"branch_id"`
	Label        string         `json:"label"`
	Description  string         `json:"description,omitempty"`
	TargetStepID string         `json:"target_step_id"`
	Metadata     map[string]any `json:"metadata"`
}

type WizardStep struct {
	StepID      string             `json:"step_id"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Kind        string             `json:"kind,omitempty"`
	Optional    bool               `json:"optional,omitempty"`
	Fields      map[string]any     `json:"fields,omitempty"`
	Branches    []WizardStepBranch `json:"branches"`
	Metadata    map[string]any     `json:"metadata"`
}

type WizardStepProgress struct {
	StepID      string         `json:"step_id"`
	Status      string         `json:"status"`
	RevisionID  string         `json:"revision_id,omitempty"`
	Response    map[string]any `json:"response"`
	Summary     string         `json:"summary,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
}

type WizardBranchSelection struct {
	StepID       string `json:"step_id"`
	OptionID     string `json:"option_id"`
	SelectedAt   string `json:"selected_at,omitempty"`
	TargetStepID string `json:"target_step_id,omitempty"`
}

type WizardSummary struct {
	Status              string `json:"status,omitempty"`
	Headline            string `json:"headline,omitempty"`
	Detail              string `json:"detail,omitempty"`
	CompletedStepCount  int    `json:"completed_step_count,omitempty"`
	TotalStepCount      int    `json:"total_step_count,omitempty"`
	CompletedAt         string `json:"completed_at,omitempty"`
	LastCompletedStepID string `json:"last_completed_step_id,omitempty"`
	CurrentStepID       string `json:"current_step_id,omitempty"`
}

type WizardStateView struct {
	WizardID         string                  `json:"wizard_id"`
	Title            string                  `json:"title,omitempty"`
	Description      string                  `json:"description,omitempty"`
	Steps            []WizardStep            `json:"steps"`
	CurrentStepID    string                  `json:"current_step_id,omitempty"`
	Progress         []WizardStepProgress    `json:"progress"`
	BranchSelections []WizardBranchSelection `json:"branch_selections"`
	Summary          *WizardSummary          `json:"summary,omitempty"`
	UpdatedAt        string                  `json:"updated_at,omitempty"`
}

type WizardSnapshot struct {
	WizardID         string
	Title            string
	Description      string
	Steps            []WizardStep
	CurrentStepID    string
	Progress         []WizardStepProgress
	BranchSelections []WizardBranchSelection
	Summary          *WizardSummary
	UpdatedAt        string
}

func (r *Room) SaveWizardSnapshot(snapshot WizardSnapshot) error {
	normalized, err := normalizeWizardSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[WizardPhaseID] = wizardBlobFromSnapshot(normalized)
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveWizardSnapshot(roomID string, snapshot WizardSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveWizardSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectWizardState(state PhaseState) *WizardStateView {
	return projectWizardStateFromBlob(state.PhaseOutputs[WizardPhaseID])
}

func projectWizardStateFromBlob(blob PhaseOutput) *WizardStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	wizardID := readString(blob.Data, wizardIDKey)
	if wizardID == "" {
		return nil
	}
	view := &WizardStateView{
		WizardID:         wizardID,
		Title:            readString(blob.Data, wizardTitleKey),
		Description:      readString(blob.Data, wizardDescriptionKey),
		Steps:            readWizardSteps(blob.Data[wizardStepsKey]),
		CurrentStepID:    readString(blob.Data, wizardCurrentStepIDKey),
		Progress:         readWizardProgress(blob.Data[wizardProgressKey]),
		BranchSelections: readWizardBranchSelections(blob.Data[wizardBranchSelectionsKey]),
		Summary:          readWizardSummary(blob.Data[wizardSummaryKey]),
		UpdatedAt:        readString(blob.Data, wizardUpdatedAtKey),
	}
	if view.Steps == nil {
		view.Steps = []WizardStep{}
	}
	if view.Progress == nil {
		view.Progress = []WizardStepProgress{}
	}
	if view.BranchSelections == nil {
		view.BranchSelections = []WizardBranchSelection{}
	}
	return view
}

func normalizeWizardSnapshot(snapshot WizardSnapshot) (WizardSnapshot, error) {
	wizardID := strings.TrimSpace(snapshot.WizardID)
	if wizardID == "" {
		return WizardSnapshot{}, ErrInvalidWizardID
	}
	steps, stepIDs, branchTargets, err := normalizeWizardSteps(snapshot.Steps)
	if err != nil {
		return WizardSnapshot{}, err
	}
	currentStepID := strings.TrimSpace(snapshot.CurrentStepID)
	if currentStepID != "" {
		if _, ok := stepIDs[currentStepID]; !ok {
			return WizardSnapshot{}, fmt.Errorf("%w: unknown current_step_id %q", ErrInvalidWizardState, currentStepID)
		}
	}
	progress, err := normalizeWizardProgress(snapshot.Progress, stepIDs)
	if err != nil {
		return WizardSnapshot{}, err
	}
	selections, err := normalizeWizardBranchSelections(snapshot.BranchSelections, stepIDs, branchTargets)
	if err != nil {
		return WizardSnapshot{}, err
	}
	summary, err := normalizeWizardSummary(snapshot.Summary, stepIDs, len(steps))
	if err != nil {
		return WizardSnapshot{}, err
	}
	return WizardSnapshot{
		WizardID:         wizardID,
		Title:            strings.TrimSpace(snapshot.Title),
		Description:      strings.TrimSpace(snapshot.Description),
		Steps:            steps,
		CurrentStepID:    currentStepID,
		Progress:         progress,
		BranchSelections: selections,
		Summary:          summary,
		UpdatedAt:        strings.TrimSpace(snapshot.UpdatedAt),
	}, nil
}

func normalizeWizardSteps(items []WizardStep) ([]WizardStep, map[string]struct{}, map[string]string, error) {
	if len(items) == 0 {
		return []WizardStep{}, map[string]struct{}{}, map[string]string{}, nil
	}
	out := make([]WizardStep, 0, len(items))
	stepIDs := make(map[string]struct{}, len(items))
	branchTargets := map[string]string{}
	for _, item := range items {
		stepID := strings.TrimSpace(item.StepID)
		title := strings.TrimSpace(item.Title)
		if stepID == "" || title == "" {
			return nil, nil, nil, ErrInvalidWizardStep
		}
		if _, exists := stepIDs[stepID]; exists {
			return nil, nil, nil, fmt.Errorf("%w: duplicate step_id %q", ErrInvalidWizardStep, stepID)
		}
		fields, err := normalizeFormMap(item.Fields, ErrInvalidWizardStep)
		if err != nil {
			return nil, nil, nil, err
		}
		metadata, err := normalizeFormMap(item.Metadata, ErrInvalidWizardStep)
		if err != nil {
			return nil, nil, nil, err
		}
		branches, err := normalizeWizardBranches(item.Branches)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, branch := range branches {
			branchTargets[stepID+"::"+branch.BranchID] = branch.TargetStepID
		}
		stepIDs[stepID] = struct{}{}
		out = append(out, WizardStep{
			StepID:      stepID,
			Title:       title,
			Description: strings.TrimSpace(item.Description),
			Kind:        strings.TrimSpace(item.Kind),
			Optional:    item.Optional,
			Fields:      fields,
			Branches:    branches,
			Metadata:    metadata,
		})
	}
	for _, item := range out {
		for _, branch := range item.Branches {
			if _, ok := stepIDs[branch.TargetStepID]; !ok {
				return nil, nil, nil, fmt.Errorf("%w: step %q branch %q references unknown target_step_id %q", ErrInvalidWizardStep, item.StepID, branch.BranchID, branch.TargetStepID)
			}
		}
	}
	return out, stepIDs, branchTargets, nil
}

func normalizeWizardBranches(items []WizardStepBranch) ([]WizardStepBranch, error) {
	if len(items) == 0 {
		return []WizardStepBranch{}, nil
	}
	out := make([]WizardStepBranch, 0, len(items))
	branchIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		branchID := strings.TrimSpace(item.BranchID)
		label := strings.TrimSpace(item.Label)
		targetStepID := strings.TrimSpace(item.TargetStepID)
		if branchID == "" || label == "" || targetStepID == "" {
			return nil, ErrInvalidWizardStep
		}
		if _, exists := branchIDs[branchID]; exists {
			return nil, fmt.Errorf("%w: duplicate branch_id %q", ErrInvalidWizardStep, branchID)
		}
		metadata, err := normalizeFormMap(item.Metadata, ErrInvalidWizardStep)
		if err != nil {
			return nil, err
		}
		branchIDs[branchID] = struct{}{}
		out = append(out, WizardStepBranch{
			BranchID:     branchID,
			Label:        label,
			Description:  strings.TrimSpace(item.Description),
			TargetStepID: targetStepID,
			Metadata:     metadata,
		})
	}
	return out, nil
}

func normalizeWizardProgress(items []WizardStepProgress, stepIDs map[string]struct{}) ([]WizardStepProgress, error) {
	if len(items) == 0 {
		return []WizardStepProgress{}, nil
	}
	out := make([]WizardStepProgress, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		stepID := strings.TrimSpace(item.StepID)
		status := strings.TrimSpace(item.Status)
		if stepID == "" || !isAllowedWizardProgressStatus(status) {
			return nil, ErrInvalidWizardProgress
		}
		if _, ok := stepIDs[stepID]; !ok {
			return nil, fmt.Errorf("%w: unknown step_id %q", ErrInvalidWizardProgress, stepID)
		}
		if _, exists := seen[stepID]; exists {
			return nil, fmt.Errorf("%w: duplicate step_id %q", ErrInvalidWizardProgress, stepID)
		}
		response, err := normalizeFormMap(item.Response, ErrInvalidWizardProgress)
		if err != nil {
			return nil, err
		}
		seen[stepID] = struct{}{}
		out = append(out, WizardStepProgress{
			StepID:      stepID,
			Status:      status,
			RevisionID:  strings.TrimSpace(item.RevisionID),
			Response:    response,
			Summary:     strings.TrimSpace(item.Summary),
			CompletedAt: strings.TrimSpace(item.CompletedAt),
			UpdatedAt:   strings.TrimSpace(item.UpdatedAt),
		})
	}
	return out, nil
}

func normalizeWizardBranchSelections(items []WizardBranchSelection, stepIDs map[string]struct{}, branchTargets map[string]string) ([]WizardBranchSelection, error) {
	if len(items) == 0 {
		return []WizardBranchSelection{}, nil
	}
	out := make([]WizardBranchSelection, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		stepID := strings.TrimSpace(item.StepID)
		optionID := strings.TrimSpace(item.OptionID)
		if stepID == "" || optionID == "" {
			return nil, ErrInvalidWizardBranchSelection
		}
		if _, ok := stepIDs[stepID]; !ok {
			return nil, fmt.Errorf("%w: unknown step_id %q", ErrInvalidWizardBranchSelection, stepID)
		}
		if _, exists := seen[stepID]; exists {
			return nil, fmt.Errorf("%w: duplicate selection for step_id %q", ErrInvalidWizardBranchSelection, stepID)
		}
		targetStepID, ok := branchTargets[stepID+"::"+optionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown branch option %q for step_id %q", ErrInvalidWizardBranchSelection, optionID, stepID)
		}
		if item.TargetStepID != "" && strings.TrimSpace(item.TargetStepID) != targetStepID {
			return nil, fmt.Errorf("%w: selection target_step_id %q does not match branch target %q", ErrInvalidWizardBranchSelection, item.TargetStepID, targetStepID)
		}
		seen[stepID] = struct{}{}
		out = append(out, WizardBranchSelection{
			StepID:       stepID,
			OptionID:     optionID,
			SelectedAt:   strings.TrimSpace(item.SelectedAt),
			TargetStepID: targetStepID,
		})
	}
	return out, nil
}

func normalizeWizardSummary(summary *WizardSummary, stepIDs map[string]struct{}, totalSteps int) (*WizardSummary, error) {
	if summary == nil {
		return nil, nil
	}
	status := strings.TrimSpace(summary.Status)
	if status != "" && !isAllowedWizardSummaryStatus(status) {
		return nil, ErrInvalidWizardSummary
	}
	if summary.CompletedStepCount < 0 || summary.TotalStepCount < 0 {
		return nil, ErrInvalidWizardSummary
	}
	totalCount := summary.TotalStepCount
	if totalCount == 0 && totalSteps > 0 {
		totalCount = totalSteps
	}
	if totalCount > 0 && totalCount != totalSteps {
		return nil, ErrInvalidWizardSummary
	}
	if summary.CompletedStepCount > totalCount {
		return nil, ErrInvalidWizardSummary
	}
	lastCompletedStepID := strings.TrimSpace(summary.LastCompletedStepID)
	if lastCompletedStepID != "" {
		if _, ok := stepIDs[lastCompletedStepID]; !ok {
			return nil, fmt.Errorf("%w: unknown last_completed_step_id %q", ErrInvalidWizardSummary, lastCompletedStepID)
		}
	}
	currentStepID := strings.TrimSpace(summary.CurrentStepID)
	if currentStepID != "" {
		if _, ok := stepIDs[currentStepID]; !ok {
			return nil, fmt.Errorf("%w: unknown current_step_id %q", ErrInvalidWizardSummary, currentStepID)
		}
	}
	return &WizardSummary{
		Status:              status,
		Headline:            strings.TrimSpace(summary.Headline),
		Detail:              strings.TrimSpace(summary.Detail),
		CompletedStepCount:  summary.CompletedStepCount,
		TotalStepCount:      totalCount,
		CompletedAt:         strings.TrimSpace(summary.CompletedAt),
		LastCompletedStepID: lastCompletedStepID,
		CurrentStepID:       currentStepID,
	}, nil
}

func wizardBlobFromSnapshot(snapshot WizardSnapshot) PhaseOutput {
	record := map[string]any{
		wizardIDKey:               snapshot.WizardID,
		wizardStepsKey:            wizardStepsAny(snapshot.Steps),
		wizardProgressKey:         wizardProgressAny(snapshot.Progress),
		wizardBranchSelectionsKey: wizardBranchSelectionsAny(snapshot.BranchSelections),
		wizardUpdatedAtKey:        snapshot.UpdatedAt,
	}
	if snapshot.Title != "" {
		record[wizardTitleKey] = snapshot.Title
	}
	if snapshot.Description != "" {
		record[wizardDescriptionKey] = snapshot.Description
	}
	if snapshot.CurrentStepID != "" {
		record[wizardCurrentStepIDKey] = snapshot.CurrentStepID
	}
	if summary := wizardSummaryAny(snapshot.Summary); len(summary) > 0 {
		record[wizardSummaryKey] = summary
	}
	return PhaseOutput{
		Version: phaseOutputVersion,
		Data:    record,
	}
}

func wizardStepsAny(items []WizardStep) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			wizardStepIDKey:       item.StepID,
			wizardStepTitleKey:    item.Title,
			wizardStepFieldsKey:   cloneAnyMap(item.Fields),
			wizardStepBranchesKey: wizardBranchesAny(item.Branches),
			wizardStepMetadataKey: cloneAnyMap(item.Metadata),
		}
		if item.Description != "" {
			record[wizardStepDescriptionKey] = item.Description
		}
		if item.Kind != "" {
			record[wizardStepKindKey] = item.Kind
		}
		if item.Optional {
			record[wizardStepOptionalKey] = true
		}
		out = append(out, record)
	}
	return out
}

func wizardBranchesAny(items []WizardStepBranch) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			wizardBranchIDKey:         item.BranchID,
			wizardBranchLabelKey:      item.Label,
			wizardBranchTargetStepKey: item.TargetStepID,
			wizardBranchMetadataKey:   cloneAnyMap(item.Metadata),
		}
		if item.Description != "" {
			record[wizardBranchDescriptionKey] = item.Description
		}
		out = append(out, record)
	}
	return out
}

func wizardProgressAny(items []WizardStepProgress) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			wizardStepIDKey:           item.StepID,
			wizardProgressStatusKey:   item.Status,
			wizardProgressResponseKey: cloneAnyMap(item.Response),
		}
		if item.RevisionID != "" {
			record[wizardProgressRevisionIDKey] = item.RevisionID
		}
		if item.Summary != "" {
			record[wizardProgressSummaryKey] = item.Summary
		}
		if item.CompletedAt != "" {
			record[wizardProgressCompletedAtKey] = item.CompletedAt
		}
		if item.UpdatedAt != "" {
			record[wizardProgressUpdatedAtKey] = item.UpdatedAt
		}
		out = append(out, record)
	}
	return out
}

func wizardBranchSelectionsAny(items []WizardBranchSelection) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		record := map[string]any{
			wizardStepIDKey:            item.StepID,
			wizardSelectionOptionIDKey: item.OptionID,
			wizardBranchTargetStepKey:  item.TargetStepID,
		}
		if item.SelectedAt != "" {
			record[wizardSelectionSelectedAtKey] = item.SelectedAt
		}
		out = append(out, record)
	}
	return out
}

func wizardSummaryAny(summary *WizardSummary) map[string]any {
	if summary == nil {
		return map[string]any{}
	}
	record := map[string]any{}
	if summary.Status != "" {
		record[wizardSummaryStatusKey] = summary.Status
	}
	if summary.Headline != "" {
		record[wizardSummaryHeadlineKey] = summary.Headline
	}
	if summary.Detail != "" {
		record[wizardSummaryDetailKey] = summary.Detail
	}
	if summary.CompletedStepCount > 0 {
		record[wizardSummaryCompletedCountKey] = summary.CompletedStepCount
	}
	if summary.TotalStepCount > 0 {
		record[wizardSummaryTotalCountKey] = summary.TotalStepCount
	}
	if summary.CompletedAt != "" {
		record[wizardSummaryCompletedAtKey] = summary.CompletedAt
	}
	if summary.LastCompletedStepID != "" {
		record[wizardSummaryLastCompletedStepKey] = summary.LastCompletedStepID
	}
	if summary.CurrentStepID != "" {
		record[wizardSummaryCurrentStepIDKey] = summary.CurrentStepID
	}
	return record
}

func readWizardSteps(raw any) []WizardStep {
	records := readObjectSlice(raw)
	out := make([]WizardStep, 0, len(records))
	for _, record := range records {
		stepID := strings.TrimSpace(readString(record, wizardStepIDKey))
		title := strings.TrimSpace(readString(record, wizardStepTitleKey))
		if stepID == "" || title == "" {
			continue
		}
		out = append(out, WizardStep{
			StepID:      stepID,
			Title:       title,
			Description: readString(record, wizardStepDescriptionKey),
			Kind:        readString(record, wizardStepKindKey),
			Optional:    readBool(record, wizardStepOptionalKey),
			Fields:      readObjectValueMap(record[wizardStepFieldsKey]),
			Branches:    readWizardBranches(record[wizardStepBranchesKey]),
			Metadata:    readObjectValueMap(record[wizardStepMetadataKey]),
		})
	}
	return out
}

func readWizardBranches(raw any) []WizardStepBranch {
	records := readObjectSlice(raw)
	out := make([]WizardStepBranch, 0, len(records))
	for _, record := range records {
		branchID := strings.TrimSpace(readString(record, wizardBranchIDKey))
		label := strings.TrimSpace(readString(record, wizardBranchLabelKey))
		targetStepID := strings.TrimSpace(readString(record, wizardBranchTargetStepKey))
		if branchID == "" || label == "" || targetStepID == "" {
			continue
		}
		out = append(out, WizardStepBranch{
			BranchID:     branchID,
			Label:        label,
			Description:  readString(record, wizardBranchDescriptionKey),
			TargetStepID: targetStepID,
			Metadata:     readObjectValueMap(record[wizardBranchMetadataKey]),
		})
	}
	return out
}

func readWizardProgress(raw any) []WizardStepProgress {
	records := readObjectSlice(raw)
	out := make([]WizardStepProgress, 0, len(records))
	for _, record := range records {
		stepID := strings.TrimSpace(readString(record, wizardStepIDKey))
		status := strings.TrimSpace(readString(record, wizardProgressStatusKey))
		if stepID == "" || status == "" {
			continue
		}
		out = append(out, WizardStepProgress{
			StepID:      stepID,
			Status:      status,
			RevisionID:  readString(record, wizardProgressRevisionIDKey),
			Response:    readObjectValueMap(record[wizardProgressResponseKey]),
			Summary:     readString(record, wizardProgressSummaryKey),
			CompletedAt: readString(record, wizardProgressCompletedAtKey),
			UpdatedAt:   readString(record, wizardProgressUpdatedAtKey),
		})
	}
	return out
}

func readWizardBranchSelections(raw any) []WizardBranchSelection {
	records := readObjectSlice(raw)
	out := make([]WizardBranchSelection, 0, len(records))
	for _, record := range records {
		stepID := strings.TrimSpace(readString(record, wizardStepIDKey))
		optionID := strings.TrimSpace(readString(record, wizardSelectionOptionIDKey))
		if stepID == "" || optionID == "" {
			continue
		}
		out = append(out, WizardBranchSelection{
			StepID:       stepID,
			OptionID:     optionID,
			SelectedAt:   readString(record, wizardSelectionSelectedAtKey),
			TargetStepID: readString(record, wizardBranchTargetStepKey),
		})
	}
	return out
}

func readWizardSummary(raw any) *WizardSummary {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	summary := &WizardSummary{
		Status:              readString(record, wizardSummaryStatusKey),
		Headline:            readString(record, wizardSummaryHeadlineKey),
		Detail:              readString(record, wizardSummaryDetailKey),
		CompletedStepCount:  readInt(record, wizardSummaryCompletedCountKey),
		TotalStepCount:      readInt(record, wizardSummaryTotalCountKey),
		CompletedAt:         readString(record, wizardSummaryCompletedAtKey),
		LastCompletedStepID: readString(record, wizardSummaryLastCompletedStepKey),
		CurrentStepID:       readString(record, wizardSummaryCurrentStepIDKey),
	}
	if summary.Status == "" && summary.Headline == "" && summary.Detail == "" &&
		summary.CompletedStepCount == 0 && summary.TotalStepCount == 0 &&
		summary.CompletedAt == "" && summary.LastCompletedStepID == "" && summary.CurrentStepID == "" {
		return nil
	}
	return summary
}

func isAllowedWizardProgressStatus(status string) bool {
	_, ok := allowedWizardProgressStatuses[status]
	return ok
}

func isAllowedWizardSummaryStatus(status string) bool {
	_, ok := allowedWizardSummaryStatuses[status]
	return ok
}

func readBool(record map[string]any, key string) bool {
	if record == nil {
		return false
	}
	value, _ := record[key].(bool)
	return value
}
