package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
)

const testEventEpoch = "epoch-test"
const testEventStream = "inbox"

type testEventNotifier struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (n *testEventNotifier) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.subs == nil {
		n.subs = make(map[chan struct{}]struct{})
	}
	n.subs[ch] = struct{}{}
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		delete(n.subs, ch)
		n.mu.Unlock()
	}
}

func (n *testEventNotifier) publish() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func appendTestEvent(t *testing.T, log streamhub.Log, notify *testEventNotifier, number int) {
	t.Helper()
	_, err := log.Append(t.Context(), testEventStream, streamhub.Event{
		Name: "item.enqueued", Data: fmt.Appendf(nil, `{"id":"evt_%d","kind":"item.enqueued"}`, number),
	})
	if err != nil {
		t.Fatal(err)
	}
	if notify != nil {
		notify.publish()
	}
}

type testSSEFrame struct{ id, kind, data string }

func readTestSSEFrame(t *testing.T, reader *bufio.Reader) testSSEFrame {
	t.Helper()
	var frame testSSEFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if frame.kind != "" {
				return frame
			}
			frame = testSSEFrame{}
			continue
		}
		field, value, _ := strings.Cut(line, ": ")
		switch field {
		case "id":
			frame.id = value
		case "event":
			frame.kind = value
		case "data":
			frame.data = value
		}
	}
}

func openTestEventStream(t *testing.T, source *HITLEventStream, header, query string) *bufio.Reader {
	t.Helper()
	reader, _, _ := openTestEventConnection(t, source, header, query)
	return reader
}

func openTestEventConnection(t *testing.T, source *HITLEventStream, header, query string) (*bufio.Reader, func(), string) {
	t.Helper()
	handler := newHITLHTTPHandler(&fakeHITLService{inbox: hitl.OperatorInbox{Revision: "revision-1"}})
	handler.eventStream = source
	handler.eventPoll = time.Hour
	handler.eventLifetime = 10 * time.Second
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		handler.events(w, r)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/hitl/events"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		request.Header.Set("Last-Event-ID", header)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	closeConnection := func() {
		_ = response.Body.Close()
		cancel()
		<-done
	}
	t.Cleanup(closeConnection)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("headers = %v", response.Header)
	}
	if response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("proxy buffering header was not sent: %v", response.Header)
	}
	reader := bufio.NewReader(response.Body)
	checkpoint := ""
	if source != nil && source.Log != nil {
		checkpoint = readTestCheckpoint(t, reader)
	}
	legacy := readTestSSEFrame(t, reader)
	if legacy.id != "" || legacy.kind != "revision" || legacy.data != `{"revision":"revision-1"}` {
		t.Fatalf("legacy revision ping changed: %+v", legacy)
	}
	return reader, closeConnection, checkpoint
}

func readTestCheckpoint(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "id: ") {
		t.Fatalf("initial cursor = %q, %v", line, err)
	}
	if blank, err := reader.ReadString('\n'); err != nil || blank != "\n" {
		t.Fatalf("checkpoint must be id-only: %q, %v", blank, err)
	}
	return strings.TrimSuffix(strings.TrimPrefix(line, "id: "), "\n")
}

func TestHITLTypedEventFramePreservesEnvelopeID(t *testing.T) {
	var output bytes.Buffer
	record := streamhub.Record{Seq: 17, Name: "item.resolved", Data: []byte("{\n  \"id\": \"evt_distinct\",\n  \"kind\": \"item.resolved\"\n}")}
	if err := writeHITLTypedEvent(&output, testEventEpoch, record); err != nil {
		t.Fatal(err)
	}
	want := "id: epoch-test:17\nevent: item.resolved\ndata: {\"id\":\"evt_distinct\",\"kind\":\"item.resolved\"}\n\n"
	if output.String() != want {
		t.Fatalf("frame = %q, want %q", output.String(), want)
	}
}

