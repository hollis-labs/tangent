package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tangent/internal/telemetry"
)

// Criterion 2 asks for reconnects and stale clients. Neither is a thing the
// system had to be taught: a reconnect is a client id whose predecessor was
// still attached, and a stale client is a failed presentation compare-and-set
// or a resolver-lease refusal. Both already existed in internal/room from
// CW-20260825-0071. These tests drive the real sockets and assert the
// observations, rather than asserting that a counter can be incremented.

// TestReconnectIsObservedAsAReplacedAttachment drives the exact sequence a tab
// refresh produces: the same client id comes back while its previous socket is
// still attached.
func TestReconnectIsObservedAsAReplacedAttachment(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, "reconnect")

	first, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID)+"&clientID=tab-a", nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer first.Close(websocket.StatusNormalClosure, "done")
	// A real tab reads its socket. This one has to as well: the server
	// broadcasts connection state to every attachment before AttachConn
	// returns, and a peer that never reads makes that broadcast wait out its
	// write timeout — which would make this test measure a stalled socket
	// rather than a reconnect.
	drain(first)

	// The first attachment has to be observed before the second replaces it,
	// otherwise the test is asserting on a race rather than on a refresh.
	awaitAttachments(t, rg, roomID, 1)

	second, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID)+"&clientID=tab-a", nil)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	defer second.Close(websocket.StatusNormalClosure, "done")
	drain(second)
	records := awaitAttachments(t, rg, roomID, 2)

	var replaced, fresh int
	for _, record := range records {
		if record.Attributes["replaced"] == true {
			replaced++
			continue
		}
		fresh++
	}
	if replaced != 1 || fresh != 1 {
		t.Fatalf("attachments observed: %d replaced, %d fresh; want one of each", replaced, fresh)
	}

	// The metric is what an operator actually reads, and a reconnect has to be
	// separable from a new client in the dimensions.
	snapshot := rg.recorder.Metrics().Snapshot().Counters[telemetry.MetricConnections]
	var sawReplacedSeries bool
	for _, series := range snapshot {
		if strings.Contains(series.Dimensions, "replaced=true") {
			sawReplacedSeries = true
		}
	}
	if !sawReplacedSeries {
		t.Fatalf("no reconnect series in %+v", snapshot)
	}
}

// TestStaleClientRefusalIsFiledUnderTheInvocationTrace is the correlation
// assertion at the WebSocket boundary.
//
// The browser carries no trace. The handler reconstructs it from the durable
// record behind the presentation, so a refusal on the socket lands in the same
// trace as the MCP call that created the work — across a process boundary that
// transported nothing.
func TestStaleClientRefusalIsFiledUnderTheInvocationTrace(t *testing.T) {
	rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: 30 * time.Second})
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "stale")
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID)+"&clientID=tab-a", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	fixture := namedFixture(t, "tangent.triage")
	const envelopeID = "stale-1"
	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()

	frame := readWSFrame(t, conn, 10*time.Second)
	presented, _ := frame["revision"].(float64)

	// Answer at a revision this connection was never shown. That is precisely
	// what a stale tab does after someone else's submission moved the frame on.
	stale := fixture.participantResponse(envelopeID)
	stale["revision"] = int64(presented) + 7
	writeWSFrame(t, conn, stale)

	records := awaitEvent(t, rg, telemetry.EventPresentationRefused, roomID)
	refusal := records[0]
	if refusal.Code != "stale_presentation" {
		t.Fatalf("refusal code = %q, want stale_presentation", refusal.Code)
	}
	if refusal.InteractionID == "" {
		t.Fatal("the refusal was not correlated to an interaction")
	}
	var callerScope, idempotencyKey string
	if err := rg.db.QueryRow(
		`SELECT caller_scope, idempotency_key FROM interactions WHERE id = ?`, refusal.InteractionID,
	).Scan(&callerScope, &idempotencyKey); err != nil {
		t.Fatalf("read interaction identity: %v", err)
	}
	if want := telemetry.TraceFor(callerScope, idempotencyKey).String(); refusal.TraceID != want {
		t.Fatalf("refusal trace = %s, want the invocation trace %s", refusal.TraceID, want)
	}

	// The interaction is untouched: a refused frame is not a lifecycle event.
	if state := interactionState(t, rg, refusal.InteractionID); state == "resolved" {
		t.Fatal("a stale submission resolved the interaction")
	}

	// Answer properly so the workflow's goroutine does not outlive the test.
	// The refusal is followed by an error frame and then a fresh presentation;
	// the replay is the frame this connection may actually answer, which is the
	// whole affordance a stale client is given.
	good := fixture.participantResponse(envelopeID)
	good["revision"] = readEnvelopeFrame(t, conn)["revision"]
	writeWSFrame(t, conn, good)
	if result := <-done; result.err != nil {
		t.Fatalf("workflow transport err: %v", result.err)
	}
}

// readEnvelopeFrame reads until the next presentation frame, skipping the
// error frame the server sends first.
func readEnvelopeFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	for range 5 {
		frame := readWSFrame(t, conn, 10*time.Second)
		if frame["type"] == "envelope" {
			return frame
		}
	}
	t.Fatal("no presentation frame followed the stale-presentation refusal")
	return nil
}

// drain reads and discards frames until the socket closes, so the server's
// broadcasts never block on an unread peer.
func drain(conn *websocket.Conn) {
	go func() {
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	}()
}

func awaitAttachments(t *testing.T, rg *sessionRig, roomID string, want int) []telemetry.Record {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		records, err := rg.observed.Query(context.Background(), telemetry.Query{
			EventName: telemetry.EventConnectionAttached, RoomID: roomID,
		})
		if err != nil {
			t.Fatalf("query attachments: %v", err)
		}
		if len(records) >= want {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("observed %d attachments, want %d", len(records), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func awaitEvent(t *testing.T, rg *sessionRig, name, roomID string) []telemetry.Record {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		records, err := rg.observed.Query(context.Background(), telemetry.Query{
			EventName: name, RoomID: roomID,
		})
		if err != nil {
			t.Fatalf("query %s: %v", name, err)
		}
		if len(records) > 0 {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s observation within the deadline", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
