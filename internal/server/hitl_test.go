package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
)

type fakeHITLService struct {
	mu         sync.Mutex
	inboxCalls int
	inboxErrAt int
	inbox      hitl.OperatorInbox
	item       hitl.OperatorItemView
	presented  hitl.PresentInput
	resolved   hitl.ResolveInput
	resolveErr error
}

func (f *fakeHITLService) Inbox(context.Context) (hitl.OperatorInbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inboxCalls++
	if f.inboxErrAt > 0 && f.inboxCalls >= f.inboxErrAt {
		return hitl.OperatorInbox{}, errors.New("durable inbox unavailable")
	}
	result := f.inbox
	if f.inboxCalls > 1 {
		result.Revision = "revision-2"
	}
	return result, nil
}

func (f *fakeHITLService) InspectOperatorItem(context.Context, string) (hitl.OperatorItemView, error) {
	return f.item, nil
}

func (f *fakeHITLService) Present(_ context.Context, input hitl.PresentInput) (hitl.ItemHandle, error) {
	f.presented = input
	return hitl.ItemHandle{ItemID: input.ItemID, Revision: input.ExpectedRevision + 1}, nil
}

func (f *fakeHITLService) Resolve(_ context.Context, input hitl.ResolveInput) (hitl.TerminalOutcome, error) {
	f.resolved = input
	return hitl.TerminalOutcome{ItemID: input.ItemID, State: interaction.InteractionStateResolved}, f.resolveErr
}

