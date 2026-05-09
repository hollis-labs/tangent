package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/room"
)

type whiteboardInput struct {
	Envelope envelopes.Envelope `json:"envelope"`
}

type whiteboardSelectionSummary struct {
	Count int      `json:"count"`
	IDs   []string `json:"ids,omitempty"`
	Types []string `json:"types,omitempty"`
}

type whiteboardExportRef struct {
	ArtifactID string `json:"artifact_id,omitempty"`
	Name       string `json:"name,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Kind       string `json:"kind,omitempty"`
	URI        string `json:"uri,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	SizeBytes  int    `json:"size_bytes,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type whiteboardSubmitDraft struct {
	BoardID                 string                      `json:"board_id"`
	ContinuedFromRevisionID string                      `json:"continued_from_revision_id,omitempty"`
	Scene                   map[string]any              `json:"scene"`
	Assets                  []room.WhiteboardAssetRef   `json:"assets,omitempty"`
	Notes                   string                      `json:"notes,omitempty"`
	ToolMode                string                      `json:"tool_mode,omitempty"`
	SelectionSummary        *whiteboardSelectionSummary `json:"selection_summary,omitempty"`
	ExportRefs              []whiteboardExportRef       `json:"export_refs,omitempty"`
}

type whiteboardSubmitPayload struct {
	BoardID                 string                      `json:"board_id"`
	RevisionID              string                      `json:"revision_id"`
	ContinuedFromRevisionID string                      `json:"continued_from_revision_id,omitempty"`
	Scene                   map[string]any              `json:"scene"`
	Assets                  []room.WhiteboardAssetRef   `json:"assets"`
	Notes                   string                      `json:"notes"`
	ToolMode                string                      `json:"tool_mode,omitempty"`
	SelectionSummary        *whiteboardSelectionSummary `json:"selection_summary,omitempty"`
	ExportRefs              []whiteboardExportRef       `json:"export_refs,omitempty"`
}

func (s *Server) handleWhiteboard(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args whiteboardInput,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Envelope.Type != whiteboardEnvelopeType {
		return toolErrorResult(
			envelopes.ErrorCodeUnsupportedType,
			fmt.Sprintf(
				"tangent.whiteboard rejects envelope type %q; want %q",
				args.Envelope.Type,
				whiteboardEnvelopeType,
			),
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
			return toolErrorResult(
				envelopes.ErrorCodeHostError,
				fmt.Sprintf("decode session_create result: %v", err),
			), nil, nil
		}
		roomID = created.RoomID
		s.logWorkflowRoomCreated("whiteboard", roomID, args.Envelope.ID)
	} else {
		s.logWorkflowRoomReused("whiteboard", roomID, args.Envelope.ID)
	}

	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("load room phase state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	snapshot := whiteboardSnapshotFromEnvelope(args.Envelope, room.ProjectWhiteboardState(phaseState))
	if _, saveErr := s.manager.SaveWhiteboardSnapshot(roomID, snapshot); saveErr != nil {
		return sessionPhaseStateError(roomID, saveErr), nil, nil
	}

	phaseState, found, err = s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("reload room whiteboard state: %v", err)), nil, nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}

	return s.advanceRoomEnvelope(ctx, roomID, buildVisibleWhiteboardEnvelope(&args.Envelope, room.ProjectWhiteboardState(phaseState)))
}