func TestHITLTypedEventsResumeInOrder(t *testing.T) {
	for _, tc := range []struct{ name, header, query string }{
		{"header", "epoch-test:1", ""},
		{"query", "", "?after=epoch-test:1"},
		{"header wins", "epoch-test:1", "?after=invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := streamhub.NewMemoryLog()
			defer log.Close()
			notify := &testEventNotifier{}
			for i := 1; i <= 3; i++ {
				appendTestEvent(t, log, notify, i)
			}
			reader := openTestEventStream(t, &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}, tc.header, tc.query)
			for i := 2; i <= 3; i++ {
				frame := readTestSSEFrame(t, reader)
				if frame.id != fmt.Sprintf("epoch-test:%d", i) || frame.kind != "item.enqueued" || frame.data != fmt.Sprintf(`{"id":"evt_%d","kind":"item.enqueued"}`, i) {
					t.Fatalf("replay frame = %+v", frame)
				}
			}
			appendTestEvent(t, log, notify, 4)
			if frame := readTestSSEFrame(t, reader); frame.id != "epoch-test:4" {
				t.Fatalf("live frame = %+v", frame)
			}
		})
	}
}

func TestHITLTypedEventsWithoutCursorStartAtHead(t *testing.T) {
	log := streamhub.NewMemoryLog()
	defer log.Close()
	notify := &testEventNotifier{}
	appendTestEvent(t, log, notify, 1)
	reader := openTestEventStream(t, &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}, "", "")
	appendTestEvent(t, log, notify, 2)
	if frame := readTestSSEFrame(t, reader); frame.id != "epoch-test:2" {
		t.Fatalf("fresh stream replayed history: %+v", frame)
	}
}

func TestHITLTypedEventsIdleReconnectReplaysInterveningAppend(t *testing.T) {
	log := streamhub.NewMemoryLog()
	defer log.Close()
	notify := &testEventNotifier{}
	source := &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}
	_, disconnect, cursor := openTestEventConnection(t, source, "", "")
	if cursor != "epoch-test:0" {
		t.Fatalf("initial checkpoint = %q", cursor)
	}
	disconnect()
	// A second idle connection must keep the cursor too: an initial revision
	// without a preceding checkpoint would clear EventSource's remembered id.
	_, disconnect, cursor = openTestEventConnection(t, source, cursor, "")
	if cursor != "epoch-test:0" {
		t.Fatalf("idle reconnect checkpoint = %q", cursor)
	}
	disconnect()
	notify.mu.Lock()
	subscribers := len(notify.subs)
	notify.mu.Unlock()
	if subscribers != 0 {
		t.Fatalf("disconnect leaked %d subscriptions", subscribers)
	}
	appendTestEvent(t, log, notify, 1)
	// EventSource uses the id-only checkpoint even though no typed event was
	// received on the first connection.
	reader := openTestEventStream(t, source, cursor, "")
	appendTestEvent(t, log, notify, 2)
	for i := 1; i <= 2; i++ {
		if frame := readTestSSEFrame(t, reader); frame.id != fmt.Sprintf("epoch-test:%d", i) {
			t.Fatalf("idle reconnect skipped an event: %+v", frame)
		}
	}
}

func TestHITLTypedEventsRejectInvalidConfigurationAtStartup(t *testing.T) {
	envelopes, err := envelope.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	log := streamhub.NewMemoryLog()
	defer log.Close()
	for _, source := range []*HITLEventStream{
		{Log: log, Stream: testEventStream},
		{Log: log, Epoch: testEventEpoch},
		{Log: log, Epoch: "bad:epoch", Stream: testEventStream},
		{Log: log, Epoch: "bad\nepoch", Stream: testEventStream},
		{Log: log, Epoch: "bad\x00epoch", Stream: testEventStream},
	} {
		if _, err := New(Config{Envelope: envelopes, HITLEvents: source}); err == nil {
			t.Fatalf("invalid typed config accepted: %+v", source)
		}
	}
	if _, err := New(Config{Envelope: envelopes, HITLEvents: &HITLEventStream{}}); err != nil {
		t.Fatalf("nil log must leave typed stream disabled: %v", err)
	}
}

type failingHeadLog struct{ streamhub.Log }

func (l failingHeadLog) Head(context.Context, string) (streamhub.Head, error) {
	return streamhub.Head{}, errors.New("head unavailable")
}

type cancelAfterRefreshService struct {
	*fakeHITLService
	cancel context.CancelFunc
}

func (s cancelAfterRefreshService) Inbox(ctx context.Context) (hitl.OperatorInbox, error) {
	inbox, err := s.fakeHITLService.Inbox(ctx)
	if inbox.Revision == "revision-2" {
		s.cancel()
	}
	return inbox, err
}

