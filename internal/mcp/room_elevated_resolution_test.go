package mcp_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// TestElevatedRoomAnswerRecordsOperatorToken: a room answer records the
// identity of the socket that answered (CW-20261016-0174). The room presents
// under the fixed loopback-unverified participant; an operator-token socket
// supersedes it by assurance order and the resolution says operator-token.
func TestElevatedRoomAnswerRecordsOperatorToken(t *testing.T) {
	elevated := func(*http.Request) (room.ParticipantBinding, error) {
		return room.ParticipantBinding{
			Scope: authz.ParticipantScope, PrincipalRef: "local-operator",
			Authority: "tangent-loopback", Assurance: "operator-token",
			Capabilities: authz.FormatCapabilities(authz.DefaultParticipantCapabilities()),
		}, nil
	}
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: fastPathWindow, participant: elevated})
	defer rg.cleanup()
	fixture := shippedRoomWorkflows()[0]

	roomID, _ := createSession(t, rg, fixture.tool)
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	const envelopeID = "elevated-1"
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()
	frame := readWSFrame(t, conn, 5*time.Second)
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)
	if result := <-done; result.err != nil || result.result.IsError {
		t.Fatalf("%s did not complete: %v", fixture.tool, result.err)
	}

	interactionID := singleInteractionID(t, rg, envelopeID)
	var assurance string
	if err := rg.db.QueryRow(`SELECT participant_assurance FROM resolutions WHERE interaction_id = ?`,
		interactionID).Scan(&assurance); err != nil || assurance != "operator-token" {
		t.Fatalf("room resolution assurance = %q, %v; want operator-token", assurance, err)
	}
	var superseded int
	if err := rg.db.QueryRow(`SELECT COUNT(*) FROM interaction_events WHERE interaction_id = ? AND event_type = ?`,
		interactionID, interaction.EventPresenterSuperseded).Scan(&superseded); err != nil || superseded != 1 {
		t.Fatalf("presenter_superseded events = %d, %v; want 1", superseded, err)
	}
}
