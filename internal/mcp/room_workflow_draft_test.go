package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// CW-20260909-0046 — the draft round trip, end to end.
//
// # Why this file exists
//
// `Service.SaveDraft` was complete, revisioned, conflict-detecting and
// capability-gated for weeks with **zero callers**. When one was finally wired,
// it shipped with a defect that no test could have caught: `saveDraft`
// defaulted the revision to 1, so any consumer would have saved exactly once
// and then conflicted forever (fixed in f2b0892). No test caught it because
// there was no consumer, and every test of SaveDraft called it directly with
// the revision the test chose.
//
// That is the specific gap here. These tests drive the whole chain the way a
// browser does — MCP call → pending envelope → WebSocket `draft` frame →
// `Room.HandleDraftFrom` → `roomDisposition.Draft` → `interaction.SaveDraft` →
// `tangent.surface_get` — and assert what a caller can actually observe. A test
// that called SaveDraft directly would have passed against the broken client.
//
// The board is the consumer, so it is what drives them. Nothing here is
// board-specific: the chain is the same for any kind whose manifest declares
// `draft_custody: tangent-custodied`.

// surfaceSnapshot is the part of tangent.surface_get these tests read.
type surfaceSnapshot struct {
	Surface struct {
		SurfaceID string `json:"surface_id"`
		State     string `json:"state"`
	} `json:"surface"`
	Interactions []struct {
		ID    string `json:"interaction_id"`
		State string `json:"state"`
		// Revision is what a caller-side terminal disposition has to pin. A
		// board refresh reads it back here rather than assuming it, because
		// presentation bumps it and an assumed 1 is a stale_revision refusal.
		Revision int64 `json:"revision"`
	} `json:"interactions"`
	Drafts      []interaction.DraftRevision `json:"drafts"`
	Resolutions []struct {
		ID string `json:"resolution_id"`
	} `json:"resolutions"`
}

func getSurface(t *testing.T, rg *sessionRig, surfaceID string) surfaceSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.surface_get",
		// The room workflow's own caller scope. roomflow.DefaultCaller owns the
		// surface, so this is the caller reading back what it opened — which is
		// the pull ADR 0007 §5 describes, not a third party reading someone
		// else's view state.
		Arguments: map[string]any{
			"surface_id": surfaceID, "requester_scope": authz.PartitionAnonymous,
		},
	})
	if err != nil {
		t.Fatalf("surface_get transport: %v", err)
	}
	if res.IsError {
		t.Fatalf("surface_get failed: %s", extractText(t, res))
	}
	var snapshot surfaceSnapshot
	if err := json.Unmarshal([]byte(extractText(t, res)), &snapshot); err != nil {
		t.Fatalf("decode surface snapshot: %v (body %s)", err, extractText(t, res))
	}
	return snapshot
}

// openAsyncBoard dispatches an app-board with completion async and returns the
// pending receipt plus a live browser socket that has seen the envelope.
func openAsyncBoard(t *testing.T, rg *sessionRig, envelopeID string) (pendingReceipt, *websocket.Conn) {
	t.Helper()
	fixture := fixtureByTool(t, "tangent.app-board")
	roomID, _ := createSession(t, rg, "app-board")

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}

	result := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if result.err != nil {
		t.Fatalf("app-board transport: %v", result.err)
	}
	if result.result.IsError {
		t.Fatalf("app-board failed: %s", extractText(t, result.result))
	}
	var receipt pendingReceipt
	if err := json.Unmarshal([]byte(extractText(t, result.result)), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if receipt.Status != "pending" {
		t.Fatalf("completion async did not return a pending receipt: %s", extractText(t, result.result))
	}

	// The browser has to have seen the envelope before it can draft against it.
	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "envelope" || frame["envelopeId"] != envelopeID {
		t.Fatalf("first frame = %v, want the envelope presentation", frame)
	}
	return receipt, conn
}

