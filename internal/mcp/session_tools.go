package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/interactionpkg"
	"github.com/hollis-labs/tangent/internal/room"
)

const (
	errorCodeSessionBusy  = "SESSION_BUSY"
	errorCodeRoomNotFound = "ROOM_NOT_FOUND"
	// errorCodeRoomForbidden reports a refusal inside the caller's own
	// authority. Its message never names the required capability, the owning
	// scope, or the participant: an authorization failure that explains itself
	// is a probe (ADR 0004 §6.5).
	errorCodeRoomForbidden = "ROOM_FORBIDDEN"
)

type sessionCreateInput struct {
	Title string         `json:"title"`
	Meta  map[string]any `json:"meta"`
}

type sessionAdvanceInput struct {
	RoomID     string             `json:"roomID"`
	Envelope   envelopes.Envelope `json:"envelope"`
	Completion completionInput    `json:"completion,omitempty"`
}

type sessionGetInput struct {
	RoomID string `json:"roomID"`
}

type sessionAdvancePhaseInput struct {
	RoomID  string `json:"roomID"`
	ToPhase string `json:"to_phase"`
	Reason  string `json:"reason"`
}

type sessionSetPhaseOutputInput struct {
	RoomID string `json:"roomID"`
	Phase  string `json:"phase"`
	Key    string `json:"key"`
	Value  any    `json:"value"`
}

type sessionCloseInput struct {
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

type sessionListInput struct {
	ActiveOnly bool `json:"active_only"`
}

type sessionCreateResult struct {
	RoomID string `json:"roomID"`
	URL    string `json:"url"`
}

type sessionGetResult struct {
	Status                string                           `json:"status"`
	Phase                 string                           `json:"phase"`
	CurrentPhase          string                           `json:"current_phase"`
	PhasesVisited         []string                         `json:"phases_visited"`
	PhaseOutputs          map[string]room.PhaseOutput      `json:"phase_outputs"`
	Wizard                *room.WizardStateView            `json:"wizard,omitempty"`
	Dashboard             *room.DashboardStateView         `json:"dashboard,omitempty"`
	ProgressPanel         *room.ProgressPanelStateView     `json:"progress_panel,omitempty"`
	FilePicker            *room.FilePickerStateView        `json:"file_picker,omitempty"`
	DiffReview            *room.DiffReviewStateView        `json:"diff_review,omitempty"`
	ApprovalQueue         *room.ApprovalQueueStateView     `json:"approval_queue,omitempty"`
	SpreadsheetReview     *room.SpreadsheetReviewStateView `json:"spreadsheet_review,omitempty"`
	Whiteboard            *room.WhiteboardStateView        `json:"whiteboard,omitempty"`
	SynthesisNotes        *room.SynthesisNotesView         `json:"synthesis_notes,omitempty"`
	AcceptedDraftBlocks   []room.DraftBlock                `json:"accepted_draft_blocks"`
	CurrentDraft          *room.CurrentDraftView           `json:"current_draft,omitempty"`
	ProseRevisionOutcomes []room.ProseRevisionOutcome      `json:"prose_revision_outcomes"`
	FinalOutput           *room.FinalOutputView            `json:"final_output,omitempty"`
	EnvelopesHistory      []room.EnvelopeHistory           `json:"envelopes_history"`
	CurrentEnvelope       *envelopes.Envelope              `json:"current_envelope,omitempty"`
	// Connections reports the room's Connection lifecycle: who is attached and
	// which client holds the resolver lease. It is deliberately a sibling of
	// the interaction projections rather than folded into Status, because a
	// room can be active with nobody looking and connected with nothing to
	// answer.
	Connections *room.ConnectionState `json:"connections,omitempty"`

	// packageProjections carries one entry per installed interaction package
	// that has state for this room, keyed on the package's own projection key.
	// MarshalJSON splices them in at the top level, which is what lets a
	// package own its projection type without core declaring a field for it.
	//
	// It is unexported and has no tag on purpose: it must not be reflected
	// into tangent.session_get's advertised output schema, because that schema
	// is composed at registration from the same packages' own contributions
	// (see sessionGetOutputSchema).
	packageProjections map[string]any
}

// MarshalJSON emits the core projection with every package's projection
// spliced in as a sibling field.
//
// ADR 0003 §8 C6 freezes tangent.session_get's shape, and the SPA and every
// v0.12 client read `form_collect` at the top level of this object. Nesting
// package state under a container would have been the cleaner design and is a
// wire break; splicing preserves the exact bytes while letting the field's
// meaning and type live in the package that owns them.
//
// A package key that collides with a core field name loses: core's own value
// is written last. Nothing shipped collides, and a package cannot quietly
// shadow `status` or `phase_outputs` by declaring a projection key.
func (r sessionGetResult) MarshalJSON() ([]byte, error) {
	type plain sessionGetResult
	core, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	if len(r.packageProjections) == 0 {
		return core, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(core, &merged); err != nil {
		return nil, err
	}
	for key, projection := range r.packageProjections {
		if _, taken := merged[key]; taken {
			continue
		}
		raw, marshalErr := json.Marshal(projection)
		if marshalErr != nil {
			return nil, marshalErr
		}
		merged[key] = raw
	}
	return json.Marshal(merged)
}

type sessionPhaseStateResult struct {
	RoomID        string                      `json:"roomID"`
	CurrentPhase  string                      `json:"current_phase"`
	PhasesVisited []string                    `json:"phases_visited"`
	PhaseOutputs  map[string]room.PhaseOutput `json:"phase_outputs"`
}

type sessionCloseResult struct {
	OK     bool   `json:"ok"`
	RoomID string `json:"roomID"`
	Status string `json:"status"`
}

type sessionListResult struct {
	Rooms []room.RoomSummary `json:"rooms"`
}

func (s *Server) handleSessionCreate(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionCreateInput,
) (*mcpsdk.CallToolResult, sessionCreateResult, error) {
	meta := stringifyMeta(args.Meta)
	if args.Title != "" {
		if _, exists := meta["title"]; !exists {
			meta["title"] = args.Title
		}
	}
	rm, err := s.manager.CreateWithError(meta)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("create room: %v", err)), sessionCreateResult{}, nil
	}
	// Ownership is recorded with the room rather than at its first advance, so
	// a room is never briefly an object no authorization check has an answer
	// for. The new surface's owner scope is the host-derived caller scope; the
	// caller cannot nominate it.
	if s.roomflow != nil {
		if ownErr := s.roomflow.EnsureRoomOwnership(ctx, rm.ID, callerIdentity(request)); ownErr != nil {
			return toolErrorResult(envelopes.ErrorCodeHostError,
				fmt.Sprintf("record room ownership: %v", ownErr)), sessionCreateResult{}, nil
		}
	}
	result := sessionCreateResult{
		RoomID: rm.ID,
		URL:    s.roomURL(rm.ID),
	}
	toolRes, payload := toolJSONResult(result)
	return toolRes, payload, nil
}