func TestHITLBrowserRoutesExposeInboxPresentationAndResolution(t *testing.T) {
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	projectionRevision := int64(3)
	fake := &fakeHITLService{
		inbox: hitl.OperatorInbox{
			ContractVersion: hitl.ContractVersion, SurfaceID: hitl.DefaultSurfaceID,
			Revision: "revision-1", Pending: []hitl.OperatorItemView{}, History: []hitl.OperatorItemView{},
		},
		item: hitl.OperatorItemView{
			ItemView:                    hitl.ItemView{ItemID: "item-1", Revision: 4, State: interaction.InteractionStatePresented},
			PresentedProjectionRevision: &projectionRevision,
		},
	}
	srv, err := New(Config{Envelope: envelopeService, HITL: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl", nil)
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision":"revision-1"`) {
		t.Fatalf("GET inbox = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/items/item-1", nil)
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"presented_projection_revision":3`) {
		t.Fatalf("GET item = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/item-1/present", strings.NewReader(
		`{"expected_revision":3,"presented_projection_revision":3,"connection_id":"tab-a"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.presented.ItemID != "item-1" || fake.presented.ConnectionID != "tab-a" {
		t.Fatalf("POST present = %d %#v %s", response.Code, fake.presented, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/item-1/resolve", strings.NewReader(
		`{"expected_revision":4,"presented_projection_revision":3,"response":{"kind":"approval","decision":"approved"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.resolved.ItemID != "item-1" ||
		string(fake.resolved.Response) != `{"kind":"approval","decision":"approved"}` {
		t.Fatalf("POST resolve = %d %#v %s", response.Code, fake.resolved, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/attention-1/resolve", strings.NewReader(
		`{"expected_revision":7,"presented_projection_revision":6,"response":{"kind":"attention","decision":"acknowledged","note":"logged","reply":"worker recovered"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.resolved.ItemID != "attention-1" ||
		fake.resolved.ExpectedRevision != 7 || fake.resolved.PresentedProjectionRevision != 6 ||
		string(fake.resolved.Response) != `{"kind":"attention","decision":"acknowledged","note":"logged","reply":"worker recovered"}` {
		t.Fatalf("POST attention resolve = %d %#v %s", response.Code, fake.resolved, response.Body.String())
	}
}

func TestHITLBrowserRoutesRejectInvalidAndExposeStaleConflict(t *testing.T) {
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	fake := &fakeHITLService{
		resolveErr: &hitl.StaleRevisionError{
			Operation: "resolve", ItemID: "item-1", RevisionKind: "interaction",
			ExpectedRevision: 4, ActualRevision: 5, CurrentState: interaction.InteractionStateResolved,
			TerminalOutcome: &hitl.TerminalOutcome{ItemID: "item-1", State: interaction.InteractionStateResolved},
		},
	}
	srv, err := New(Config{Envelope: envelopeService, HITL: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/item-1/present", strings.NewReader(
		`{"expected_revision":3,"presented_projection_revision":3,"extra":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid present = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/item-1/resolve", strings.NewReader(
		`{"expected_revision":4,"presented_projection_revision":3,"response":{"kind":"approval","decision":"denied"}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale resolve status = %d", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode stale response: %v", err)
	}
	if body["code"] != "stale_revision" || body["actual_revision"] != float64(5) || body["terminal_outcome"] == nil {
		t.Fatalf("stale response = %#v", body)
	}
}

func TestHITLBrowserRoutesRequireJSONAndSameOrigin(t *testing.T) {
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	fake := &fakeHITLService{inbox: hitl.OperatorInbox{Revision: "revision-1"}}
	srv, err := New(Config{Envelope: envelopeService, HITL: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/hitl/items/item-1/present", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType ||
		!strings.Contains(response.Body.String(), `"code":"unsupported_media_type"`) {
		t.Fatalf("non-JSON command = %d %s", response.Code, response.Body.String())
	}
	if fake.presented.ItemID != "" {
		t.Fatalf("non-JSON command reached service: %#v", fake.presented)
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"forbidden_origin"`) {
		t.Fatalf("cross-origin read = %d %s", response.Code, response.Body.String())
	}
	if fake.inboxCalls != 0 {
		t.Fatalf("cross-origin request reached service: %d inbox calls", fake.inboxCalls)
	}

	request = httptest.NewRequest(http.MethodGet, "http://attacker.example:7842/api/hitl", nil)
	request.Header.Set("Origin", "http://attacker.example:7842")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"forbidden_origin"`) {
		t.Fatalf("non-loopback same-origin read = %d %s", response.Code, response.Body.String())
	}
	if fake.inboxCalls != 0 {
		t.Fatalf("non-loopback request reached service: %d inbox calls", fake.inboxCalls)
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl", nil)
	request.Header.Set("Origin", "http://localhost:7842")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin read = %d %s", response.Code, response.Body.String())
	}
}

func TestHITLEventsCarryRevisionHintsOnly(t *testing.T) {
	fake := &fakeHITLService{inbox: hitl.OperatorInbox{Revision: "revision-1"}}
	handler := newHITLHTTPHandler(fake)
	handler.eventPoll = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/hitl/events", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.events(response, request)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		fake.mu.Lock()
		calls := fake.inboxCalls
		fake.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("event handler did not poll for a changed revision")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event handler did not stop after disconnect")
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: revision\ndata: {\"revision\":\"revision-1\"}") ||
		!strings.Contains(body, "event: revision\ndata: {\"revision\":\"revision-2\"}") {
		t.Fatalf("event body = %q", body)
	}
	if strings.Contains(body, "pending") || strings.Contains(body, "history") {
		t.Fatalf("event stream leaked durable state instead of revision hints: %q", body)
	}
}

func TestHITLEventsDisconnectWhenDurableRefreshFails(t *testing.T) {
	fake := &fakeHITLService{
		inbox:      hitl.OperatorInbox{Revision: "revision-1"},
		inboxErrAt: 2,
	}
	handler := newHITLHTTPHandler(fake)
	handler.eventPoll = time.Millisecond
	request := httptest.NewRequest(http.MethodGet, "/api/hitl/events", nil)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.events(response, request)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event handler did not disconnect after durable refresh failure")
	}
	fake.mu.Lock()
	calls := fake.inboxCalls
	fake.mu.Unlock()
	if calls != 2 {
		t.Fatalf("event handler made %d inbox calls, want initial load plus failed refresh", calls)
	}
}

func TestWriteHITLErrorMapsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	writeHITLError(response, interaction.ErrNotFound)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
		t.Fatalf("not found = %d %s", response.Code, response.Body.String())
	}
}