func draftFrame(envelopeID string, revision int64, payload map[string]any) map[string]any {
	return map[string]any{
		"type": "draft", "envelopeId": envelopeID,
		"draftRevision": revision, "draft": payload,
	}
}

// TestAppBoardDraft_StaysPendingAndIsReadableByTheCaller is the whole ADR 0007
// §5 claim in one test: the participant's view state becomes readable by the
// caller **without** the interaction settling.
//
// The pending assertion is not decoration. If a draft settled the envelope, the
// board would close the moment the operator touched a filter, and the caller
// would receive a resolution it never asked for. The empty-resolutions
// assertion is the same fact from the other side: nothing terminal was
// recorded, so a caller polling for an outcome still correctly sees none.
func TestAppBoardDraft_StaysPendingAndIsReadableByTheCaller(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const envelopeID = "board-draft-001"
	receipt, conn := openAsyncBoard(t, rg, envelopeID)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	before := getSurface(t, rg, receipt.Handle.SurfaceID)
	if len(before.Drafts) != 0 {
		t.Fatalf("surface carried %d drafts before the browser sent one", len(before.Drafts))
	}

	writeWSFrame(t, conn, draftFrame(envelopeID, 1, map[string]any{
		"board_id":         "board-1",
		"filters":          map[string]any{"status": []any{"doing"}},
		"selected_card_id": "CW-1",
		"detail_open":      true,
	}))

	after := waitForDrafts(t, rg, receipt.Handle.SurfaceID, 1)

	draft := after.Drafts[0]
	if draft.InteractionID != receipt.Handle.InteractionID {
		t.Errorf("draft belongs to interaction %q, want %q",
			draft.InteractionID, receipt.Handle.InteractionID)
	}
	if draft.Revision != 1 {
		t.Errorf("first draft revision = %d, want 1", draft.Revision)
	}

	// The payload is the browser's, byte for byte in meaning. A draft the host
	// reinterpreted would not be view state any more.
	var payload map[string]any
	if err := json.Unmarshal(draft.Payload, &payload); err != nil {
		t.Fatalf("decode draft payload: %v", err)
	}
	if payload["selected_card_id"] != "CW-1" {
		t.Errorf("draft payload lost the selection: %v", payload)
	}
	if payload["detail_open"] != true {
		t.Errorf("draft payload lost the detail state: %v", payload)
	}

	// Still pending, still unresolved. This is the point.
	if len(after.Resolutions) != 0 {
		t.Errorf("a draft recorded %d resolutions; a draft is not a decision",
			len(after.Resolutions))
	}
	if len(after.Interactions) != 1 {
		t.Fatalf("surface carries %d interactions, want 1", len(after.Interactions))
	}
	// "canceled", not "cancelled": InteractionState has always used the US
	// spelling, so the British half of this comparison was dead from the day
	// it was written and the test could not have caught a draft that canceled
	// the interaction. Found by retiring the misspell ignore-rule in
	// CW-20260904-0168.
	if state := after.Interactions[0].State; state == "resolved" || state == "canceled" {
		t.Errorf("interaction state = %q after a draft; the surface must stay open", state)
	}
}