func (s *Server) handleSessionAdvance(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args sessionAdvanceInput,
) (*mcpsdk.CallToolResult, any, error) {
	return s.advanceRoomEnvelope(ctx, args.RoomID, &args.Envelope, nil, args.Completion)
}

func (s *Server) handleSessionGet(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionGetInput,
) (*mcpsdk.CallToolResult, sessionGetResult, error) {
	// Reads stay authority-wide. `session_*` is ADR 0001 §10 compatibility
	// vocabulary, and narrowing a read would break "show me all my rooms"
	// without buying isolation that `standalone-local` partitions can back.
	// Cross-authority is still refused, and as not-found.
	if err := s.authorizeRoom(ctx, args.RoomID, callerIdentity(request),
		authz.View, authz.AuthorityWide); err != nil {
		return roomAuthorizationError(args.RoomID, err), emptySessionGetResult(), nil
	}
	result, found, err := s.roomProjection(ctx, args.RoomID)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, err.Error()), emptySessionGetResult(), nil
	}
	if !found {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", args.RoomID)), emptySessionGetResult(), nil
	}
	toolRes, payload := toolJSONResult(result)
	return toolRes, payload, nil
}

// roomProjection assembles one room's full projection.
//
// It is shared by tangent.session_get and the browser room API so the two
// cannot drift: migrating the SPA off /mcp must not change what the SPA sees,
// or the migration becomes a contract change instead of a transport one.
func (s *Server) roomProjection(ctx context.Context, roomID string) (sessionGetResult, bool, error) {
	phaseState, found, err := s.manager.GetPhaseState(ctx, roomID)
	if err != nil {
		return emptySessionGetResult(), false, fmt.Errorf("load room phase state: %w", err)
	}
	history, err := s.manager.History(ctx, roomID)
	if err != nil {
		return emptySessionGetResult(), false, fmt.Errorf("load room history: %w", err)
	}

	rm, ok := s.manager.Get(roomID)
	if !found && !ok && len(history) == 0 {
		return emptySessionGetResult(), false, nil
	}

	result := sessionGetResult{
		Status:                "closed",
		Phase:                 "closed",
		CurrentPhase:          phaseState.CurrentPhase,
		PhasesVisited:         phaseState.PhasesVisited,
		PhaseOutputs:          phaseState.PhaseOutputs,
		Wizard:                room.ProjectWizardState(phaseState),
		Dashboard:             room.ProjectDashboardState(phaseState),
		ProgressPanel:         room.ProjectProgressPanelState(phaseState),
		FilePicker:            room.ProjectFilePickerState(phaseState),
		DiffReview:            room.ProjectDiffReviewState(phaseState),
		ApprovalQueue:         room.ProjectApprovalQueueState(phaseState),
		SpreadsheetReview:     room.ProjectSpreadsheetReviewState(phaseState),
		Whiteboard:            room.ProjectWhiteboardState(phaseState),
		SynthesisNotes:        room.ProjectSynthesisNotes(phaseState),
		AcceptedDraftBlocks:   room.ProjectAcceptedDraftBlocks(phaseState),
		CurrentDraft:          room.ProjectCurrentDraft(phaseState),
		ProseRevisionOutcomes: room.ProjectProseRevisionOutcomes(phaseState),
		FinalOutput:           room.ProjectFinalOutput(phaseState),
		EnvelopesHistory:      history,
	}
	// Every installed package renders its own projection from the phase blob
	// it owns. Core passes the reader and keys the result on what the package
	// declares; it never names a package, a phase, or a projection field.
	result.packageProjections = s.packages.Projections(func(phaseID string) (map[string]any, bool) {
		blob, present := phaseState.PhaseOutputs[phaseID]
		return blob.Data, present
	})
	if ok {
		result.Status = "active"
		result.Phase = "active"
		result.CurrentEnvelope = rm.CurrentEnvelope()
		state := rm.ConnectionState()
		result.Connections = &state
	}
	return result, true, nil
}

