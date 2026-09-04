package room

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"
)

func TestPresentationRevisionRejectsReplacedAndStaleClients(t *testing.T) {
	mgr := NewManager(nil)
	rm := mgr.Create(nil)
	serverConns := make(chan *websocket.Conn, 2)
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		serverConns <- conn
		<-stop
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	dial := func() (*websocket.Conn, *websocket.Conn) {
		client, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		server := <-serverConns
		t.Cleanup(func() {
			_ = client.CloseNow()
			_ = server.CloseNow()
		})
		return client, server
	}

	firstClient, firstServer := dial()
	if err := rm.AttachConn(context.Background(), firstServer); err != nil {
		t.Fatalf("attach first: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, &envelopes.Envelope{V: 1, ID: "revision-1", Type: "triage"})
		done <- err
	}()
	firstRevision := readPresentationRevision(t, firstClient)
	firstClosed := make(chan error, 1)
	go func() {
		_, _, err := firstClient.Read(context.Background())
		firstClosed <- err
	}()

	secondClient, secondServer := dial()
	if err := rm.AttachConn(context.Background(), secondServer); err != nil {
		t.Fatalf("attach second: %v", err)
	}
	if err := rm.ReplayPending(context.Background(), secondServer); err != nil {
		t.Fatalf("replay second: %v", err)
	}
	if err := <-firstClosed; websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("first connection close = %v", err)
	}
	secondRevision := readPresentationRevision(t, secondClient)
	if secondRevision <= firstRevision {
		t.Fatalf("second revision = %d, first = %d", secondRevision, firstRevision)
	}

	response := &envelopes.Response{
		V:          1,
		EnvelopeID: "revision-1",
		Kind:       envelopes.ResponseKindData,
		Status:     envelopes.ResponseStatusSubmitted,
		Payload:    map[string]any{"winner": "current"},
	}
	if err := rm.HandleResponseFrom(firstServer, "revision-1", firstRevision, response); !errors.Is(err, ErrStaleConnection) {
		t.Fatalf("replaced connection result = %v, want ErrStaleConnection", err)
	}
	if err := rm.HandleResponseFrom(secondServer, "revision-1", firstRevision, response); !errors.Is(err, ErrPresentationRevisionConflict) {
		t.Fatalf("old revision result = %v, want ErrPresentationRevisionConflict", err)
	}
	if err := rm.HandleResponseFrom(secondServer, "revision-1", 0, response); !errors.Is(err, ErrPresentationRevisionConflict) {
		t.Fatalf("zero revision after reconnect = %v, want ErrPresentationRevisionConflict", err)
	}
	if !rm.HasPending() {
		t.Fatal("stale dispositions terminalized pending work")
	}
	if err := rm.HandleResponseFrom(secondServer, "revision-1", secondRevision, response); err != nil {
		t.Fatalf("current revision response: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func TestAttachConnRejectsClosedRoom(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	rm.Close("operator closed surface")
	if err := rm.AttachConn(context.Background(), nil); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("AttachConn on closed room = %v, want ErrRoomClosed", err)
	}
	if rm.HasConn() {
		t.Fatal("closed room retained a connection")
	}
}

func readPresentationRevision(t *testing.T, conn *websocket.Conn) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read presentation: %v", err)
	}
	var frame struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode presentation: %v", err)
	}
	if frame.Revision <= 0 {
		t.Fatalf("presentation revision = %d", frame.Revision)
	}
	return frame.Revision
}