func TestHITLTypedEventsHeadFailurePreservesRevisionHints(t *testing.T) {
	log := streamhub.NewMemoryLog()
	defer log.Close()
	notify := &testEventNotifier{}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	service := cancelAfterRefreshService{&fakeHITLService{inbox: hitl.OperatorInbox{Revision: "revision-1"}}, cancel}
	handler := newHITLHTTPHandler(service)
	var logs bytes.Buffer
	handler.logger = slog.New(slog.NewTextHandler(&logs, nil))
	handler.eventStream = &HITLEventStream{Log: failingHeadLog{log}, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}
	handler.eventPoll = time.Millisecond
	handler.eventLifetime = 10 * time.Second
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/hitl/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "epoch-test:7")
	handler.events(response, request)
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "head unavailable") {
		t.Fatalf("head failure was not logged: %s", logs.String())
	}
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("head failure disabled legacy SSE: %d %v", response.Code, response.Header())
	}
	reader := bufio.NewReader(response.Body)
	if cursor := readTestCheckpoint(t, reader); cursor != "epoch-test:7" {
		t.Fatalf("fallback lost reconnect cursor: %q", cursor)
	}
	for _, revision := range []string{"revision-1", "revision-2"} {
		if frame := readTestSSEFrame(t, reader); frame.kind != "revision" || frame.data != fmt.Sprintf(`{"revision":%q}`, revision) {
			t.Fatalf("head failure lost revision hint: %+v", frame)
		}
	}
	notify.mu.Lock()
	defer notify.mu.Unlock()
	if len(notify.subs) != 0 {
		t.Fatal("head failure leaked subscription")
	}
}

func TestHITLTypedEventsPollWithoutRevisionChangeOrNotifier(t *testing.T) {
	log := streamhub.NewMemoryLog()
	defer log.Close()
	handler := newHITLHTTPHandler(&fakeHITLService{inbox: hitl.OperatorInbox{Revision: "revision-2"}})
	handler.eventStream = &HITLEventStream{Log: log, Epoch: testEventEpoch, Stream: testEventStream}
	handler.eventPoll = time.Millisecond
	handler.eventLifetime = 10 * time.Second
	server := httptest.NewServer(http.HandlerFunc(handler.events))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/hitl/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if frame := readTestSSEFrame(t, reader); frame.kind != "revision" {
		t.Fatalf("initial frame = %+v", frame)
	}
	appendTestEvent(t, log, nil, 1)
	if frame := readTestSSEFrame(t, reader); frame.id != "epoch-test:1" {
		t.Fatalf("poll missed event with unchanged revision: %+v", frame)
	}
}

func TestHITLTypedEventsGapAndContinue(t *testing.T) {
	for _, tc := range []struct{ name, cursor, reason string }{
		{"pruned", "epoch-test:0", "pruned"},
		{"foreign epoch", "old-epoch:2", "foreign_epoch"},
		{"ahead", "epoch-test:99", "cursor_ahead"},
		{"invalid header", "not-a-cursor", "invalid_cursor"},
		{"overflow header", "epoch-test:18446744073709551616", "invalid_cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := streamhub.NewMemoryLog(streamhub.WithMemoryRetention(streamhub.Retention{MaxRecords: 2}))
			defer log.Close()
			notify := &testEventNotifier{}
			for i := 1; i <= 3; i++ {
				appendTestEvent(t, log, notify, i)
			}
			reader := openTestEventStream(t, &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}, tc.cursor, "?after=ignored")
			frame := readTestSSEFrame(t, reader)
			var gap hitlEventGap
			if err := json.Unmarshal([]byte(frame.data), &gap); err != nil {
				t.Fatal(err)
			}
			if frame.kind != "gap" || frame.id != "epoch-test:3" || gap.Reason != tc.reason || gap.EarliestAvailable != "epoch-test:2" || gap.HighWater != "epoch-test:3" || gap.Epoch != testEventEpoch {
				t.Fatalf("gap = %+v, %+v", frame, gap)
			}
			appendTestEvent(t, log, notify, 4)
			if next := readTestSSEFrame(t, reader); next.id != "epoch-test:4" {
				t.Fatalf("gap did not resume at head: %+v", next)
			}
		})
	}
}