func (s *Server) handleSessionAdvancePhase(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionAdvancePhaseInput,
) (*mcpsdk.CallToolResult, sessionPhaseStateResult, error) {
	if err := s.authorizeRoom(ctx, args.RoomID, callerIdentity(request),
		authz.Submit, authz.PartitionScoped); err != nil {
		return roomAuthorizationError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	phaseState, err := s.manager.AdvancePhase(args.RoomID, args.ToPhase, args.Reason)
	if err != nil {
		return sessionPhaseStateError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	toolRes, payload := toolJSONResult(sessionPhaseStateResult{
		RoomID:        args.RoomID,
		CurrentPhase:  phaseState.CurrentPhase,
		PhasesVisited: phaseState.PhasesVisited,
		PhaseOutputs:  phaseState.PhaseOutputs,
	})
	return toolRes, payload, nil
}

func (s *Server) handleSessionSetPhaseOutput(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionSetPhaseOutputInput,
) (*mcpsdk.CallToolResult, sessionPhaseStateResult, error) {
	if err := s.authorizeRoom(ctx, args.RoomID, callerIdentity(request),
		authz.Submit, authz.PartitionScoped); err != nil {
		return roomAuthorizationError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	phaseState, err := s.manager.SetPhaseOutput(args.RoomID, args.Phase, args.Key, args.Value)
	if err != nil {
		return sessionPhaseStateError(args.RoomID, err), emptySessionPhaseStateResult(args.RoomID), nil
	}
	toolRes, payload := toolJSONResult(sessionPhaseStateResult{
		RoomID:        args.RoomID,
		CurrentPhase:  phaseState.CurrentPhase,
		PhasesVisited: phaseState.PhasesVisited,
		PhaseOutputs:  phaseState.PhaseOutputs,
	})
	return toolRes, payload, nil
}

func (s *Server) handleSessionClose(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionCloseInput,
) (*mcpsdk.CallToolResult, sessionCloseResult, error) {
	status := args.Status
	if status == "" {
		status = "closed"
	}
	// This is the one place ADR 0004 narrows a shipped behavior, and it is a
	// deliberate exception to treating `session_*` as compatibility
	// vocabulary: closing dispositions another partition's pending human work.
	// Reads stay authority-wide; only the destructive operation is narrowed.
	caller := callerIdentity(request)
	if err := s.authorizeRoom(ctx, args.RoomID, caller, authz.Close, authz.PartitionScoped); err != nil {
		return roomAuthorizationError(args.RoomID, err), sessionCloseResult{}, nil
	}
	// Closing a room is an explicit, authorized caller action — one of the two
	// things permitted to terminalize outstanding work. The canonical surface
	// disposition is recorded first so the legacy room rows written below are
	// a projection of that decision rather than a competing claim about it.
	if err := s.closeRoomDurably(ctx, args.RoomID, status, caller); err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("close room interactions: %v", err)), sessionCloseResult{}, nil
	}
	if err := s.manager.Close(args.RoomID, status); err != nil {
		if errors.Is(err, room.ErrRoomNotFound) {
			return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", args.RoomID)), sessionCloseResult{}, nil
		}
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("close room: %v", err)), sessionCloseResult{}, nil
	}
	toolRes, payload := toolJSONResult(sessionCloseResult{
		OK:     true,
		RoomID: args.RoomID,
		Status: status,
	})
	return toolRes, payload, nil
}

