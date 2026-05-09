package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	tangentws "github.com/hollis-labs/tangent/internal/ws"
)

func TestFeedback_RoundTrip(t *testing.T) {
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterTriage(envSvc); regErr != nil {
		t.Fatalf("RegisterTriage: %v", regErr)
	}
	if regErr := extensions.RegisterFeedback(envSvc); regErr != nil {
		t.Fatalf("RegisterFeedback: %v", regErr)
	}
	if regErr := extensions.RegisterInterviewQuestion(envSvc); regErr != nil {
		t.Fatalf("RegisterInterviewQuestion: %v", regErr)
	}
	if regErr := extensions.RegisterBlockDraft(envSvc); regErr != nil {
		t.Fatalf("RegisterBlockDraft: %v", regErr)
	}
	if regErr := extensions.RegisterProseRevision(envSvc); regErr != nil {
		t.Fatalf("RegisterProseRevision: %v", regErr)
	}
	if regErr := extensions.RegisterSynthesisNotes(envSvc); regErr != nil {
		t.Fatalf("RegisterSynthesisNotes: %v", regErr)
	}

	dispatcher := envelope.NewDispatcher(envSvc)
	manager := room.NewManager(nil)
	logger := slog.New(slog.NewTextHandler(feedbackTestLogWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv, err := tangentmcp.New(envSvc, dispatcher, manager, "")
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	wsHandler := tangentws.New(manager, logger)
	wsHandler.SetOriginPatterns([]string{"*"})
	wsSrv := httptest.NewServer(wsHandler)
	defer wsSrv.Close()

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-feedback-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer clientSession.Close()

	done := make(chan struct{})
	var res *mcpsdk.CallToolResult
	var callErr error
	go func() {
		defer close(done)
		callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, callErr = clientSession.CallTool(callCtx, &mcpsdk.CallToolParams{
			Name: "tangent.feedback",
			Arguments: map[string]any{"envelope": map[string]any{
				"v":    1,
				"id":   "feedback-roundtrip-1",
				"type": "tangent.feedback",
				"data": map[string]any{
					"questions": []any{
						map[string]any{
							"id":       "q1",
							"type":     "text",
							"label":    "What changed?",
							"required": true,
						},
					},
				},
			}},
		})
	}()

	rm := awaitRoomWithType(t, manager, "tangent.feedback", 2*time.Second)
	clientConn, _, err := websocket.Dial(context.Background(), wsURL(wsSrv.URL, rm.ID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "test done")

	frame := readFrame(t, clientConn, 3*time.Second)
	if frame["type"] != "envelope" {
		t.Fatalf("expected envelope frame, got %+v", frame)
	}
	if frame["envelopeId"] != "feedback-roundtrip-1" {
		t.Fatalf("envelopeId = %v, want feedback-roundtrip-1", frame["envelopeId"])
	}
	writeFrame(t, clientConn, map[string]any{
		"type":       "response",
		"envelopeId": "feedback-roundtrip-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "feedback-roundtrip-1",
			"kind":       "data",
			"status":     "submitted",
			"payload": map[string]any{
				"answers": []any{
					map[string]any{
						"questionId": "q1",
						"value":      "Ship it",
					},
				},
			},
			"completedAt": time.Now().UTC().Format(time.RFC3339),
		},
	})

	<-done
	if callErr != nil {
		t.Fatalf("CallTool feedback: %v", callErr)
	}
	if res == nil || res.IsError {
		t.Fatalf("feedback returned error result: %v", extractText(t, res))
	}

	var parsed envelopes.Response
	if err := json.Unmarshal([]byte(extractText(t, res)), &parsed); err != nil {
		t.Fatalf("unmarshal feedback response: %v", err)
	}
	if parsed.EnvelopeID != "feedback-roundtrip-1" {
		t.Fatalf("EnvelopeID = %q, want feedback-roundtrip-1", parsed.EnvelopeID)
	}
	if parsed.Kind != envelopes.ResponseKindData {
		t.Fatalf("Kind = %q, want %q", parsed.Kind, envelopes.ResponseKindData)
	}
	payload, _ := parsed.Payload.(map[string]any)
	answers, _ := payload["answers"].([]any)
	if len(answers) != 1 {
		t.Fatalf("answers length = %d, want 1", len(answers))
	}
}

func awaitRoomWithType(t *testing.T, mgr *room.Manager, envelopeType string, timeout time.Duration) *room.Room {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, id := range mgr.IDs() {
			rm, ok := mgr.Get(id)
			if !ok {
				continue
			}
			if rm.MetaCopy()["envelopeType"] == envelopeType {
				return rm
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no room appeared for envelope type %q within %v", envelopeType, timeout)
	return nil
}

func wsURL(baseURL, roomID string) string {
	return "ws" + strings.TrimPrefix(baseURL, "http") + "?roomID=" + roomID
}

func readFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	mt, payload, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", mt)
	}
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	return frame
}

func writeFrame(t *testing.T, c *websocket.Conn, msg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

type feedbackTestLogWriter struct{ t *testing.T }

func (w feedbackTestLogWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
