package room

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
)

// stubDisposition stands in for the canonical authority so the connection
// model can be exercised without the interaction substrate.
type stubDisposition struct {
	revision DurableRevision
	resolved chan *envelopes.Response
	presents chan string
}

func newStubDisposition() *stubDisposition {
	return &stubDisposition{
		revision: DurableRevision{
			SurfaceID: "surface-1", SurfaceRevision: 9,
			InteractionID: "interaction-1", InteractionRevision: 4,
			PresentedProjectionRevision: 1, State: "presented",
		},
		resolved: make(chan *envelopes.Response, 4),
		presents: make(chan string, 16),
	}
}

func (d *stubDisposition) Presented(
	_ context.Context, _ string, _ *envelopes.Envelope, _ int64, connectionID string,
) error {
	select {
	case d.presents <- connectionID:
	default:
	}
	return nil
}

func (d *stubDisposition) Resolve(
	_ context.Context, _ string, _ *envelopes.Envelope, resp *envelopes.Response,
) error {
	d.resolved <- resp
	return nil
}

func (d *stubDisposition) Cancel(_ context.Context, _ string, _ *envelopes.Envelope) error {
	return nil
}

func (d *stubDisposition) DurableRevision(context.Context) (DurableRevision, error) {
	return d.revision, nil
}

func presentedEnvelope(id string) *envelopes.Envelope {
	return &envelopes.Envelope{V: 1, ID: id, Type: "triage"}
}

func submittedResponse(id, marker string) *envelopes.Response {
	return &envelopes.Response{
		V: 1, EnvelopeID: id,
		Kind: envelopes.ResponseKindData, Status: envelopes.ResponseStatusSubmitted,
		Payload: map[string]any{"client": marker},
	}
}