func (s *Server) handleSessionList(
	ctx context.Context,
	request *mcpsdk.CallToolRequest,
	args sessionListInput,
) (*mcpsdk.CallToolResult, sessionListResult, error) {
	rooms, err := s.manager.List(ctx, args.ActiveOnly)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("list rooms: %v", err)), sessionListResult{}, nil
	}
	toolRes, payload := toolJSONResult(sessionListResult{
		Rooms: s.visibleRooms(ctx, rooms, callerIdentity(request)),
	})
	return toolRes, payload, nil
}

// visibleRooms drops rooms belonging to another authority.
//
// They are omitted rather than errored on: an error is itself an existence
// signal, and `active_only` keeps filtering surface lifecycle only. Within
// `standalone-local` nothing is filtered, which preserves today's behavior
// exactly — and is honest, because those partitions are advisory.
//
// One ownership lookup per room. That is a point read against a primary key on
// a local SQLite file for a list a single user's tab strip polls, so the cost
// is not worth a join that would have to duplicate the room-to-surface mapping
// this package deliberately owns in one place.
func (s *Server) visibleRooms(
	ctx context.Context,
	rooms []room.RoomSummary,
	caller interaction.ActorBinding,
) []room.RoomSummary {
	if s.roomflow == nil {
		return rooms
	}
	visible := make([]room.RoomSummary, 0, len(rooms))
	for _, summary := range rooms {
		if s.roomflow.VisibleToCaller(ctx, summary.ID, caller) {
			visible = append(visible, summary)
		}
	}
	return visible
}

// advanceRoomEnvelope is the single entry point every named room workflow and
// tangent.session_advance funnels through.
//
// When the durable substrate is installed it routes to the canonical
// compatibility adapter; the legacy blocking path below remains only for
// embedders that construct the MCP server without an interaction service, and
// it is the behavior the durable path is measured against.
// request is the caller's own envelope: it establishes durable identity and is
// what an identical retry is compared against. presented is what the
// participant actually sees — for several workflows that is the caller's
// envelope merged with persisted room state, or (for synthesis notes) a
// redacted view of it. They are deliberately separate: room state moves as the
// participant works, so keying identity on the presented envelope would turn
// every legitimate retry into a conflict. Pass nil when they are the same.
func (s *Server) advanceRoomEnvelope(
	ctx context.Context,
	roomID string,
	request *envelopes.Envelope,
	presented *envelopes.Envelope,
	completion completionInput,
) (*mcpsdk.CallToolResult, any, error) {
	if request == nil {
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, "envelope is required"), nil, nil
	}
	if presented == nil {
		presented = request
	}
	if err := s.envSvc.Validate(presented); err != nil {
		return triageErrorResult(err), nil, nil
	}
	// Every named workflow and tangent.session_advance passes through here, so
	// this is where the seventeen workflow tools stop being able to reuse an
	// arbitrary caller-supplied room and receive that room's human response.
	// A room the caller just created has no owner yet, which AuthorizeRoom
	// permits; one another authority owns is refused as not-found.
	caller := callerIdentity(nil)
	if err := s.authorizeRoom(ctx, roomID, caller, authz.Submit, authz.PartitionScoped); err != nil {
		return roomAuthorizationError(roomID, err), nil, nil
	}
	if s.roomflow != nil {
		return s.advanceRoomEnvelopeDurable(ctx, roomID, request, presented, completion, caller)
	}
	return s.advanceRoomEnvelopeLegacy(ctx, roomID, presented)
}

