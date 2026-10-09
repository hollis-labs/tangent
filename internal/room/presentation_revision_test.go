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
	envelopes "github.com/hollis-labs/libs/ui-go/envelopes"
)

// wsPair is one dialed client/server socket pair plus the httptest server
// that produced it.
type wsPair struct {
	client *websocket.Conn
	server *websocket.Conn
}

// newWSDialer stands up a bare WebSocket endpoint and returns a dial function.
// The Room is driven directly, so the endpoint does nothing but hand the
// server side of each socket back to the test.
func newWSDialer(t *testing.T) func() wsPair {
	t.Helper()
	serverConns := make(chan *websocket.Conn, 8)
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
	return func() wsPair {
		client, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		server := <-serverConns
		t.Cleanup(func() {
			_ = client.CloseNow()
			_ = server.CloseNow()
		})
		return wsPair{client: client, server: server}
	}
}

func attach(t *testing.T, rm *Room, pair wsPair, opts AttachOptions) *Connection {
	t.Helper()
	connection, err := rm.AttachConn(context.Background(), pair.server, opts)
	if err != nil {
		t.Fatalf("attach %q: %v", opts.ClientID, err)
	}
	return connection
}

// TestPresentationRevisionRejectsReplacedAndStaleClients covers the refresh
// path: a tab reconnecting under the client id it already holds replaces its
// own predecessor, inherits the resolver lease, and gets a fresh revision. The
// replaced socket can no longer answer, and neither can a stale revision.
func TestPresentationRevisionRejectsReplacedAndStaleClients(t *testing.T) {
	mgr := NewManager(nil)
	rm := mgr.Create(nil)
	dial := newWSDialer(t)

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-1"})
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rm.Push(ctx, &envelopes.Envelope{V: 1, ID: "revision-1", Type: "triage"})
		done <- err
	}()
	firstRevision := readPresentationRevision(t, first.client)
	firstClosed := make(chan error, 1)
	go func() {
		for {
			_, _, err := first.client.Read(context.Background())
			if err != nil {
				firstClosed <- err
				return
			}
		}
	}()

	// The same tab reconnecting: same client id, so the predecessor is
	// replaced rather than joined.
	second := dial()
	secondConn := attach(t, rm, second, AttachOptions{ClientID: "tab-1"})
	if err := rm.ReplayPending(context.Background(), secondConn); err != nil {
		t.Fatalf("replay second: %v", err)
	}
	if err := <-firstClosed; websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("first connection close = %v", err)
	}
	if got := rm.ConnectionCount(); got != 1 {
		t.Fatalf("refresh left %d connections attached, want 1", got)
	}
	if role := rm.RoleOf(secondConn); role != RoleResolver {
		t.Fatalf("refreshed connection role = %q, want resolver", role)
	}
	secondRevision := readPresentationRevision(t, second.client)
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
	if err := rm.HandleResponseFrom(firstConn, "revision-1", firstRevision, response); !errors.Is(err, ErrStaleConnection) {
		t.Fatalf("replaced connection result = %v, want ErrStaleConnection", err)
	}
	if err := rm.HandleResponseFrom(secondConn, "revision-1", firstRevision, response); !errors.Is(err, ErrPresentationRevisionConflict) {
		t.Fatalf("old revision result = %v, want ErrPresentationRevisionConflict", err)
	}
	if err := rm.HandleResponseFrom(secondConn, "revision-1", 0, response); !errors.Is(err, ErrPresentationRevisionConflict) {
		t.Fatalf("zero revision after reconnect = %v, want ErrPresentationRevisionConflict", err)
	}
	if !rm.HasPending() {
		t.Fatal("stale dispositions terminalized pending work")
	}
	if err := rm.HandleResponseFrom(secondConn, "revision-1", secondRevision, response); err != nil {
		t.Fatalf("current revision response: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func TestAttachConnRejectsClosedRoom(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	rm.Close("operator closed surface")
	if _, err := rm.AttachConn(context.Background(), nil, AttachOptions{}); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("AttachConn on closed room = %v, want ErrRoomClosed", err)
	}
	if rm.HasConn() {
		t.Fatal("closed room retained a connection")
	}
}

// readPresentationRevision reads until an envelope frame arrives. Connection
// and sync frames interleave with presentations by design, so a reader that
// insisted on the next frame being an envelope would be asserting an
// ordering the protocol does not promise.
func readPresentationRevision(t *testing.T, conn *websocket.Conn) int64 {
	t.Helper()
	frame := readFrameOfType(t, conn, "envelope")
	var decoded struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(frame, &decoded); err != nil {
		t.Fatalf("decode presentation: %v", err)
	}
	if decoded.Revision <= 0 {
		t.Fatalf("presentation revision = %d", decoded.Revision)
	}
	return decoded.Revision
}

// readFrameOfType reads frames until one of the requested type arrives.
func readFrameOfType(t *testing.T, conn *websocket.Conn, frameType string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %s frame: %v", frameType, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if envelope.Type == frameType {
			return payload
		}
	}
}