// TestAppBoardDraft_SecondDraftAdvancesTheRevision.
//
// The store computes MAX(revision) + 1 and refuses anything else, so a client
// that does not advance its counter saves once and conflicts forever. That was
// the real defect (f2b0892), and this is the assertion that would have caught
// it: two drafts from one socket, revisions 1 and 2, both retained.
//
// Both are kept rather than the second overwriting the first. A draft history
// is what lets a caller see that the participant narrowed a filter and then
// widened it again, which is view state a single latest-value slot would erase.
func TestAppBoardDraft_SecondDraftAdvancesTheRevision(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const envelopeID = "board-draft-002"
	receipt, conn := openAsyncBoard(t, rg, envelopeID)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	writeWSFrame(t, conn, draftFrame(envelopeID, 1, map[string]any{
		"board_id": "board-1", "filters": map[string]any{"status": []any{"doing"}},
	}))
	waitForDrafts(t, rg, receipt.Handle.SurfaceID, 1)

	writeWSFrame(t, conn, draftFrame(envelopeID, 2, map[string]any{
		"board_id": "board-1", "filters": map[string]any{"status": []any{"doing", "review"}},
	}))
	snapshot := waitForDrafts(t, rg, receipt.Handle.SurfaceID, 2)

	revisions := make([]int64, 0, len(snapshot.Drafts))
	for _, draft := range snapshot.Drafts {
		revisions = append(revisions, draft.Revision)
	}
	if len(revisions) != 2 || revisions[0] != 1 || revisions[1] != 2 {
		t.Fatalf("draft revisions = %v, want [1 2]", revisions)
	}

	var latest map[string]any
	if err := json.Unmarshal(snapshot.Drafts[1].Payload, &latest); err != nil {
		t.Fatalf("decode second draft: %v", err)
	}
	filters, _ := latest["filters"].(map[string]any)
	selected, _ := filters["status"].([]any)
	if len(selected) != 2 {
		t.Errorf("second draft did not carry the widened filter: %v", latest)
	}

	// And still nothing terminal, after two drafts.
	if len(snapshot.Resolutions) != 0 {
		t.Errorf("two drafts recorded %d resolutions", len(snapshot.Resolutions))
	}
}

// TestAppBoardDraft_StaleRevisionIsRefusedAndWritesNothing.
//
// "Refused, never merged" (ADR 0007 §5) has two halves, and only the first is
// visible to the client. The error frame proves the refusal; the draft count
// proves nothing was written — a refusal that still persisted the payload would
// look identical from the socket and would silently reorder a participant's
// view state behind their back.
//
// It also pins the known limitation honestly: the frame echoes the revision the
// CLIENT sent, not the one the record expects, so a client that falls behind
// cannot resynchronize from this frame alone. Single-tab drafting is fine.
// Closing that means carrying the expected revision out of SaveDraftRevision,
// where the value already exists — and this assertion is what will fail, on
// purpose, when someone does.
func TestAppBoardDraft_StaleRevisionIsRefusedAndWritesNothing(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const envelopeID = "board-draft-003"
	receipt, conn := openAsyncBoard(t, rg, envelopeID)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	writeWSFrame(t, conn, draftFrame(envelopeID, 1, map[string]any{
		"board_id": "board-1", "selected_card_id": "CW-1",
	}))
	waitForDrafts(t, rg, receipt.Handle.SurfaceID, 1)

	// A replay of a revision the record has already moved past — what a second
	// tab, or a client that lost a frame, would send.
	writeWSFrame(t, conn, draftFrame(envelopeID, 1, map[string]any{
		"board_id": "board-1", "selected_card_id": "SHOULD-NOT-PERSIST",
	}))

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "error" {
		t.Fatalf("stale draft produced frame %v, want an error frame", frame)
	}
	if frame["code"] != "stale_draft" {
		t.Fatalf("error code = %v, want stale_draft", frame["code"])
	}
	if frame["envelopeId"] != envelopeID {
		t.Errorf("error frame names envelope %v, want %s", frame["envelopeId"], envelopeID)
	}
	// The known limitation, asserted rather than described: this is the
	// client's own revision coming back, which is why it is not enough to
	// resynchronize from.
	if revision, ok := frame["revision"].(float64); !ok || int64(revision) != 1 {
		t.Errorf("error frame revision = %v, want the client's own 1", frame["revision"])
	}

	// Nothing was written. One draft, and it is still the first one.
	snapshot := getSurface(t, rg, receipt.Handle.SurfaceID)
	if len(snapshot.Drafts) != 1 {
		t.Fatalf("a refused draft left %d drafts, want 1", len(snapshot.Drafts))
	}
	var payload map[string]any
	if err := json.Unmarshal(snapshot.Drafts[0].Payload, &payload); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if payload["selected_card_id"] != "CW-1" {
		t.Errorf("the refused payload reached the record: %v", payload)
	}
	if len(snapshot.Resolutions) != 0 {
		t.Errorf("a refused draft recorded %d resolutions", len(snapshot.Resolutions))
	}
}