// advanceRoomEnvelopeLegacy is the v0.12 blocking path: it pushes the envelope
// onto the room and blocks the caller's request until the participant answers,
// the room closes, or the request context expires. Its shortcoming is the
// reason this task exists — an expiring context both loses the result and
// terminalizes the request — so it is kept strictly as the compatibility
// reference for embedders without the durable substrate.
func (s *Server) advanceRoomEnvelopeLegacy(
	ctx context.Context,
	roomID string,
	env *envelopes.Envelope,
) (*mcpsdk.CallToolResult, any, error) {
	rm, ok := s.manager.Get(roomID)
	if !ok {
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID)), nil, nil
	}
	if !rm.TryClaimAdvance() {
		return toolErrorResult(errorCodeSessionBusy, fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
	}
	defer rm.ReleaseAdvance()
	if rm.HasPending() {
		return toolErrorResult(errorCodeSessionBusy, fmt.Sprintf("room %q already has a pending envelope", roomID)), nil, nil
	}

	resp, err := rm.PushWithResponseTransform(ctx, env, func(resp *envelopes.Response) (*envelopes.Response, error) {
		return s.NormalizeResponse(roomID, env, resp)
	})
	if err != nil {
		if errors.Is(err, room.ErrUserCancelled) {
			cancelled := &envelopes.Response{
				V:           envelopes.ProtocolVersion,
				EnvelopeID:  env.ID,
				Kind:        envelopes.ResponseKindAck,
				Status:      envelopes.ResponseStatusCancelled,
				CompletedAt: nowRFC3339(),
			}
			toolRes, payload := toolJSONResult(cancelled)
			return toolRes, payload, nil
		}
		return triageErrorResult(err), nil, nil
	}
	toolRes, payload := toolJSONResult(resp)
	return toolRes, payload, nil
}

func (s *Server) roomURL(roomID string) string {
	if s.roomURLBase == "" {
		return ""
	}
	return s.roomURLBase + fmt.Sprintf(triageRoomURLPath, roomID)
}

