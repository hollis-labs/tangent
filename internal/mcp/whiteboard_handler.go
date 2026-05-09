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

	scene := readObjectValue(data, "scene")
	if len(scene) == 0 && persisted != nil {
		scene = persisted.SceneSnapshot
	}

	assets := readWhiteboardAssetRefs(data["assets"])
	if len(assets) == 0 && persisted != nil {
		assets = persisted.Assets
	}

	notes := readStringValue(data, "notes")
	if notes == "" && persisted != nil {
		notes = persisted.Notes
	}

	return room.WhiteboardSnapshot{
		BoardID:       boardID,
		SceneSnapshot: scene,
		Assets:        assets,
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
	if view.Notes != "" {
		data["notes"] = view.Notes
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
			Width:      int(readNumberValue(record, "width")),
			Height:     int(readNumberValue(record, "height")),
		})
	}
	return assets
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