func whiteboardSnapshotFromEnvelope(env envelopes.Envelope, persisted *room.WhiteboardStateView) room.WhiteboardSnapshot {
	data := env.Data
	boardID := readStringValue(data, "board_id")
	if boardID == "" && persisted != nil {
		boardID = persisted.BoardID
	}
	reusePersisted := persisted != nil && persisted.BoardID != "" && persisted.BoardID == boardID

	scene := readObjectValue(data, "scene")
	if reusePersisted {
		scene = persisted.SceneSnapshot
	} else if len(scene) == 0 && persisted != nil {
		scene = persisted.SceneSnapshot
	}

	assets := readWhiteboardAssetRefs(data["assets"])
	assets = mergeWhiteboardAssetRefs(assets, readWhiteboardReferenceImages(data["reference_images"]))
	if reusePersisted {
		assets = persisted.Assets
	} else if len(assets) == 0 && persisted != nil {
		assets = persisted.Assets
	}

	exportRefs := readWhiteboardExportRefs(data["export_refs"])
	if reusePersisted {
		exportRefs = whiteboardMCPExportRefsFromRoom(persisted.ExportRefs)
	} else if len(exportRefs) == 0 && persisted != nil {
		exportRefs = whiteboardMCPExportRefsFromRoom(persisted.ExportRefs)
	}

	notes := readStringValue(data, "notes")
	if reusePersisted {
		notes = persisted.Notes
	} else if notes == "" && persisted != nil {
		notes = persisted.Notes
	}

	return room.WhiteboardSnapshot{
		BoardID:       boardID,
		SceneSnapshot: scene,
		Assets:        assets,
		ExportRefs:    whiteboardRoomExportRefs(exportRefs),
		Notes:         notes,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

func buildVisibleWhiteboardEnvelope(env *envelopes.Envelope, view *room.WhiteboardStateView) *envelopes.Envelope {
	clone := cloneEnvelopeForDispatch(env)
	if clone.Data == nil {
		clone.Data = map[string]any{}
	}
	if view == nil {
		return clone
	}

	data := cloneAnyMapForDispatch(clone.Data)
	data["board_id"] = view.BoardID
	data["scene"] = view.SceneSnapshot
	data["assets"] = whiteboardAssetRefsAny(view.Assets)
	data["reference_images"] = whiteboardAssetRefsAny(room.WhiteboardReferenceImageRefs(view.Assets))
	data["export_refs"] = whiteboardExportRefsAny(view.ExportRefs)
	if view.Notes != "" {
		data["notes"] = view.Notes
	}
	if view.UpdatedAt != "" {
		data["updated_at"] = view.UpdatedAt
	}
	if len(view.RevisionHistory) > 0 {
		data["revision_history"] = whiteboardRevisionHistoryAny(view.RevisionHistory)
		data["revision_id"] = view.RevisionHistory[len(view.RevisionHistory)-1].RevisionID
	}
	if len(view.RevisionSnapshots) > 0 {
		data["revisions"] = whiteboardRevisionSnapshotsAny(view.RevisionSnapshots)
	}
	clone.Data = data
	return clone
}

func readObjectValue(data map[string]any, key string) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	value, _ := data[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return cloneAnyMapForDispatch(value)
}

func readWhiteboardAssetRefs(raw any) []room.WhiteboardAssetRef {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return nil
	}
	assets := make([]room.WhiteboardAssetRef, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		assets = append(assets, room.WhiteboardAssetRef{
			AssetID:    readStringValue(record, "asset_id"),
			ArtifactID: readStringValue(record, "artifact_id"),
			Name:       readStringValue(record, "name"),
			MIMEType:   readStringValue(record, "mime_type"),
			Source:     readStringValue(record, "source"),
			URI:        readStringValue(record, "uri"),
			Kind:       readStringValue(record, "kind"),
			Width:      int(readNumberValue(record, "width")),
			Height:     int(readNumberValue(record, "height")),
		})
	}
	return assets
}

func readWhiteboardReferenceImages(raw any) []room.WhiteboardAssetRef {
	assets := readWhiteboardAssetRefs(raw)
	if len(assets) == 0 {
		return nil
	}
	out := make([]room.WhiteboardAssetRef, 0, len(assets))
	for _, asset := range assets {
		asset.Kind = "reference_image"
		out = append(out, asset)
	}
	return out
}

func readWhiteboardExportRefs(raw any) []whiteboardExportRef {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return nil
	}
	refs := make([]whiteboardExportRef, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refs = append(refs, whiteboardExportRef{
			ArtifactID: readStringValue(record, "artifact_id"),
			Name:       readStringValue(record, "name"),
			MIMEType:   readStringValue(record, "mime_type"),
			Kind:       readStringValue(record, "kind"),
			URI:        readStringValue(record, "uri"),
			CreatedAt:  readStringValue(record, "created_at"),
			SizeBytes:  int(readNumberValue(record, "size_bytes")),
			Width:      int(readNumberValue(record, "width")),
			Height:     int(readNumberValue(record, "height")),
		})
	}
	return refs
}

func whiteboardAssetRefsAny(assets []room.WhiteboardAssetRef) []any {
	if len(assets) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(assets))
	for _, asset := range assets {
		record := map[string]any{}
		if asset.AssetID != "" {
			record["asset_id"] = asset.AssetID
		}
		if asset.ArtifactID != "" {
			record["artifact_id"] = asset.ArtifactID
		}
		if asset.Name != "" {
			record["name"] = asset.Name
		}
		if asset.MIMEType != "" {
			record["mime_type"] = asset.MIMEType
		}
		if asset.Source != "" {
			record["source"] = asset.Source
		}
		if asset.URI != "" {
			record["uri"] = asset.URI
		}
		if asset.Kind != "" {
			record["kind"] = asset.Kind
		}
		if asset.Width > 0 {
			record["width"] = asset.Width
		}
		if asset.Height > 0 {
			record["height"] = asset.Height
		}
		out = append(out, record)
	}
	return out
}