func stringifyMeta(meta map[string]any) map[string]string {
	if len(meta) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		switch typed := v.(type) {
		case string:
			out[k] = typed
		default:
			raw, err := json.Marshal(v)
			if err != nil {
				out[k] = fmt.Sprint(v)
				continue
			}
			out[k] = string(raw)
		}
	}
	return out
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func sessionPhaseStateError(roomID string, err error) *mcpsdk.CallToolResult {
	switch {
	case errors.Is(err, room.ErrRoomNotFound):
		return toolErrorResult(errorCodeRoomNotFound, fmt.Sprintf("room %q not found", roomID))
	// One arm for every interaction package. A package wraps its own
	// validation refusals in interactionpkg.ErrInvalidState, so core maps them
	// onto `validation-failed` without naming a single publisher sentinel —
	// which is what the seven room.ErrInvalidForm* arms this replaced used to
	// require of every new kind.
	case errors.Is(err, interactionpkg.ErrInvalidState):
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, err.Error())
	case errors.Is(err, room.ErrInvalidPhaseID),
		errors.Is(err, room.ErrInvalidPhaseKey),
		errors.Is(err, room.ErrInvalidDashboardID),
		errors.Is(err, room.ErrInvalidDashboardTile),
		errors.Is(err, room.ErrInvalidDashboardLayout),
		errors.Is(err, room.ErrInvalidDashboardSavedLayout),
		errors.Is(err, room.ErrInvalidDashboardQueryState),
		errors.Is(err, room.ErrInvalidDashboardSummary),
		errors.Is(err, room.ErrInvalidDashboardSnapshot),
		errors.Is(err, room.ErrInvalidDashboardExport),
		errors.Is(err, room.ErrInvalidFilePickerID),
		errors.Is(err, room.ErrInvalidFilePickerBrowseRoot),
		errors.Is(err, room.ErrInvalidFilePickerSelectionRef),
		errors.Is(err, room.ErrInvalidFilePickerQueryState),
		errors.Is(err, room.ErrInvalidFilePickerSelectionRevision),
		errors.Is(err, room.ErrInvalidApprovalQueueID),
		errors.Is(err, room.ErrInvalidApprovalQueueItem),
		errors.Is(err, room.ErrInvalidApprovalQueueDecision),
		errors.Is(err, room.ErrInvalidApprovalQueueAuditEntry),
		errors.Is(err, room.ErrInvalidApprovalQueueExportRef),
		errors.Is(err, room.ErrInvalidSpreadsheetTableID),
		errors.Is(err, room.ErrInvalidSpreadsheetColumn),
		errors.Is(err, room.ErrInvalidSpreadsheetRow),
		errors.Is(err, room.ErrInvalidSpreadsheetSavedView),
		errors.Is(err, room.ErrInvalidSpreadsheetRowAction),
		errors.Is(err, room.ErrInvalidSpreadsheetActionID),
		errors.Is(err, room.ErrInvalidSpreadsheetExportRef),
		errors.Is(err, room.ErrInvalidWhiteboardBoardID),
		errors.Is(err, room.ErrInvalidWhiteboardSceneSnapshot),
		errors.Is(err, room.ErrInvalidWhiteboardAssetRef),
		errors.Is(err, room.ErrInvalidWhiteboardExportRef),
		errors.Is(err, room.ErrInvalidWhiteboardRevisionID),
		errors.Is(err, room.ErrInvalidDraftBlockID),
		errors.Is(err, room.ErrInvalidDraftBlockContent),
		errors.Is(err, room.ErrInvalidProseRevisionID),
		errors.Is(err, room.ErrInvalidProseRevisionLens),
		errors.Is(err, room.ErrInvalidProseRevisionSourceText),
		errors.Is(err, room.ErrInvalidProseRevisionSuggestionID),
		errors.Is(err, room.ErrInvalidProseRevisionSuggestionText),
		errors.Is(err, room.ErrInvalidProseRevisionDecision),
		errors.Is(err, room.ErrInvalidProseRevisionOutcome),
		errors.Is(err, room.ErrInvalidFinalOutputMarkdown),
		errors.Is(err, room.ErrInvalidFinalOutputFormat):
		return toolErrorResult(envelopes.ErrorCodeValidationFailed, err.Error())
	default:
		return toolErrorResult(envelopes.ErrorCodeHostError, err.Error())
	}
}

func emptySessionPhaseStateResult(roomID string) sessionPhaseStateResult {
	return sessionPhaseStateResult{
		RoomID:        roomID,
		PhasesVisited: []string{},
		PhaseOutputs:  map[string]room.PhaseOutput{},
	}
}

func emptySessionGetResult() sessionGetResult {
	return sessionGetResult{
		PhasesVisited:         []string{},
		PhaseOutputs:          map[string]room.PhaseOutput{},
		AcceptedDraftBlocks:   []room.DraftBlock{},
		ProseRevisionOutcomes: []room.ProseRevisionOutcome{},
		EnvelopesHistory:      []room.EnvelopeHistory{},
	}
}

func toolJSONResult[T any](payload T) (*mcpsdk.CallToolResult, T) {
	body, err := json.Marshal(payload)
	if err != nil {
		return toolErrorResult(envelopes.ErrorCodeHostError, fmt.Sprintf("marshal tool result: %v", err)), payload
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}, payload
}

func (s *Server) logWorkflowRoomCreated(workflow, roomID, envelopeID string) {
	url := s.roomURL(roomID)
	if url != "" {
		slog.Default().Info(workflow+" room created", "room", roomID, "url", url, "envelope", envelopeID)
		return
	}
	slog.Default().Info(workflow+" room created", "room", roomID, "envelope", envelopeID)
}

func (s *Server) logWorkflowRoomReused(workflow, roomID, envelopeID string) {
	slog.Default().Info(workflow+" routed to existing room", "room", roomID, "envelope", envelopeID)
}
