package room

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const phaseOutputVersion = 1

// PhaseOutput stores a versioned JSON blob for one workflow phase.
type PhaseOutput struct {
	Version int            `json:"version"`
	Data    map[string]any `json:"data"`
}

// PhaseState is the workflow-neutral phase substrate persisted on rooms.
type PhaseState struct {
	CurrentPhase  string                 `json:"current_phase"`
	PhasesVisited []string               `json:"phases_visited"`
	PhaseOutputs  map[string]PhaseOutput `json:"phase_outputs"`
}

func (r *Room) PhaseState() PhaseState {
	r.phaseMu.RLock()
	defer r.phaseMu.RUnlock()
	return clonePhaseState(PhaseState{
		CurrentPhase:  r.currentPhase,
		PhasesVisited: r.phasesVisited,
		PhaseOutputs:  r.phaseOutputs,
	})
}

func (r *Room) AdvancePhase(toPhase, _ string) error {
	phaseID, err := normalizePhaseID(toPhase)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextVisited := append(cloneStringSlice(r.phasesVisited), phaseID)
	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	if err := r.persistPhaseState(phaseID, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.currentPhase = phaseID
	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (r *Room) SetPhaseOutput(phase, key string, value any) error {
	phaseID, err := normalizePhaseID(phase)
	if err != nil {
		return err
	}
	outputKey, err := normalizePhaseOutputKey(key)
	if err != nil {
		return err
	}
	normalizedValue, err := normalizeJSONValue(value)
	if err != nil {
		return fmt.Errorf("room: normalize phase output value: %w", err)
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	blob := nextOutputs[phaseID]
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, phaseID)
	}
	if blob.Data == nil {
		blob.Data = map[string]any{}
	}
	blob.Data[outputKey] = normalizedValue
	nextOutputs[phaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

// ReplacePhaseOutput replaces one phase's whole output blob.
//
// It is the workflow-neutral write an interaction package persists through
// (internal/interactionpkg.StateStore). SetPhaseOutput sets one key at a time,
// which is the right primitive for tangent.session_set_phase_output but not
// for a package that owns the whole blob and has already normalized it: a
// package's state is one document, and writing it key-by-key would let a
// half-applied snapshot exist.
//
// The value is stored as given. Core does not inspect, validate, or interpret
// it — ADR 0003 §5 assigns publisher-owned business state to the publisher,
// and this method is where that assignment becomes mechanical rather than
// aspirational.
func (r *Room) ReplacePhaseOutput(phase string, data map[string]any) error {
	phaseID, err := normalizePhaseID(phase)
	if err != nil {
		return err
	}
	normalized, err := normalizeJSONValue(cloneAnyMap(data))
	if err != nil {
		return fmt.Errorf("room: normalize phase output: %w", err)
	}
	record, ok := normalized.(map[string]any)
	if !ok {
		record = map[string]any{}
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	nextOutputs[phaseID] = PhaseOutput{Version: phaseOutputVersion, Data: record}
	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

// ReplacePhaseOutput is the Manager-scoped form, resolving the room first.
func (m *Manager) ReplacePhaseOutput(roomID, phase string, data map[string]any) error {
	rm, ok := m.Get(roomID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	return rm.ReplacePhaseOutput(phase, data)
}

// PhaseOutputData returns one phase's stored blob, reading through to the
// database for a room that is no longer resident. The bool distinguishes "the
// room is unknown" from "the room holds nothing for that phase", which a
// package needs in order to tell a first turn from a cleared one.
func (m *Manager) PhaseOutputData(ctx context.Context, roomID, phase string) (map[string]any, bool, error) {
	state, found, err := m.GetPhaseState(ctx, roomID)
	if err != nil || !found {
		return nil, false, err
	}
	return cloneAnyMap(state.PhaseOutputs[phase].Data), true, nil
}

func (m *Manager) AdvancePhase(roomID, toPhase, reason string) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.AdvancePhase(toPhase, reason); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func (m *Manager) SetPhaseOutput(roomID, phase, key string, value any) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SetPhaseOutput(phase, key, value); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func (m *Manager) GetPhaseState(ctx context.Context, roomID string) (PhaseState, bool, error) {
	if rm, ok := m.Get(roomID); ok {
		return rm.PhaseState(), true, nil
	}
	if m.db == nil {
		return PhaseState{}, false, nil
	}

	var (
		currentPhase string
		visitedRaw   string
		outputsRaw   string
	)
	err := m.db.QueryRowContext(ctx, `
SELECT current_phase, phases_visited, phase_outputs
FROM rooms
WHERE id = ?`,
		roomID,
	).Scan(&currentPhase, &visitedRaw, &outputsRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PhaseState{}, false, nil
		}
		return PhaseState{}, false, fmt.Errorf("query room %q phase state: %w", roomID, err)
	}

	state, err := decodePhaseState(currentPhase, visitedRaw, outputsRaw)
	if err != nil {
		return PhaseState{}, false, fmt.Errorf("decode room %q phase state: %w", roomID, err)
	}
	return state, true, nil
}

func normalizePhaseID(phase string) (string, error) {
	trimmed := strings.TrimSpace(phase)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("%w: %q", ErrInvalidPhaseID, phase)
	}
	return trimmed, nil
}

func normalizePhaseOutputKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("%w: %q", ErrInvalidPhaseKey, key)
	}
	return trimmed, nil
}

func clonePhaseState(state PhaseState) PhaseState {
	return PhaseState{
		CurrentPhase:  state.CurrentPhase,
		PhasesVisited: cloneStringSlice(state.PhasesVisited),
		PhaseOutputs:  clonePhaseOutputs(state.PhaseOutputs),
	}
}

func clonePhaseOutputs(in map[string]PhaseOutput) map[string]PhaseOutput {
	if len(in) == 0 {
		return map[string]PhaseOutput{}
	}
	out := make(map[string]PhaseOutput, len(in))
	for phaseID, blob := range in {
		out[phaseID] = PhaseOutput{
			Version: blob.Version,
			Data:    cloneAnyMap(blob.Data),
		}
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func normalizeJSONValue(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodePhaseState(currentPhase, visitedRaw, outputsRaw string) (PhaseState, error) {
	state := PhaseState{
		CurrentPhase:  currentPhase,
		PhasesVisited: []string{},
		PhaseOutputs:  map[string]PhaseOutput{},
	}
	if visitedRaw != "" {
		if err := json.Unmarshal([]byte(visitedRaw), &state.PhasesVisited); err != nil {
			return PhaseState{}, fmt.Errorf("unmarshal phases_visited: %w", err)
		}
	}
	if outputsRaw != "" {
		if err := json.Unmarshal([]byte(outputsRaw), &state.PhaseOutputs); err != nil {
			return PhaseState{}, fmt.Errorf("unmarshal phase_outputs: %w", err)
		}
	}

	if state.CurrentPhase != "" {
		normalized, err := normalizePhaseID(state.CurrentPhase)
		if err != nil {
			return PhaseState{}, err
		}
		state.CurrentPhase = normalized
	}
	for i, phaseID := range state.PhasesVisited {
		normalized, err := normalizePhaseID(phaseID)
		if err != nil {
			return PhaseState{}, err
		}
		state.PhasesVisited[i] = normalized
	}
	for phaseID, blob := range state.PhaseOutputs {
		normalized, err := normalizePhaseID(phaseID)
		if err != nil {
			return PhaseState{}, err
		}
		if blob.Version == 0 {
			blob.Version = phaseOutputVersion
		}
		if blob.Data == nil {
			blob.Data = map[string]any{}
		}
		delete(state.PhaseOutputs, phaseID)
		state.PhaseOutputs[normalized] = PhaseOutput{
			Version: blob.Version,
			Data:    cloneAnyMap(blob.Data),
		}
	}

	return clonePhaseState(state), nil
}