func whiteboardExportRefsAny(refs []room.WhiteboardExportRef) []any {
	if len(refs) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(refs))
	for _, ref := range refs {
		record := map[string]any{}
		if ref.ArtifactID != "" {
			record["artifact_id"] = ref.ArtifactID
		}
		if ref.Name != "" {
			record["name"] = ref.Name
		}
		if ref.MIMEType != "" {
			record["mime_type"] = ref.MIMEType
		}
		if ref.Kind != "" {
			record["kind"] = ref.Kind
		}
		if ref.URI != "" {
			record["uri"] = ref.URI
		}
		if ref.CreatedAt != "" {
			record["created_at"] = ref.CreatedAt
		}
		if ref.SizeBytes > 0 {
			record["size_bytes"] = ref.SizeBytes
		}
		if ref.Width > 0 {
			record["width"] = ref.Width
		}
		if ref.Height > 0 {
			record["height"] = ref.Height
		}
		out = append(out, record)
	}
	return out
}

func readNumberValue(data map[string]any, key string) float64 {
	if data == nil {
		return 0
	}
	switch value := data[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func (s *Server) normalizeWhiteboardSubmitResponse(
	roomID string,
	env *envelopes.Envelope,
	resp *envelopes.Response,
) (*envelopes.Response, error) {
	if resp == nil {
		return nil, whiteboardResponseValidationError("response is required")
	}
	if resp.Kind != envelopes.ResponseKindData {
		return nil, whiteboardResponseValidationError("kind must be %q", envelopes.ResponseKindData)
	}
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return nil, whiteboardResponseValidationError("status must be %q", envelopes.ResponseStatusSubmitted)
	}

	phaseState, found, err := s.manager.GetPhaseState(context.Background(), roomID)
	if err != nil {
		return nil, fmt.Errorf("whiteboard submit: load room state: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", room.ErrRoomNotFound, roomID)
	}
	persisted := room.ProjectWhiteboardState(phaseState)

	draft, err := decodeWhiteboardSubmitDraft(resp.Payload)
	if err != nil {
		return nil, err
	}
	if draft.BoardID == "" {
		return nil, whiteboardResponseValidationError("payload.board_id is required")
	}
	if persisted != nil && persisted.BoardID != "" && persisted.BoardID != draft.BoardID {
		return nil, whiteboardResponseValidationError(
			"payload.board_id %q does not match persisted board %q",
			draft.BoardID,
			persisted.BoardID,
		)
	}
	if draft.Scene == nil {
		return nil, whiteboardResponseValidationError("payload.scene is required")
	}
	if !isValidWhiteboardToolMode(draft.ToolMode) {
		return nil, whiteboardResponseValidationError("payload.tool_mode %q is invalid", draft.ToolMode)
	}

	assets := draft.Assets
	if assets == nil && persisted != nil && persisted.BoardID == draft.BoardID {
		assets = persisted.Assets
	}

	completedAt := nowRFC3339()
	revisionID := nextWhiteboardRevisionID(draft.BoardID, persisted)
	payload := whiteboardSubmitPayload{
		BoardID:                 draft.BoardID,
		RevisionID:              revisionID,
		ContinuedFromRevisionID: draft.ContinuedFromRevisionID,
		Scene:                   draft.Scene,
		Assets:                  assets,
		Notes:                   draft.Notes,
		ToolMode:                draft.ToolMode,
	}
	if payload.Assets == nil {
		payload.Assets = []room.WhiteboardAssetRef{}
	}
	if draft.SelectionSummary != nil {
		payload.SelectionSummary = normalizeWhiteboardSelectionSummary(*draft.SelectionSummary)
	}
	if len(draft.ExportRefs) > 0 {
		payload.ExportRefs = normalizeWhiteboardExportRefs(draft.ExportRefs)
	}

	if _, err := s.manager.SaveWhiteboardSnapshot(roomID, room.WhiteboardSnapshot{
		BoardID:       payload.BoardID,
		SceneSnapshot: payload.Scene,
		Assets:        payload.Assets,
		ExportRefs:    whiteboardRoomExportRefs(payload.ExportRefs),
		Notes:         payload.Notes,
		UpdatedAt:     completedAt,
		Revision: &room.WhiteboardRevision{
			RevisionID:              revisionID,
			ContinuedFromRevisionID: payload.ContinuedFromRevisionID,
			UpdatedAt:               completedAt,
			Summary:                 whiteboardRevisionSummary(payload),
			SceneSize:               whiteboardSceneSize(payload.Scene),
			AssetCount:              len(payload.Assets),
		},
	}); err != nil {
		return nil, err
	}

	return &envelopes.Response{
		V:           envelopes.ProtocolVersion,
		EnvelopeID:  env.ID,
		Kind:        envelopes.ResponseKindData,
		Status:      envelopes.ResponseStatusSubmitted,
		Payload:     payload,
		CompletedAt: completedAt,
	}, nil
}

func decodeWhiteboardSubmitDraft(raw any) (whiteboardSubmitDraft, error) {
	if raw == nil {
		return whiteboardSubmitDraft{}, whiteboardResponseValidationError("payload is required")
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return whiteboardSubmitDraft{}, whiteboardResponseValidationError("payload must be JSON-shaped")
	}
	var draft whiteboardSubmitDraft
	if err := json.Unmarshal(blob, &draft); err != nil {
		return whiteboardSubmitDraft{}, whiteboardResponseValidationError("payload is invalid: %v", err)
	}
	return draft, nil
}

func whiteboardResponseValidationError(format string, args ...any) error {
	return fmt.Errorf("whiteboard submit response: %w: %s", envelopes.ErrSchemaValidation, fmt.Sprintf(format, args...))
}

func nextWhiteboardRevisionID(boardID string, persisted *room.WhiteboardStateView) string {
	seq := 1
	if persisted != nil && persisted.BoardID == boardID {
		seq = len(persisted.RevisionHistory) + 1
	}
	return fmt.Sprintf("%s-r%d", boardID, seq)
}

func whiteboardRevisionSummary(payload whiteboardSubmitPayload) string {
	if payload.ContinuedFromRevisionID != "" {
		return fmt.Sprintf("continued from %s", payload.ContinuedFromRevisionID)
	}
	if payload.Notes != "" {
		if len(payload.Notes) > 160 {
			return payload.Notes[:160]
		}
		return payload.Notes
	}
	if payload.SelectionSummary != nil && payload.SelectionSummary.Count > 0 {
		return fmt.Sprintf("%d selected object(s)", payload.SelectionSummary.Count)
	}
	return fmt.Sprintf("whiteboard revision %s", payload.RevisionID)
}

func whiteboardSceneSize(scene map[string]any) int {
	if len(scene) == 0 {
		return 0
	}
	if store, ok := scene["store"].(map[string]any); ok {
		return len(store)
	}
	document, _ := scene["document"].(map[string]any)
	if pages, ok := document["pages"].([]any); ok {
		return len(pages)
	}
	return len(scene)
}

func normalizeWhiteboardSelectionSummary(summary whiteboardSelectionSummary) *whiteboardSelectionSummary {
	out := &whiteboardSelectionSummary{
		Count: summary.Count,
		IDs:   compactStrings(summary.IDs),
		Types: compactStrings(summary.Types),
	}
	if out.Count < 0 {
		out.Count = 0
	}
	if len(out.IDs) == 0 {
		out.IDs = nil
	}
	if len(out.Types) == 0 {
		out.Types = nil
	}
	return out
}

func normalizeWhiteboardExportRefs(refs []whiteboardExportRef) []whiteboardExportRef {
	out := make([]whiteboardExportRef, 0, len(refs))
	for _, ref := range refs {
		item := whiteboardExportRef{
			ArtifactID: ref.ArtifactID,
			Name:       ref.Name,
			MIMEType:   ref.MIMEType,
			Kind:       ref.Kind,
			URI:        ref.URI,
			CreatedAt:  ref.CreatedAt,
			SizeBytes:  ref.SizeBytes,
			Width:      ref.Width,
			Height:     ref.Height,
		}
		if item.ArtifactID == "" && item.Name == "" && item.MIMEType == "" && item.Kind == "" && item.URI == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func whiteboardRoomExportRefs(refs []whiteboardExportRef) []room.WhiteboardExportRef {
	if len(refs) == 0 {
		return []room.WhiteboardExportRef{}
	}
	out := make([]room.WhiteboardExportRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, room.WhiteboardExportRef{
			ArtifactID: ref.ArtifactID,
			Name:       ref.Name,
			MIMEType:   ref.MIMEType,
			Kind:       ref.Kind,
			URI:        ref.URI,
			CreatedAt:  ref.CreatedAt,
			SizeBytes:  ref.SizeBytes,
			Width:      ref.Width,
			Height:     ref.Height,
		})
	}
	return out
}

func whiteboardRevisionHistoryAny(revisions []room.WhiteboardRevision) []any {
	if len(revisions) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(revisions))
	for _, revision := range revisions {
		record := map[string]any{
			"revision_id": revision.RevisionID,
		}
		if revision.ContinuedFromRevisionID != "" {
			record["continued_from_revision_id"] = revision.ContinuedFromRevisionID
		}
		if revision.UpdatedAt != "" {
			record["updated_at"] = revision.UpdatedAt
		}
		if revision.Summary != "" {
			record["summary"] = revision.Summary
		}
		if revision.SceneSize > 0 {
			record["scene_size"] = revision.SceneSize
		}
		if revision.AssetCount > 0 {
			record["asset_count"] = revision.AssetCount
		}
		out = append(out, record)
	}
	return out
}

func whiteboardRevisionSnapshotsAny(snapshots []room.WhiteboardRevisionSnapshot) []any {
	if len(snapshots) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		record := map[string]any{
			"revision_id": snapshot.RevisionID,
			"scene":       snapshot.SceneSnapshot,
			"assets":      whiteboardAssetRefsAny(snapshot.Assets),
			"reference_images": whiteboardAssetRefsAny(
				room.WhiteboardReferenceImageRefs(snapshot.Assets),
			),
			"export_refs": whiteboardExportRefsAny(snapshot.ExportRefs),
			"notes":       snapshot.Notes,
		}
		if snapshot.ContinuedFromRevisionID != "" {
			record["continued_from_revision_id"] = snapshot.ContinuedFromRevisionID
		}
		if snapshot.UpdatedAt != "" {
			record["updated_at"] = snapshot.UpdatedAt
		}
		if snapshot.Summary != "" {
			record["summary"] = snapshot.Summary
		}
		if snapshot.SceneSize > 0 {
			record["scene_size"] = snapshot.SceneSize
		}
		if snapshot.AssetCount > 0 {
			record["asset_count"] = snapshot.AssetCount
		}
		out = append(out, record)
	}
	return out
}