func TestHITLTypedEventsRejectMalformedQueryBeforeStreaming(t *testing.T) {
	for _, query := range []string{"", "invalid", "epoch-test:-1", "epoch-test:1:2", "epoch-test:18446744073709551616"} {
		t.Run(query, func(t *testing.T) {
			h := newHITLHTTPHandler(&fakeHITLService{})
			log := streamhub.NewMemoryLog()
			defer log.Close()
			h.eventStream = &HITLEventStream{Log: log, Epoch: testEventEpoch, Stream: testEventStream}
			response := httptest.NewRecorder()
			h.events(response, httptest.NewRequest(http.MethodGet, "/api/hitl/events?after="+query, nil))
			if response.Code != http.StatusBadRequest || strings.Contains(response.Header().Get("Content-Type"), "event-stream") {
				t.Fatalf("malformed cursor started SSE: %d %v", response.Code, response.Header())
			}
		})
	}
}

// seamLog gates only the first replay so a concurrent publisher appends once
// before the iterator reads and once after it finishes, coalescing the wakeups.
type seamLog struct {
	streamhub.Log
	once                        sync.Once
	before, drained             chan struct{}
	firstWritten, secondWritten <-chan struct{}
}

func (l *seamLog) After(ctx context.Context, stream string, after streamhub.Seq) iter.Seq2[streamhub.Record, error] {
	return func(yield func(streamhub.Record, error) bool) {
		first := false
		l.once.Do(func() { first = true })
		if first {
			close(l.before)
			select {
			case <-l.firstWritten:
			case <-ctx.Done():
				return
			}
		}
		for record, err := range l.Log.After(ctx, stream, after) {
			if !yield(record, err) {
				return
			}
		}
		if first {
			close(l.drained)
			select {
			case <-l.secondWritten:
			case <-ctx.Done():
			}
		}
	}
}

func TestHITLTypedEventsReplayLiveSeamHasNoDuplicateOrGap(t *testing.T) {
	memory := streamhub.NewMemoryLog(streamhub.WithPageSize(1))
	defer memory.Close()
	notify := &testEventNotifier{}
	appendTestEvent(t, memory, nil, 1)
	first, second := make(chan struct{}), make(chan struct{})
	log := &seamLog{Log: memory, before: make(chan struct{}), drained: make(chan struct{}), firstWritten: first, secondWritten: second}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	writerDone := make(chan error, 1)
	go func() {
		for i, gate := range []<-chan struct{}{log.before, log.drained} {
			select {
			case <-gate:
			case <-ctx.Done():
				writerDone <- ctx.Err()
				return
			}
			notify.mu.Lock()
			subscribed := len(notify.subs) > 0
			notify.mu.Unlock()
			if !subscribed {
				writerDone <- errors.New("replay started before live subscription")
				return
			}
			_, err := memory.Append(ctx, testEventStream, streamhub.Event{Name: "item.enqueued", Data: fmt.Appendf(nil, `{"id":"evt_%d"}`, i+2)})
			if err != nil {
				writerDone <- err
				return
			}
			notify.publish()
			if i == 0 {
				close(first)
			} else {
				close(second)
			}
		}
		writerDone <- nil
	}()
	reader := openTestEventStream(t, &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}, "epoch-test:1", "")
	for i := 2; i <= 3; i++ {
		if frame := readTestSSEFrame(t, reader); frame.id != fmt.Sprintf("epoch-test:%d", i) {
			t.Fatalf("seam frame = %+v", frame)
		}
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	appendTestEvent(t, memory, notify, 4)
	if frame := readTestSSEFrame(t, reader); frame.id != "epoch-test:4" {
		t.Fatalf("live frame duplicated or skipped: %+v", frame)
	}
}

func TestHITLTypedEventsKeepOperatorGate(t *testing.T) {
	envelopes, err := envelope.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	log := streamhub.NewMemoryLog()
	defer log.Close()
	notify := &testEventNotifier{}
	server, err := New(Config{Envelope: envelopes, HITL: &fakeHITLService{}, HITLEvents: &HITLEventStream{Log: log, Notify: notify, Epoch: testEventEpoch, Stream: testEventStream}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/events", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin SSE = %d", response.Code)
	}
	notify.mu.Lock()
	defer notify.mu.Unlock()
	if len(notify.subs) != 0 {
		t.Fatal("unauthorized request subscribed to events")
	}
}
