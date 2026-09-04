package room_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// readClientFrame reads the next presentation frame from conn.
//
// Connection-lifecycle frames ("connection", "sync") interleave with
// presentations by design — a client is told who else is attached and which
// durable revisions it holds independently of any envelope — so they are
// skipped here.
func readClientFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		frame := readClientFrameCtx(ctx, t, conn)
		switch frame["type"] {
		case "connection", "sync":
			continue
		default:
			return frame
		}
	}
}

func readClientFrameCtx(ctx context.Context, t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	mt, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", mt)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("client unmarshal: %v (payload=%s)", err, string(payload))
	}
	return m
}

// writeClientFrame marshals and writes a JSON object to conn as a text frame.
func writeClientFrame(t *testing.T, conn *websocket.Conn, msg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("client marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("client write: %v", err)
	}
}