func whiteboardMCPExportRefsFromRoom(refs []room.WhiteboardExportRef) []whiteboardExportRef {
	if len(refs) == 0 {
		return []whiteboardExportRef{}
	}
	out := make([]whiteboardExportRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, whiteboardExportRef{
			ArtifactID: ref.ArtifactID,
			Name:       ref.Name,
			MIMEType:   ref.MIMEType,
			Kind:       ref.Kind,
			URI:        ref.URI,
			CreatedAt:  ref.CreatedAt,
			SizeBytes:  ref.SizeBytes,
			Width:      ref.Width,
			Height:     ref.Height,
		})
	}
	return out
}

func mergeWhiteboardAssetRefs(groups ...[]room.WhiteboardAssetRef) []room.WhiteboardAssetRef {
	if len(groups) == 0 {
		return nil
	}
	seen := make(map[string]int)
	out := make([]room.WhiteboardAssetRef, 0)
	for _, group := range groups {
		for _, asset := range group {
			key := whiteboardAssetRefKey(asset)
			if key == "" {
				continue
			}
			if idx, ok := seen[key]; ok {
				if asset.Kind == "reference_image" {
					out[idx].Kind = "reference_image"
				}
				if out[idx].URI == "" {
					out[idx].URI = asset.URI
				}
				if out[idx].Source == "" {
					out[idx].Source = asset.Source
				}
				if out[idx].ArtifactID == "" {
					out[idx].ArtifactID = asset.ArtifactID
				}
				continue
			}
			seen[key] = len(out)
			out = append(out, asset)
		}
	}
	return out
}

func whiteboardAssetRefKey(asset room.WhiteboardAssetRef) string {
	switch {
	case asset.AssetID != "":
		return "asset:" + asset.AssetID
	case asset.URI != "":
		return "uri:" + asset.URI
	case asset.ArtifactID != "":
		return "artifact:" + asset.ArtifactID
	case asset.Source != "":
		return "source:" + asset.Source
	default:
		return ""
	}
}

func compactStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func isValidWhiteboardToolMode(mode string) bool {
	switch mode {
	case "", "select", "draw", "text", "shape", "arrow", "note":
		return true
	default:
		return false
	}
}
