package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/authz"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CW-20260910-0031 — replacing a long-lived board in a live room.
//
// # The gap this closes
//
// `Room.Release` has always documented itself as the path for "a durable
// presentation whose interaction became terminal somewhere other than this
// room — a caller withdrawal or a surface policy disposition". Nothing called
// it for a caller withdrawal. A participant's own resolve or cancel drops the
// room's pending entry on its way through `Room`; a caller's `interaction_cancel`
// did not, so the browser kept rendering an envelope whose interaction was
// already terminal and the room stayed "busy" against every subsequent advance.
//
// That is not an abstract tidiness problem. It is the difference between a
// board that can be refreshed and one that can only be abandoned: refreshing a
// board means withdrawing the one on screen and advancing a fresh one onto the
// same room, and the second step was refused as `session_busy` forever.
//
// These tests drive the whole cycle the way the Torque board plugin does.

// TestARoomRefusesASecondEnvelopeWhileOneIsPending is the precondition, pinned
// so the test below is measuring the fix rather than an absence.
func TestARoomRefusesASecondEnvelopeWhileOneIsPending(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const first = "board-refresh-busy-1"
	receipt, conn := openAsyncBoard(t, rg, first)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	result := advanceSecondBoard(t, rg, receipt.Handle.RoomID, "board-refresh-busy-2")
	if !result.IsError {
		t.Fatal("a second envelope was accepted while one was pending")
	}
	if body := extractText(t, result); !strings.Contains(body, "pending envelope") {
		t.Errorf("refusal = %s, want the busy room named", body)
	}
}

// TestWithdrawingABoardLetsTheRoomShowAFreshOne is the fix, and it is the
// mechanism the board's Sync button rests on.
func TestWithdrawingABoardLetsTheRoomShowAFreshOne(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const first = "board-refresh-1"
	const second = "board-refresh-2"
	receipt, conn := openAsyncBoard(t, rg, first)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	withdrawInteraction(t, rg, receipt.Handle.SurfaceID, receipt.Handle.InteractionID)

	result := advanceSecondBoard(t, rg, receipt.Handle.RoomID, second)
	if result.IsError {
		t.Fatalf("the refreshed board was refused after a withdrawal: %s", extractText(t, result))
	}

	// The browser is shown the replacement. A refresh the participant cannot
	// see is not a refresh.
	deadline := time.Now().Add(5 * time.Second)
	for {
		frame := readWSFrame(t, conn, time.Until(deadline))
		if frame["type"] == "envelope" && frame["envelopeId"] == second {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the replacement envelope never reached the browser")
		}
	}

	// Both interactions are on one surface: the first terminal, the second
	// pending. The board's history is the surface's, which is what makes
	// "what happened to this board" answerable afterwards.
	snapshot := getSurface(t, rg, receipt.Handle.SurfaceID)
	if len(snapshot.Interactions) != 2 {
		t.Fatalf("interactions = %d, want the withdrawn board and its replacement",
			len(snapshot.Interactions))
	}
	states := map[string]string{}
	for _, record := range snapshot.Interactions {
		states[record.ID] = record.State
	}
	if states[receipt.Handle.InteractionID] != "canceled" {
		t.Errorf("the withdrawn board is in state %q", states[receipt.Handle.InteractionID])
	}
}

// TestWithdrawingAnInteractionInNoRoomIsHarmless: the generic interaction tools
// serve callers with no room at all, and retiring a presentation that does not
// exist must not become an error they have to handle.
func TestWithdrawingAnInteractionInNoRoomIsHarmless(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
	defer rg.cleanup()

	const envelopeID = "board-refresh-noroom"
	receipt, conn := openAsyncBoard(t, rg, envelopeID)
	// Closing every room drops the presentation the withdrawal would retire.
	conn.Close(websocket.StatusNormalClosure, "done")
	rg.mgr.CloseAll("test: rooms gone")

	withdrawInteraction(t, rg, receipt.Handle.SurfaceID, receipt.Handle.InteractionID)
}

// advanceSecondBoard pushes a different app-board envelope onto an existing
// room, which is exactly what a sync does after withdrawing the current one.
func advanceSecondBoard(t *testing.T, rg *sessionRig, roomID, envelopeID string) *mcpsdk.CallToolResult {
	t.Helper()
	fixture := fixtureByTool(t, "tangent.app-board")
	result := callWorkflow(t, rg, fixture, roomID, envelopeID, map[string]any{"mode": "async"}, nil)
	if result.err != nil {
		t.Fatalf("second app-board transport: %v", result.err)
	}
	return result.result
}

// withdrawInteraction is the caller-side terminal disposition a board refresh
// performs. `caller_withdrawn` is the honest cause: the caller is replacing its
// own interaction, and recording that the participant cancelled would put a
// wrong fact in an immutable record.
func withdrawInteraction(t *testing.T, rg *sessionRig, surfaceID, interactionID string) {
	t.Helper()
	// Retried once on a stale revision, which is what the Torque board plugin
	// does and for the same reason: presenting an envelope to a browser bumps
	// the interaction's revision, and that happens asynchronously after the
	// frame is delivered. A withdrawal issued in that window reads revision N
	// and finds N+1 by the time it writes. Nothing about the board changed —
	// only its presentation state — so re-reading and retrying is the honest
	// response, and a test that papered over it with a sleep would be hiding
	// the very race the plugin has to handle.
	for attempt := range 2 {
		revision := int64(0)
		for _, record := range getSurface(t, rg, surfaceID).Interactions {
			if record.ID == interactionID {
				revision = record.Revision
			}
		}
		if revision == 0 {
			t.Fatalf("interaction %s is not on surface %s", interactionID, surfaceID)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name: "tangent.interaction_cancel",
			Arguments: map[string]any{
				"interaction_id":    interactionID,
				"expected_revision": revision,
				// The caller partition a room workflow resolves to. A room
				// workflow carries no caller argument, so it owns its
				// interactions as the anonymous partition of the local
				// authority, and only that caller may withdraw one.
				"requester": map[string]any{
					"scope": authz.PartitionAnonymous, "principal_ref": "loopback-mcp-caller",
				},
				"cause":  "caller_withdrawn",
				"reason": "replaced by a synced board",
			},
		})
		cancel()
		if err != nil {
			t.Fatalf("interaction_cancel transport: %v", err)
		}
		if !result.IsError {
			return
		}
		body := extractText(t, result)
		if attempt == 1 || !strings.Contains(body, "stale_revision") {
			t.Fatalf("interaction_cancel failed: %s", body)
		}
	}
}
