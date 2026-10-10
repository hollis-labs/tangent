package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/uicommand"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func uiClient(t *testing.T, broker *uicommand.Broker) *mcpsdk.ClientSession {
	t.Helper()
	env, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var options []tangentmcp.Option
	if broker != nil {
		options = append(options, tangentmcp.WithUICommands(broker))
	}
	server, err := tangentmcp.New(env, envelope.NewDispatcher(env), room.NewManager(nil), "", options...)
	if err != nil {
		t.Fatal(err)
	}
	client, closeClient := connectInteractionClient(t, server)
	t.Cleanup(closeClient)
	return client
}

func uiRefusal(t *testing.T, client *mcpsdk.ClientSession, name string, args map[string]any, code string) {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		if code == "schema" {
			return
		}
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || (code != "schema" && !strings.Contains(string(raw), code)) {
		t.Fatalf("expected %s refusal: %s", code, raw)
	}
}

func TestUICommandsDefaultRefusalAndNoTargetAssertions(t *testing.T) {
	client := uiClient(t, nil)
	uiRefusal(t, client, "tangent.view_get", nil, "ui_unavailable")
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_unavailable")
	uiRefusal(t, client, "tangent.view_get", map[string]any{"participant_ref": "asserted"}, "schema")
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic", "session_ref": "asserted"}, "schema")
	client = uiClient(t, uicommand.New(nil, time.Second))
	uiRefusal(t, client, "tangent.view_get", nil, "ui_forbidden")
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_forbidden")
}

func TestUICommandsBrowserAcknowledgementAndAuthority(t *testing.T) {
	binding := uicommand.Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-session"}
	broker := uicommand.New(func(context.Context, uicommand.Access) (uicommand.Binding, error) { return binding, nil }, 40*time.Millisecond)
	status := "applied"
	sends := 0
	if err := broker.Attach("synthetic-attachment", binding, func(_ context.Context, frame uicommand.Frame) error {
		sends++
		if status == "timeout" {
			return nil
		}
		if err := broker.Acknowledge("peer", uicommand.Ack{CommandID: frame.CommandID, ViewRevision: frame.ViewRevision, Status: status}); !errors.Is(err, uicommand.ErrStale) {
			t.Errorf("peer ack: %v", err)
		}
		if err := broker.Acknowledge("synthetic-attachment", uicommand.Ack{CommandID: frame.CommandID, ViewRevision: frame.ViewRevision + 1, Status: status}); !errors.Is(err, uicommand.ErrStale) {
			t.Errorf("stale ack: %v", err)
		}
		return broker.Acknowledge("synthetic-attachment", uicommand.Ack{CommandID: frame.CommandID, ViewRevision: frame.ViewRevision, Status: status})
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(uicommand.Descriptor{Version: 1, Route: "/inbox", Commands: []uicommand.Declaration{uicommand.CoreCommands()[5]}})
	revision, err := broker.Publish("synthetic-attachment", raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = broker.Active("synthetic-attachment", true); err != nil {
		t.Fatal(err)
	}
	client := uiClient(t, broker)
	snapshot := callInteractionTool[uicommand.Snapshot](t, client, "tangent.view_get", nil)
	if snapshot.Revision != revision || snapshot.ControlEnabled {
		t.Fatalf("snapshot: %#v", snapshot)
	}
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_disabled")
	if err = broker.SetControl("synthetic-attachment", true); err != nil {
		t.Fatal(err)
	}
	uiRefusal(t, client, "tangent.ui_navigate", map[string]any{"route": "/inbox"}, "ui_rejected")
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": 7}, "schema")
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": ""}, "schema")
	if sends != 0 {
		t.Fatal("refused command delivered")
	}
	for _, value := range []string{"applied", "not_visible", "rejected"} {
		status = value
		ack := callInteractionTool[uicommand.Ack](t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"})
		if ack.Status != value || ack.CommandID == "" || ack.ViewRevision != revision {
			t.Fatalf("ack: %#v", ack)
		}
	}
	status = "timeout"
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "timeout")
	if err = broker.SetControl("synthetic-attachment", false); err != nil {
		t.Fatal(err)
	}
	callInteractionTool[uicommand.Snapshot](t, client, "tangent.view_get", nil)
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_disabled")
	broker.Detach("synthetic-attachment")
	uiRefusal(t, client, "tangent.view_get", nil, "not_visible")
}

func TestUICommandsRecheckVerifiedAuthority(t *testing.T) {
	for _, access := range []uicommand.Access{uicommand.Read, uicommand.Control} {
		t.Run(string(access), func(t *testing.T) {
			binding := uicommand.Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-session"}
			checks, sends := 0, 0
			broker := uicommand.New(func(_ context.Context, requested uicommand.Access) (uicommand.Binding, error) {
				if requested != access {
					t.Errorf("unexpected access %s", requested)
				}
				checks++
				if checks > 1 {
					return uicommand.Binding{}, uicommand.ErrForbidden
				}
				return binding, nil
			}, time.Second)
			if err := broker.Attach("synthetic", binding, func(context.Context, uicommand.Frame) error { sends++; return nil }); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(uicommand.Descriptor{Version: 1, Route: "/inbox", Commands: uicommand.CoreCommands()})
			if _, err := broker.Publish("synthetic", raw); err != nil {
				t.Fatal(err)
			}
			if err := broker.Active("synthetic", true); err != nil {
				t.Fatal(err)
			}
			if err := broker.SetControl("synthetic", true); err != nil {
				t.Fatal(err)
			}
			client := uiClient(t, broker)
			if access == uicommand.Read {
				uiRefusal(t, client, "tangent.view_get", nil, "ui_forbidden")
			} else {
				uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_forbidden")
			}
			if checks != 2 || sends != 0 {
				t.Fatalf("checks=%d sends=%d", checks, sends)
			}
		})
	}
}

func TestUICommandsRefuseACKDisclosureAfterSessionChanges(t *testing.T) {
	binding := uicommand.Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-session"}
	current := binding
	broker := uicommand.New(func(context.Context, uicommand.Access) (uicommand.Binding, error) { return current, nil }, time.Second)
	if err := broker.Attach("synthetic", binding, func(_ context.Context, frame uicommand.Frame) error {
		current.SessionRef = "synthetic-replacement-session"
		return broker.Acknowledge("synthetic", uicommand.Ack{CommandID: frame.CommandID, ViewRevision: frame.ViewRevision, Status: "applied"})
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(uicommand.Descriptor{Version: 1, Route: "/inbox", Commands: uicommand.CoreCommands()})
	if _, err := broker.Publish("synthetic", raw); err != nil {
		t.Fatal(err)
	}
	if err := broker.Active("synthetic", true); err != nil {
		t.Fatal(err)
	}
	if err := broker.SetControl("synthetic", true); err != nil {
		t.Fatal(err)
	}
	client := uiClient(t, broker)
	uiRefusal(t, client, "tangent.ui_focus_item", map[string]any{"id": "synthetic"}, "ui_forbidden")
	uiRefusal(t, client, "tangent.view_get", nil, "not_visible")
}