// TestAppBoardDraft_DoesNotTakeTheResolverLease.
//
// Room.HandleDraftFrom deliberately bypasses authorizeDisposition. If it did
// not, the first tab to draft would hold the lease and the second would be
// refused — so opening a board in a second tab would silently steal the right
// to answer from the first, or lock it out of drafting.
//
// Two sockets, both drafting, both accepted, and the envelope still resolvable
// afterwards. internal/room holds this at the unit level; this holds it through
// the real WebSocket handler, which is where the lease is actually claimed.
func TestAppBoardDraft_DoesNotTakeTheResolverLease(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const envelopeID = "board-draft-004"
	receipt, first := openAsyncBoard(t, rg, envelopeID)
	defer first.Close(websocket.StatusNormalClosure, "done")

	second, _, err := websocket.Dial(context.Background(), rg.wsURL(receipt.Handle.RoomID), nil)
	if err != nil {
		t.Fatalf("second ws dial: %v", err)
	}
	defer second.Close(websocket.StatusNormalClosure, "done")
	// The attaching tab is synchronized from durable state and shown the
	// pending envelope.
	if frame := readWSFrame(t, second, 5*time.Second); frame["envelopeId"] != envelopeID {
		t.Fatalf("second tab was shown %v, want the pending envelope", frame)
	}

	writeWSFrame(t, first, draftFrame(envelopeID, 1, map[string]any{
		"board_id": "board-1", "selected_card_id": "CW-1",
	}))
	waitForDrafts(t, rg, receipt.Handle.SurfaceID, 1)

	// The second tab drafts too. It has its own counter, so it sends 1 as well
	// and is refused on revision — which is the documented single-tab
	// limitation, NOT a lease refusal. The distinction is the whole test: a
	// lease refusal would be `resolver_lease_held`.
	writeWSFrame(t, second, draftFrame(envelopeID, 1, map[string]any{
		"board_id": "board-1", "selected_card_id": "CW-2",
	}))
	frame := readWSFrame(t, second, 5*time.Second)
	if frame["code"] == "resolver_lease_held" {
		t.Fatal("drafting from a second tab was refused for the resolver lease; " +
			"HandleDraftFrom must not take it")
	}
	if frame["code"] != "stale_draft" {
		t.Fatalf("second tab draft refused with %v, want stale_draft", frame["code"])
	}

	// And a draft from a tab that never claimed the lease advances cleanly once
	// it uses the current revision — proving the refusal above was about the
	// revision and nothing else.
	writeWSFrame(t, second, draftFrame(envelopeID, 2, map[string]any{
		"board_id": "board-1", "selected_card_id": "CW-2",
	}))
	snapshot := waitForDrafts(t, rg, receipt.Handle.SurfaceID, 2)
	if len(snapshot.Resolutions) != 0 {
		t.Errorf("drafting from two tabs recorded %d resolutions", len(snapshot.Resolutions))
	}
}

// waitForDrafts polls surface_get until the surface carries want drafts.
//
// It polls because the draft frame is fire-and-forget: the WebSocket handler
// acknowledges nothing on success, which is deliberate — a draft is not a
// submission and a client must not wait on one. So the test waits for the
// observable consequence rather than for a frame that is never sent.
func waitForDrafts(t *testing.T, rg *sessionRig, surfaceID string, want int) surfaceSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var snapshot surfaceSnapshot
	for time.Now().Before(deadline) {
		snapshot = getSurface(t, rg, surfaceID)
		if len(snapshot.Drafts) >= want {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("surface carried %d drafts within the deadline, want %d", len(snapshot.Drafts), want)
	return snapshot
}