// readEnvelopeRevision reads until an envelope frame for envelopeID arrives
// and returns its revision.
func readEnvelopeRevision(t *testing.T, pair wsPair, envelopeID string) int64 {
	t.Helper()
	for {
		payload := readFrameOfType(t, pair.client, "envelope")
		var frame struct {
			EnvelopeID string `json:"envelopeId"`
			Revision   int64  `json:"revision"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatalf("decode envelope frame: %v", err)
		}
		if frame.EnvelopeID == envelopeID {
			return frame.Revision
		}
	}
}

// TestTwoConnectionsObserveOneSurface is acceptance criterion 2: a second tab
// attaching to a surface neither closes the room, replaces the first tab, nor
// disturbs the presented envelope. Both clients render it; only one may
// answer.
func TestTwoConnectionsObserveOneSurface(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-a", Label: "tab a"})
	if err := rm.Present(presentedEnvelope("observe-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	firstRevision := readEnvelopeRevision(t, first, "observe-1")

	second := dial()
	secondConn := attach(t, rm, second, AttachOptions{ClientID: "tab-b", Label: "tab b"})
	secondRevision := readEnvelopeRevision(t, second, "observe-1")

	if rm.IsClosed() {
		t.Fatal("a second connection closed the room")
	}
	if got := rm.ConnectionCount(); got != 2 {
		t.Fatalf("connection count = %d, want 2", got)
	}
	if !rm.IsAttached(firstConn) {
		t.Fatal("first connection was evicted by an unrelated tab")
	}
	if role := rm.RoleOf(firstConn); role != RoleResolver {
		t.Fatalf("first connection role = %q, want resolver", role)
	}
	if role := rm.RoleOf(secondConn); role != RoleObserver {
		t.Fatalf("second connection role = %q, want observer", role)
	}
	if secondRevision == firstRevision {
		t.Fatalf("both connections were given revision %d; revisions must be distinct", firstRevision)
	}

	state := rm.ConnectionState()
	if len(state.Connections) != 2 || state.Lease == nil ||
		state.Lease.ConnectionID != firstConn.ID() {
		t.Fatalf("connection state = %+v", state)
	}
}

// TestResolverConflictReturnsLeaseError is acceptance criterion 3: the losing
// tab gets an explicit lease error naming the holder, and nothing is
// terminalized.
func TestResolverConflictReturnsLeaseError(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-a", Label: "tab a"})
	if err := rm.Present(presentedEnvelope("lease-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	readEnvelopeRevision(t, first, "lease-1")

	second := dial()
	secondConn := attach(t, rm, second, AttachOptions{ClientID: "tab-b", Label: "tab b"})
	secondRevision := readEnvelopeRevision(t, second, "lease-1")

	err := rm.HandleResponseFrom(secondConn, "lease-1", secondRevision, submittedResponse("lease-1", "tab-b"))
	if !errors.Is(err, ErrResolverLeaseHeld) {
		t.Fatalf("observer submission = %v, want ErrResolverLeaseHeld", err)
	}
	conflict := &LeaseConflictError{}
	if !errors.As(err, &conflict) {
		t.Fatalf("observer submission error is not a LeaseConflictError: %v", err)
	}
	if conflict.Holder.ConnectionID != firstConn.ID() || conflict.Holder.Label != "tab a" {
		t.Fatalf("conflict names holder %+v, want the first connection", conflict.Holder)
	}
	if !rm.IsPresenting("lease-1") {
		t.Fatal("a refused submission terminalized the presentation")
	}
	select {
	case resp := <-disposition.resolved:
		t.Fatalf("a refused submission reached the canonical authority: %+v", resp)
	default:
	}

	// An explicit takeover is the documented way out, and it moves the lease
	// without touching the interaction.
	if _, err := rm.ClaimResolver(secondConn, false); !errors.Is(err, ErrResolverLeaseHeld) {
		t.Fatalf("claim without takeover = %v, want ErrResolverLeaseHeld", err)
	}
	if _, err := rm.ClaimResolver(secondConn, true); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if role := rm.RoleOf(firstConn); role != RoleObserver {
		t.Fatalf("evicted holder role = %q, want observer", role)
	}
	if err := rm.HandleResponseFrom(secondConn, "lease-1", secondRevision, submittedResponse("lease-1", "tab-b")); err != nil {
		t.Fatalf("submission after takeover: %v", err)
	}
	select {
	case resp := <-disposition.resolved:
		payload, _ := resp.Payload.(map[string]any)
		if payload["client"] != "tab-b" {
			t.Fatalf("resolved payload = %v, want tab-b", resp.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("takeover submission never reached the canonical authority")
	}
}

// TestExpiredLeaseIsClaimableWithoutTakeover covers the frozen-tab case: a
// holder that stops proving liveness loses the lease to the next claimant with
// no takeover and no operator gesture.
func TestExpiredLeaseIsClaimableWithoutTakeover(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-a"})
	second := dial()
	secondConn := attach(t, rm, second, AttachOptions{ClientID: "tab-b"})

	if _, err := rm.ClaimResolver(secondConn, false); !errors.Is(err, ErrResolverLeaseHeld) {
		t.Fatalf("claim against a live holder = %v, want ErrResolverLeaseHeld", err)
	}

	// Age the lease past its TTL without touching the socket, exactly as a
	// suspended tab would.
	rm.connMu.Lock()
	rm.lease.expiresAt = time.Now().UTC().Add(-time.Second)
	rm.connMu.Unlock()

	if _, err := rm.ClaimResolver(secondConn, false); err != nil {
		t.Fatalf("claim against an expired lease: %v", err)
	}
	if role := rm.RoleOf(secondConn); role != RoleResolver {
		t.Fatalf("claimant role = %q, want resolver", role)
	}
	if role := rm.RoleOf(firstConn); role != RoleObserver {
		t.Fatalf("expired holder role = %q, want observer", role)
	}
}

// TestDetachReleasesResolverLease shows the lease is a connection fact: when
// the holder's socket goes, the right is free again and the interaction is
// untouched.
func TestDetachReleasesResolverLease(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-a"})
	if err := rm.Present(presentedEnvelope("detach-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	readEnvelopeRevision(t, first, "detach-1")

	second := dial()
	secondConn := attach(t, rm, second, AttachOptions{ClientID: "tab-b"})
	secondRevision := readEnvelopeRevision(t, second, "detach-1")

	rm.DetachConn(firstConn)
	if rm.IsClosed() || !rm.IsPresenting("detach-1") {
		t.Fatalf("detach changed lifecycle state: closed=%v presenting=%v",
			rm.IsClosed(), rm.IsPresenting("detach-1"))
	}
	if err := rm.HandleResponseFrom(secondConn, "detach-1", secondRevision, submittedResponse("detach-1", "tab-b")); err != nil {
		t.Fatalf("submission after holder detached: %v", err)
	}
}

// TestRefreshReplacesOnlyItsOwnClient is acceptance criterion 4: a tab
// reconnecting under its own client id replaces its predecessor and inherits
// the lease, while an unrelated tab is left attached and is presented the
// envelope exactly once.
func TestRefreshReplacesOnlyItsOwnClient(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	first := dial()
	firstConn := attach(t, rm, first, AttachOptions{ClientID: "tab-a"})
	observer := dial()
	observerConn := attach(t, rm, observer, AttachOptions{ClientID: "tab-b"})
	if err := rm.Present(presentedEnvelope("refresh-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	readEnvelopeRevision(t, first, "refresh-1")
	observerFirst := readEnvelopeRevision(t, observer, "refresh-1")

	refreshed := dial()
	refreshedConn := attach(t, rm, refreshed, AttachOptions{ClientID: "tab-a"})
	if err := rm.ReplayPending(context.Background(), refreshedConn); err != nil {
		t.Fatalf("replay refreshed: %v", err)
	}
	readEnvelopeRevision(t, refreshed, "refresh-1")

	if rm.IsAttached(firstConn) {
		t.Fatal("refresh left its own predecessor attached")
	}
	if !rm.IsAttached(observerConn) {
		t.Fatal("refresh evicted an unrelated tab")
	}
	if got := rm.ConnectionCount(); got != 2 {
		t.Fatalf("connection count after refresh = %d, want 2", got)
	}
	if role := rm.RoleOf(refreshedConn); role != RoleResolver {
		t.Fatalf("refreshed connection role = %q, want resolver — a refresh must not demote the operator", role)
	}

	// The unrelated tab is never re-presented: it already holds the envelope,
	// and duplicating it would double the presentation in its UI.
	if got := observerConn.presentedRevision("refresh-1"); got != observerFirst {
		t.Fatalf("observer presentation revision moved from %d to %d during an unrelated refresh",
			observerFirst, got)
	}
}

// TestSyncReportsDurableRevisions is acceptance criterion "synchronize clients
// from durable revisions": the snapshot a connection receives is read from the
// canonical authority, not from the room's in-memory state.
func TestSyncReportsDurableRevisions(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)
	disposition := newStubDisposition()

	pair := dial()
	connection := attach(t, rm, pair, AttachOptions{ClientID: "tab-a"})
	if err := rm.Present(presentedEnvelope("sync-1"), nil, disposition); err != nil {
		t.Fatalf("present: %v", err)
	}
	readEnvelopeRevision(t, pair, "sync-1")

	sync, err := rm.SendSync(context.Background(), connection)
	if err != nil {
		t.Fatalf("send sync: %v", err)
	}
	if sync.SurfaceRevision != 9 || len(sync.Presentations) != 1 {
		t.Fatalf("sync = %+v, want surface revision 9 with one presentation", sync)
	}
	entry := sync.Presentations[0]
	if !entry.Durable || entry.InteractionID != "interaction-1" || entry.InteractionRevision != 4 {
		t.Fatalf("sync presentation = %+v, want the canonical interaction revisions", entry)
	}
	if got := connection.Synced().SurfaceRevision; got != 9 {
		t.Fatalf("connection recorded surface revision %d, want 9", got)
	}

	// The client is told the same thing on the wire.
	payload := readFrameOfType(t, pair.client, "sync")
	var frame struct {
		Sync struct {
			SurfaceRevision int64 `json:"surface_revision"`
		} `json:"sync"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode sync frame: %v", err)
	}
	if frame.Sync.SurfaceRevision != 9 {
		t.Fatalf("sync frame surface revision = %d, want 9", frame.Sync.SurfaceRevision)
	}
}

// TestConnectionStateIsIndependentOfInteractionState is acceptance criterion
// 5 at the model level: connection state exists and changes with nothing
// presented, and survives unchanged across a presentation.
func TestConnectionStateIsIndependentOfInteractionState(t *testing.T) {
	rm := NewManager(nil).Create(nil)
	t.Cleanup(func() { rm.Close("test done") })
	dial := newWSDialer(t)

	if state := rm.ConnectionState(); len(state.Connections) != 0 || state.Lease != nil {
		t.Fatalf("empty surface connection state = %+v", state)
	}

	pair := dial()
	connection := attach(t, rm, pair, AttachOptions{ClientID: "tab-a", Label: "tab a"})
	state := rm.ConnectionState()
	if len(state.Connections) != 1 || state.Connections[0].Role != RoleResolver {
		t.Fatalf("attached-with-no-work state = %+v", state)
	}
	if rm.HasPending() {
		t.Fatal("attaching a connection created interaction state")
	}

	// The wire says the same thing, before any envelope exists.
	payload := readFrameOfType(t, pair.client, "connection")
	var frame struct {
		ConnectionID string `json:"connectionId"`
		Role         string `json:"role"`
		Connections  []struct {
			Self bool `json:"self"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode connection frame: %v", err)
	}
	if frame.ConnectionID != connection.ID() || frame.Role != string(RoleResolver) {
		t.Fatalf("connection frame = %+v", frame)
	}
	if len(frame.Connections) != 1 || !frame.Connections[0].Self {
		t.Fatalf("connection frame did not mark the recipient: %+v", frame)
	}
}
