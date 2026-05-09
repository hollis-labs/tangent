package room

import (
	"fmt"
	"strings"
)

const (
	WhiteboardPhaseID               = "whiteboard"
	whiteboardBoardIDKey            = "board_id"
	whiteboardSceneSnapshotKey      = "scene_snapshot"
	whiteboardAssetsKey             = "assets"
	whiteboardNotesKey              = "notes"
	whiteboardUpdatedAtKey          = "updated_at"
	whiteboardRevisionHistoryKey    = "revision_history"
	whiteboardRevisionIDKey         = "revision_id"
	whiteboardRevisionSummaryKey    = "summary"
	whiteboardRevisionSceneSizeKey  = "scene_size"
	whiteboardRevisionAssetCountKey = "asset_count"
	whiteboardAssetIDKey            = "asset_id"
	whiteboardAssetArtifactIDKey    = "artifact_id"
	whiteboardAssetNameKey          = "name"
	whiteboardAssetMIMETypeKey      = "mime_type"
	whiteboardAssetSourceKey        = "source"
	whiteboardAssetWidthKey         = "width"
	whiteboardAssetHeightKey        = "height"
)

type WhiteboardAssetRef struct {
	AssetID    string `json:"asset_id,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Name       string `json:"name,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Source     string `json:"source,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type WhiteboardRevision struct {
	RevisionID string `json:"revision_id"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Summary    string `json:"summary,omitempty"`
	SceneSize  int    `json:"scene_size,omitempty"`
	AssetCount int    `json:"asset_count,omitempty"`
}

type WhiteboardStateView struct {
	BoardID         string               `json:"board_id"`
	SceneSnapshot   map[string]any       `json:"scene_snapshot"`
	Assets          []WhiteboardAssetRef `json:"assets"`
	Notes           string               `json:"notes,omitempty"`
	UpdatedAt       string               `json:"updated_at,omitempty"`
	RevisionHistory []WhiteboardRevision `json:"revision_history"`
}

type WhiteboardSnapshot struct {
	BoardID       string
	SceneSnapshot map[string]any
	Assets        []WhiteboardAssetRef
	Notes         string
	UpdatedAt     string
	Revision      *WhiteboardRevision
}

func (r *Room) SaveWhiteboardSnapshot(snapshot WhiteboardSnapshot) error {
	normalized, err := normalizeWhiteboardSnapshot(snapshot)
	if err != nil {
		return err
	}

	r.phaseMu.Lock()
	defer r.phaseMu.Unlock()

	nextOutputs := clonePhaseOutputs(r.phaseOutputs)
	current := projectWhiteboardStateFromBlob(nextOutputs[WhiteboardPhaseID])
	if current == nil {
		current = &WhiteboardStateView{}
	}

	nextState := WhiteboardStateView{
		BoardID:         normalized.BoardID,
		SceneSnapshot:   normalized.SceneSnapshot,
		Assets:          normalized.Assets,
		Notes:           normalized.Notes,
		UpdatedAt:       normalized.UpdatedAt,
		RevisionHistory: cloneWhiteboardRevisions(current.RevisionHistory),
	}
	if normalized.Revision != nil {
		nextState.RevisionHistory = append(nextState.RevisionHistory, *normalized.Revision)
		if nextState.UpdatedAt == "" {
			nextState.UpdatedAt = normalized.Revision.UpdatedAt
		}
	}

	blob, err := whiteboardBlobFromState(nextOutputs[WhiteboardPhaseID], nextState)
	if err != nil {
		return err
	}
	nextOutputs[WhiteboardPhaseID] = blob

	nextVisited := cloneStringSlice(r.phasesVisited)
	if err := r.persistPhaseState(r.currentPhase, nextVisited, nextOutputs); err != nil {
		return err
	}

	r.phasesVisited = nextVisited
	r.phaseOutputs = nextOutputs
	return nil
}

func (m *Manager) SaveWhiteboardSnapshot(roomID string, snapshot WhiteboardSnapshot) (PhaseState, error) {
	rm, ok := m.Get(roomID)
	if !ok {
		return PhaseState{}, fmt.Errorf("%w: %s", ErrRoomNotFound, roomID)
	}
	if err := rm.SaveWhiteboardSnapshot(snapshot); err != nil {
		return PhaseState{}, err
	}
	return rm.PhaseState(), nil
}

func ProjectWhiteboardState(state PhaseState) *WhiteboardStateView {
	return projectWhiteboardStateFromBlob(state.PhaseOutputs[WhiteboardPhaseID])
}

func projectWhiteboardStateFromBlob(blob PhaseOutput) *WhiteboardStateView {
	if len(blob.Data) == 0 {
		return nil
	}
	boardID := readString(blob.Data, whiteboardBoardIDKey)
	if boardID == "" {
		return nil
	}
	view := &WhiteboardStateView{
		BoardID:         boardID,
		SceneSnapshot:   readWhiteboardSceneSnapshot(blob.Data[whiteboardSceneSnapshotKey]),
		Assets:          readWhiteboardAssets(blob.Data[whiteboardAssetsKey]),
		Notes:           readString(blob.Data, whiteboardNotesKey),
		UpdatedAt:       readString(blob.Data, whiteboardUpdatedAtKey),
		RevisionHistory: readWhiteboardRevisions(blob.Data[whiteboardRevisionHistoryKey]),
	}
	if view.SceneSnapshot == nil {
		view.SceneSnapshot = map[string]any{}
	}
	if view.Assets == nil {
		view.Assets = []WhiteboardAssetRef{}
	}
	if view.RevisionHistory == nil {
		view.RevisionHistory = []WhiteboardRevision{}
	}
	return view
}

func normalizeWhiteboardSnapshot(snapshot WhiteboardSnapshot) (WhiteboardSnapshot, error) {
	boardID := strings.TrimSpace(snapshot.BoardID)
	if boardID == "" {
		return WhiteboardSnapshot{}, ErrInvalidWhiteboardBoardID
	}

	sceneSnapshot := map[string]any{}
	if snapshot.SceneSnapshot != nil {
		rawScene, err := normalizeJSONValue(snapshot.SceneSnapshot)
		if err != nil {
			return WhiteboardSnapshot{}, fmt.Errorf("room: normalize whiteboard scene snapshot: %w", err)
		}
		sceneMap, ok := rawScene.(map[string]any)
		if !ok {
			return WhiteboardSnapshot{}, ErrInvalidWhiteboardBoardID
		}
		sceneSnapshot = sceneMap
	}

	assets := make([]WhiteboardAssetRef, 0, len(snapshot.Assets))
	for _, asset := range snapshot.Assets {
		normalizedAsset, err := normalizeWhiteboardAssetRef(asset)
		if err != nil {
			return WhiteboardSnapshot{}, err
		}
		assets = append(assets, normalizedAsset)
	}

	var revision *WhiteboardRevision
	if snapshot.Revision != nil {
		normalizedRevision, err := normalizeWhiteboardRevision(*snapshot.Revision)
		if err != nil {
			return WhiteboardSnapshot{}, err
		}
		revision = &normalizedRevision
	}

	return WhiteboardSnapshot{
		BoardID:       boardID,
		SceneSnapshot: sceneSnapshot,
		Assets:        assets,
		Notes:         strings.TrimSpace(snapshot.Notes),
		UpdatedAt:     strings.TrimSpace(snapshot.UpdatedAt),
		Revision:      revision,
	}, nil
}

func normalizeWhiteboardAssetRef(asset WhiteboardAssetRef) (WhiteboardAssetRef, error) {
	normalized := WhiteboardAssetRef{
		AssetID:    strings.TrimSpace(asset.AssetID),
		ArtifactID: strings.TrimSpace(asset.ArtifactID),
		Name:       strings.TrimSpace(asset.Name),
		MIMEType:   strings.TrimSpace(asset.MIMEType),
		Source:     strings.TrimSpace(asset.Source),
		Width:      asset.Width,
		Height:     asset.Height,
	}
	if normalized.Width < 0 || normalized.Height < 0 {
		return WhiteboardAssetRef{}, ErrInvalidWhiteboardAssetRef
	}
	if normalized.AssetID == "" && normalized.ArtifactID == "" && normalized.Source == "" {
		return WhiteboardAssetRef{}, ErrInvalidWhiteboardAssetRef
	}
	if strings.HasPrefix(normalized.Source, "data:") {
		return WhiteboardAssetRef{}, ErrInvalidWhiteboardAssetRef
	}
	return normalized, nil
}

func normalizeWhiteboardRevision(revision WhiteboardRevision) (WhiteboardRevision, error) {
	normalized := WhiteboardRevision{
		RevisionID: strings.TrimSpace(revision.RevisionID),
		UpdatedAt:  strings.TrimSpace(revision.UpdatedAt),
		Summary:    strings.TrimSpace(revision.Summary),
		SceneSize:  revision.SceneSize,
		AssetCount: revision.AssetCount,
	}
	if normalized.RevisionID == "" || normalized.SceneSize < 0 || normalized.AssetCount < 0 {
		return WhiteboardRevision{}, ErrInvalidWhiteboardRevisionID
	}
	return normalized, nil
}

func whiteboardBlobFromState(blob PhaseOutput, state WhiteboardStateView) (PhaseOutput, error) {
	switch {
	case blob.Version == 0:
		blob.Version = phaseOutputVersion
	case blob.Version != phaseOutputVersion:
		return PhaseOutput{}, fmt.Errorf("room: unsupported phase output version %d for %q", blob.Version, WhiteboardPhaseID)
	}

	sceneRaw, err := normalizeJSONValue(state.SceneSnapshot)
	if err != nil {
		return PhaseOutput{}, fmt.Errorf("room: normalize whiteboard scene blob: %w", err)
	}
	assetsRaw, err := normalizeJSONValue(whiteboardAssetsAny(state.Assets))
	if err != nil {
		return PhaseOutput{}, fmt.Errorf("room: normalize whiteboard assets blob: %w", err)
	}
	revisionsRaw, err := normalizeJSONValue(whiteboardRevisionsAny(state.RevisionHistory))
	if err != nil {
		return PhaseOutput{}, fmt.Errorf("room: normalize whiteboard revisions blob: %w", err)
	}

	blob.Data = map[string]any{
		whiteboardBoardIDKey:         state.BoardID,
		whiteboardSceneSnapshotKey:   sceneRaw,
		whiteboardAssetsKey:          assetsRaw,
		whiteboardNotesKey:           state.Notes,
		whiteboardUpdatedAtKey:       state.UpdatedAt,
		whiteboardRevisionHistoryKey: revisionsRaw,
	}
	return blob, nil
}

func readWhiteboardSceneSnapshot(raw any) map[string]any {
	record, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(record))
	for key, value := range record {
		out[key] = value
	}
	return out
}

func readWhiteboardAssets(raw any) []WhiteboardAssetRef {
	items, ok := raw.([]any)
	if !ok {
		return []WhiteboardAssetRef{}
	}
	assets := make([]WhiteboardAssetRef, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		asset, err := normalizeWhiteboardAssetRef(WhiteboardAssetRef{
			AssetID:    readString(record, whiteboardAssetIDKey),
			ArtifactID: readString(record, whiteboardAssetArtifactIDKey),
			Name:       readString(record, whiteboardAssetNameKey),
			MIMEType:   readString(record, whiteboardAssetMIMETypeKey),
			Source:     readString(record, whiteboardAssetSourceKey),
			Width:      readInt(record, whiteboardAssetWidthKey),
			Height:     readInt(record, whiteboardAssetHeightKey),
		})
		if err != nil {
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}

func readWhiteboardRevisions(raw any) []WhiteboardRevision {
	items, ok := raw.([]any)
	if !ok {
		return []WhiteboardRevision{}
	}
	revisions := make([]WhiteboardRevision, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		revision, err := normalizeWhiteboardRevision(WhiteboardRevision{
			RevisionID: readString(record, whiteboardRevisionIDKey),
			UpdatedAt:  readString(record, whiteboardUpdatedAtKey),
			Summary:    readString(record, whiteboardRevisionSummaryKey),
			SceneSize:  readInt(record, whiteboardRevisionSceneSizeKey),
			AssetCount: readInt(record, whiteboardRevisionAssetCountKey),
		})
		if err != nil {
			continue
		}
		revisions = append(revisions, revision)
	}
	return revisions
}

func whiteboardAssetsAny(assets []WhiteboardAssetRef) []map[string]any {
	if len(assets) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		record := map[string]any{}
		if asset.AssetID != "" {
			record[whiteboardAssetIDKey] = asset.AssetID
		}
		if asset.ArtifactID != "" {
			record[whiteboardAssetArtifactIDKey] = asset.ArtifactID
		}
		if asset.Name != "" {
			record[whiteboardAssetNameKey] = asset.Name
		}
		if asset.MIMEType != "" {
			record[whiteboardAssetMIMETypeKey] = asset.MIMEType
		}
		if asset.Source != "" {
			record[whiteboardAssetSourceKey] = asset.Source
		}
		if asset.Width > 0 {
			record[whiteboardAssetWidthKey] = asset.Width
		}
		if asset.Height > 0 {
			record[whiteboardAssetHeightKey] = asset.Height
		}
		out = append(out, record)
	}
	return out
}

func whiteboardRevisionsAny(revisions []WhiteboardRevision) []map[string]any {
	if len(revisions) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(revisions))
	for _, revision := range revisions {
		record := map[string]any{
			whiteboardRevisionIDKey: revision.RevisionID,
		}
		if revision.UpdatedAt != "" {
			record[whiteboardUpdatedAtKey] = revision.UpdatedAt
		}
		if revision.Summary != "" {
			record[whiteboardRevisionSummaryKey] = revision.Summary
		}
		if revision.SceneSize > 0 {
			record[whiteboardRevisionSceneSizeKey] = revision.SceneSize
		}
		if revision.AssetCount > 0 {
			record[whiteboardRevisionAssetCountKey] = revision.AssetCount
		}
		out = append(out, record)
	}
	return out
}

func cloneWhiteboardRevisions(in []WhiteboardRevision) []WhiteboardRevision {
	if len(in) == 0 {
		return []WhiteboardRevision{}
	}
	out := make([]WhiteboardRevision, len(in))
	copy(out, in)
	return out
}

func readInt(record map[string]any, key string) int {
	if record == nil {
		return 0
	}
	switch value := record[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
