package ws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/uicommand"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

// Only synthetic host-resolved bindings are used. No participant cookie,
// provider, live room or actual operator data is involved.
func TestUICommandWebSocketRoundTrip(t *testing.T) {
	binding := uicommand.Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-session-reference"}
	broker := uicommand.New(func(context.Context, uicommand.Access) (uicommand.Binding, error) { return binding, nil }, time.Second)
	mgr := room.NewManager(nil)
	h := tangentws.New(mgr, nil)
	h.SetUICommands(broker, func(*http.Request, *room.Connection) (uicommand.Binding, error) { return binding, nil })
	base := newHandlerServer(t, h)
	rm := mgr.Create(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, wsURL(base, rm.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(websocket.StatusNormalClosure, "test done")
	readFrameOfType(t, client, "sync", time.Second)
	writeFrame(t, client, map[string]any{"type": "view.publish", "descriptor": uicommand.Descriptor{Version: 1, Route: "/inbox", Commands: uicommand.CoreCommands()}})
	writeFrame(t, client, map[string]any{"type": "view.active", "active": true})
	writeFrame(t, client, map[string]any{"type": "ui.control", "enabled": true})
	// A rejected malformed frame is an ordering barrier proving the preceding
	// publish/activity/control frames have been handled on the same socket.
	writeFrame(t, client, map[string]any{"type": "ui.control", "enabled": true, "session_ref": "wire-must-not-bind"})
	refusal := readFrameOfType(t, client, "error", time.Second)
	if refusal["code"] != "ui_command_rejected" {
		t.Fatalf("bad refusal: %+v", refusal)
	}
	snap, err := broker.Get(ctx)
	if err != nil || snap.Descriptor.Route != "/inbox" {
		t.Fatalf("get: %+v %v", snap, err)
	}
	done := make(chan error, 1)
	go func() {
		ack, err := broker.Command(ctx, "focus_item", json.RawMessage(`{"id":"synthetic-item"}`))
		if err == nil && ack.Status != "applied" {
			t.Error("wrong status")
		}
		done <- err
	}()
	frame := readFrameOfType(t, client, "ui.command", time.Second)
	if frame["scope"] != "ephemeral" || frame["name"] != "focus_item" {
		t.Fatalf("bad command: %+v", frame)
	}
	writeFrame(t, client, map[string]any{"type": "ui.ack", "ack": map[string]any{"command_id": frame["command_id"], "view_revision": frame["view_revision"], "status": "applied"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestUIFramesRefusedWithoutTrustedAttachmentResolver(t *testing.T) {
	binding := uicommand.Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-session"}
	broker := uicommand.New(func(context.Context, uicommand.Access) (uicommand.Binding, error) { return binding, nil }, time.Second)
	mgr := room.NewManager(nil)
	h := tangentws.New(mgr, nil)
	h.SetUICommands(broker, nil)
	base := newHandlerServer(t, h)
	rm := mgr.Create(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, wsURL(base, rm.ID)+"&clientID=synthetic-participant", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(websocket.StatusNormalClosure, "test done")
	readFrameOfType(t, client, "sync", time.Second)
	writeFrame(t, client, map[string]any{"type": "view.publish", "descriptor": uicommand.Descriptor{Version: 1, Route: "/inbox", Commands: uicommand.CoreCommands()}})
	if frame := readFrameOfType(t, client, "error", time.Second); frame["code"] != "ui_command_rejected" {
		t.Fatal(frame)
	}
	if _, err := broker.Get(ctx); err == nil {
		t.Fatal("unbound attachment disclosed a descriptor")
	}
	// Ordinary room synchronization still works; UI frames do not acquire leases
	// or change drafts/envelopes.
	writeFrame(t, client, map[string]any{"type": "resync"})
	readFrameOfType(t, client, "sync", time.Second)
}
