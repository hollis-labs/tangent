package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/channel"
	"github.com/hollis-labs/tangent/internal/channelpane"
	"github.com/hollis-labs/tangent/internal/envelope"
)

type fakeChannelService struct {
	mu          sync.Mutex
	channels    []channelpane.ChannelSummary
	detail      channelpane.ChannelDetail
	sent        channelpane.SendInput
	sendErr     error
	markReadN   int
	revision    string
	revisionErr error
	getErr      error
}

func (f *fakeChannelService) ListChannels(context.Context) ([]channelpane.ChannelSummary, error) {
	return f.channels, nil
}

func (f *fakeChannelService) GetChannel(context.Context, string) (channelpane.ChannelDetail, error) {
	if f.getErr != nil {
		return channelpane.ChannelDetail{}, f.getErr
	}
	return f.detail, nil
}

func (f *fakeChannelService) SendMessage(_ context.Context, _ string, input channelpane.SendInput) (channelpane.MessageView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = input
	if f.sendErr != nil {
		return channelpane.MessageView{}, f.sendErr
	}
	return channelpane.MessageView{ExchangeID: "exchange-1", Direction: "operator", Body: input.Body, DeliveryState: "awaiting-peer"}, nil
}

func (f *fakeChannelService) MarkRead(context.Context, string) (int, error) {
	return f.markReadN, nil
}

func (f *fakeChannelService) Revision(context.Context) (string, error) {
	if f.revisionErr != nil {
		return "", f.revisionErr
	}
	return f.revision, nil
}

func newChannelTestServer(t *testing.T, fake *fakeChannelService) *Server {
	t.Helper()
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	srv, err := New(Config{Envelope: envelopeService, Channels: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func TestChannelBrowserRoutesExposeListDetailSendAndMarkRead(t *testing.T) {
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	fake := &fakeChannelService{
		channels: []channelpane.ChannelSummary{
			{ChannelID: "chan-1", Title: "test channel", UnreadCount: 2, NeedsInputCount: 1, LastMessageAt: &at, LastMessagePreview: "hi"},
		},
		detail: channelpane.ChannelDetail{
			ChannelID: "chan-1", Title: "test channel",
			Messages: []channelpane.MessageView{
				{ExchangeID: "ex-1", Direction: "agent", Body: "from the agent", CreatedAt: at},
				{ExchangeID: "ex-2", Direction: "operator", Body: "from the operator", CreatedAt: at, DeliveryState: "queued"},
			},
			HITLItems: []channelpane.HITLItem{{ItemID: "item-1", ItemURL: "/hitl/items/item-1", State: "presented", EnqueuedAt: at}},
			Agent:     &channelpane.AgentPresence{Open: true},
		},
	}
	srv := newChannelTestServer(t, fake)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/channels", nil)
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"unread_count":2`) ||
		!strings.Contains(response.Body.String(), `"needs_input_count":1`) {
		t.Fatalf("GET /api/channels = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/channels/chan-1", nil)
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"delivery_state":"queued"`) ||
		!strings.Contains(body, `/hitl/items/item-1`) {
		t.Fatalf("GET /api/channels/chan-1 = %d %s", response.Code, body)
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/channels/chan-1/messages", strings.NewReader(
		`{"body":"reply text"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.sent.Body != "reply text" {
		t.Fatalf("POST messages = %d %#v %s", response.Code, fake.sent, response.Body.String())
	}

	fake.markReadN = 3
	request = httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/channels/chan-1/read", nil)
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"marked_read":3`) {
		t.Fatalf("POST read = %d %s", response.Code, response.Body.String())
	}
}

func TestChannelSendRefusesAnEmptyBody(t *testing.T) {
	fake := &fakeChannelService{sendErr: fmt.Errorf("%w: body is required", channel.ErrInvalidRecord)}
	srv := newChannelTestServer(t, fake)

	request := httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/channels/chan-1/messages", strings.NewReader(`{"body":""}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("POST messages with empty body = %d %s", response.Code, response.Body.String())
	}
}

func TestChannelSendReportsAmbiguousRecipientAsConflict(t *testing.T) {
	fake := &fakeChannelService{sendErr: &channelpane.AmbiguousRecipientError{ChannelID: "chan-1", CandidateIDs: []string{"a", "b"}}}
	srv := newChannelTestServer(t, fake)

	request := httptest.NewRequest(http.MethodPost, "http://localhost:7842/api/channels/chan-1/messages", strings.NewReader(`{"body":"hi"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"ambiguous_recipient"`) {
		t.Fatalf("POST messages with ambiguous recipient = %d %s", response.Code, response.Body.String())
	}
}

func TestChannelGetReportsNotFound(t *testing.T) {
	fake := &fakeChannelService{getErr: channel.ErrNotFound}
	srv := newChannelTestServer(t, fake)

	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/channels/does-not-exist", nil)
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"not_found"`) {
		t.Fatalf("GET missing channel = %d %s", response.Code, response.Body.String())
	}
}

func TestChannelEventsSendsOnlyRevisionHints(t *testing.T) {
	fake := &fakeChannelService{revision: "rev-1"}
	handler := newChannelHTTPHandler(fake)
	handler.eventPoll = 10 * time.Millisecond
	handler.eventLifetime = 200 * time.Millisecond

	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/channels/events", nil)
	response := httptest.NewRecorder()
	handler.events(response, request)

	body := response.Body.String()
	if !strings.Contains(body, "event: revision") || !strings.Contains(body, `"revision":"rev-1"`) {
		t.Fatalf("channel events stream = %q, want a revision event", body)
	}
	if strings.Contains(body, `"unread_count"`) || strings.Contains(body, "chan-1") {
		t.Fatalf("channel events stream leaked state, not just a revision hint: %q", body)
	}
}
