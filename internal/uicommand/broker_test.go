package uicommand

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var verified = Binding{ParticipantRef: "synthetic-participant", ConversationRef: "synthetic-conversation", SessionRef: "synthetic-noncredential-session"}

func descriptor(t *testing.T) []byte {
	t.Helper()
	d := Descriptor{Version: 1, Route: "/inbox", Commands: CoreCommands(), Rows: []Row{{ID: "synthetic-item", Summary: "fixture"}}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func rig(t *testing.T) (*Broker, chan Frame) {
	t.Helper()
	b := New(func(context.Context, Access) (Binding, error) { return verified, nil }, 2*time.Second)
	frames := make(chan Frame, 10)
	if err := b.Attach("tab-one", verified, func(_ context.Context, f Frame) error { frames <- f; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish("tab-one", descriptor(t)); err != nil {
		t.Fatal(err)
	}
	if err := b.Active("tab-one", true); err != nil {
		t.Fatal(err)
	}
	if err := b.SetControl("tab-one", true); err != nil {
		t.Fatal(err)
	}
	return b, frames
}
func TestDescriptorValidation(t *testing.T) {
	raw := descriptor(t)
	if _, _, err := ValidateDescriptor(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"version":1,"route":"//evil"}`, `{"version":2,"route":"/"}`, `{"version":1,"route":"/","secret":"unknown"}`,
		`{"version":1,"route":"/","available_commands":[{"name":"navigate","scope":"ephemeral","input_schema":{}}]}`,
		`{"version":1,"route":"/","available_commands":[{"name":"custom","scope":"ephemeral","input_schema":{"type":"object","$ref":"https://invalid.invalid/schema","additionalProperties":false}}]}`,
		`{"version":1,"route":"/","available_commands":[{"name":"custom","scope":"ephemeral","input_schema":{"type":"object","additionalProperties":true}}]}`,
		strings.Repeat(" ", MaxDescriptorBytes+1),
	} {
		if _, _, err := ValidateDescriptor([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid descriptor: %.100s", bad)
		}
	}
	var d Descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Commands = append(d.Commands, d.Commands[0])
	dup, _ := json.Marshal(d)
	if _, _, err := ValidateDescriptor(dup); err == nil {
		t.Fatal("accepted duplicate command")
	}
	d.Commands = CoreCommands()
	d.Rows = make([]Row, MaxRows+1)
	large, _ := json.Marshal(d)
	if _, _, err := ValidateDescriptor(large); err == nil {
		t.Fatal("accepted too many rows")
	}
}
func TestAuthorityDefaultsRefuseReadAndControl(t *testing.T) {
	for _, auth := range []Authorizer{nil, func(context.Context, Access) (Binding, error) {
		return Binding{ParticipantRef: verified.ParticipantRef}, nil
	}, func(context.Context, Access) (Binding, error) { return verified, ErrForbidden }} {
		b := New(auth, time.Second)
		sent := false
		if err := b.Attach("tab-one", verified, func(context.Context, Frame) error { sent = true; return nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Publish("tab-one", descriptor(t)); err != nil {
			t.Fatal(err)
		}
		if err := b.Active("tab-one", true); err != nil {
			t.Fatal(err)
		}
		if err := b.SetControl("tab-one", true); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Get(context.Background()); !errors.Is(err, ErrForbidden) {
			t.Fatalf("read: %v", err)
		}
		if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, ErrForbidden) {
			t.Fatalf("command: %v", err)
		}
		if sent {
			t.Fatal("unverified caller received delivery")
		}
	}
	b := New(nil, time.Second)
	if err := b.Attach("tab", Binding{}, func(context.Context, Frame) error { return nil }); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := b.Publish("unbound", descriptor(t)); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}
func TestCommandAckStatusesAndPeerFencing(t *testing.T) {
	for _, status := range []string{"applied", "not_visible", "rejected"} {
		t.Run(status, func(t *testing.T) {
			b, frames := rig(t)
			done := make(chan error, 1)
			go func() {
				ack, err := b.Command(context.Background(), "open_modal", json.RawMessage(`{"id":"synthetic-modal"}`))
				if err == nil && ack.Status != status {
					err = errors.New("wrong ack")
				}
				done <- err
			}()
			f := <-frames
			if f.Scope != Ephemeral || f.Type != "ui.command" {
				t.Fatalf("bad frame: %+v", f)
			}
			ack := Ack{CommandID: f.CommandID, ViewRevision: f.ViewRevision, Status: status}
			if err := b.Acknowledge("other-tab", ack); !errors.Is(err, ErrStale) {
				t.Fatal(err)
			}
			wrong := ack
			wrong.ViewRevision++
			if err := b.Acknowledge("tab-one", wrong); !errors.Is(err, ErrStale) {
				t.Fatal(err)
			}
			wrong = ack
			wrong.Status = "success"
			if err := b.Acknowledge("tab-one", wrong); err == nil {
				t.Fatal("invalid status")
			}
			if err := b.Acknowledge("tab-one", ack); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := b.Acknowledge("tab-one", ack); !errors.Is(err, ErrStale) {
				t.Fatal("duplicate ack accepted")
			}
		})
	}
}
func TestTimeoutCancellationAndDisconnect(t *testing.T) {
	b, _ := rig(t)
	b.timeout = 20 * time.Millisecond
	if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Command(ctx, "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	b, frames := rig(t)
	done := make(chan Ack, 1)
	go func() {
		ack, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`))
		if err != nil {
			done <- Ack{Reason: err.Error()}
			return
		}
		done <- ack
	}()
	<-frames
	b.Detach("tab-one")
	if ack := <-done; ack.Status != "rejected" {
		t.Fatalf("detach: %+v", ack)
	}
	if len(b.pending) != 0 {
		t.Fatal("pending leaked")
	}
}
func TestMultiTabLatestActiveAndIsolation(t *testing.T) {
	b, one := rig(t)
	two := make(chan Frame, 1)
	if err := b.Attach("tab-two", verified, func(_ context.Context, f Frame) error { two <- f; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish("tab-two", descriptor(t)); err != nil {
		t.Fatal(err)
	}
	if err := b.Active("tab-two", true); err != nil {
		t.Fatal(err)
	}
	if err := b.SetControl("tab-two", true); err != nil {
		t.Fatal(err)
	}
	// A different conversation/session with the same participant must not win.
	other := verified
	other.ConversationRef = "other-conversation"
	if err := b.Attach("foreign", other, func(context.Context, Frame) error { t.Error("foreign delivery"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := b.Active("foreign", true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`))
		done <- err
	}()
	f := <-two
	select {
	case <-one:
		t.Fatal("broadcast to older tab")
	default:
	}
	if err := b.Acknowledge("tab-two", Ack{CommandID: f.CommandID, ViewRevision: f.ViewRevision, Status: "applied"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := b.SetControl("tab-two", false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, ErrDisabled) {
		t.Fatal("fell back to older enabled tab")
	}
	snap, err := b.Get(context.Background())
	if err != nil || snap.ControlEnabled {
		t.Fatalf("disabled reads: %+v %v", snap, err)
	}
	snap.Descriptor.Rows[0].Summary = "mutated"
	again, err := b.Get(context.Background())
	if err != nil || again.Descriptor.Rows[0].Summary == "mutated" {
		t.Fatal("store aliases reader")
	}
	b.Detach("tab-two")
	again, err = b.Get(context.Background())
	if err != nil || !again.ControlEnabled {
		t.Fatalf("older active fallback: %v", err)
	}
}
func TestArgumentsAndRateValidation(t *testing.T) {
	b, frames := rig(t)
	for _, args := range []string{`{}`, `{"route":3}`, `{"route":"https://evil"}`, `{"route":"//evil"}`, `{"route":"/ok","extra":true}`, strings.Repeat(" ", MaxArgumentsBytes+1)} {
		if _, err := b.Command(context.Background(), "navigate", json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	if _, err := b.Command(context.Background(), "undeclared", json.RawMessage(`{}`)); err == nil {
		t.Fatal("undeclared accepted")
	}
	if _, err := b.Publish("tab-one", descriptor(t)); !errors.Is(err, ErrRateLimited) {
		t.Fatal(err)
	}
	select {
	case <-frames:
		t.Fatal("invalid arguments delivered")
	default:
	}
}

func TestStrictJSONAndObservationProjection(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":1,"route":"/"}`,
		`{"Version":1,"route":"/"}`,
		`{"version":1,"route":"/","visible_rows":[{"id":"row"}]}`,
		`{"version":1,"route":"/","search":null}`,
		`{"version":1,"route":"/","search":"\ud800"}`,
		`{"version":1,"route":"/","selected_ids":["same","same"]}`,
		`{"version":1,"route":"/","active_filters":[{"name":"same","values":["a"]},{"name":"same","values":["b"]}]}`,
		`{"version":1,"route":"/inbox?secret=value"}`,
		`{"version":1,"route":"/#fragment"}`,
		`{"version":1,"route":"/","available_commands":[{"name":"custom","scope":"ephemeral","input_schema":{"type":"object","type":"object","additionalProperties":false}}]}`,
		"{\"version\":1,\"route\":\"/\",\"search\":\"" + string([]byte{0xff}) + "\"}",
	} {
		if _, _, err := ValidateDescriptor([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed input: %.100s", raw)
		}
	}
	if err := checkJSON([]byte(strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17))); err == nil {
		t.Fatal("depth accepted")
	}
	if err := checkJSON([]byte("[" + strings.Repeat("0,", 2048) + "0]")); err == nil {
		t.Fatal("node overflow accepted")
	}
	if err := checkJSON([]byte(`{"input_schema":{"enum":[null,9007199254740993,1e+99]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateDescriptor([]byte(`{"version":1,"route":"/","search":"\ud83d\ude00","active_filters":[{"name":"status","values":["in progress",""]}]}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := rig(t)
	projection, err := b.Observation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version int                        `json:"version"`
		View    map[string]json.RawMessage `json:"view"`
	}
	if err := json.Unmarshal(projection, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != 1 || string(envelope.View["route"]) != `"/inbox"` || envelope.View["available_commands"] == nil {
		t.Fatalf("projection: %s", projection)
	}
	for _, key := range []string{"version", "control_enabled", "revision", "session_ref", "participant_ref", "conversation_ref"} {
		if envelope.View[key] != nil {
			t.Fatalf("projected host field %s", key)
		}
	}
	b.Detach("tab-one")
	if _, err := b.Observation(context.Background()); !errors.Is(err, ErrNotVisible) {
		t.Fatal("reused old view")
	}
}

func TestViewInvalidationAndCustomCommand(t *testing.T) {
	b, frames := rig(t)
	custom := Declaration{Name: "docs.set_filter", Scope: URLBacked, Schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["open","closed"]}},"required":["status"],"additionalProperties":false}`)}
	raw := descriptor(t)
	var d Descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Commands = append(d.Commands, custom)
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// Move the test clock back without a sleep to exercise accepted publication.
	b.attachments["tab-one"].published = time.Time{}
	if _, err := b.Publish("tab-one", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Command(context.Background(), custom.Name, json.RawMessage(`{"status":"invalid"}`)); err == nil {
		t.Fatal("custom schema was not enforced")
	}
	done := make(chan Ack, 1)
	go func() {
		ack, err := b.Command(context.Background(), custom.Name, json.RawMessage(`{"status":"open"}`))
		if err != nil {
			done <- Ack{Reason: err.Error()}
			return
		}
		done <- ack
	}()
	f := <-frames
	if f.Scope != URLBacked {
		t.Fatal("custom URL scope missing")
	}
	b.mu.Lock()
	b.attachments["tab-one"].published = time.Time{}
	b.mu.Unlock()
	if _, err := b.Publish("tab-one", descriptor(t)); err != nil {
		t.Fatal(err)
	}
	if ack := <-done; ack.Status != "rejected" || ack.Reason != "view changed" {
		t.Fatalf("invalidation: %+v", ack)
	}
	if err := b.Acknowledge("tab-one", Ack{CommandID: f.CommandID, ViewRevision: f.ViewRevision, Status: "applied"}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}
func TestSenderFailureAndInitialDisabledControl(t *testing.T) {
	b, _ := rig(t)
	b.mu.Lock()
	b.attachments["tab-one"].send = func(context.Context, Frame) error { return errors.New("synthetic write error") }
	b.mu.Unlock()
	if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); err == nil {
		t.Fatal("write success was invented")
	}
	b.mu.Lock()
	remaining := len(b.pending)
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatal("write failure leaked pending")
	}
	if err := b.Attach("tab-two", verified, func(context.Context, Frame) error { t.Error("disabled attachment received command"); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish("tab-two", descriptor(t)); err != nil {
		t.Fatal(err)
	}
	if err := b.Active("tab-two", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}
func TestInlineAckAndAtomicSendInvalidation(t *testing.T) {
	b, _ := rig(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	b.attachments["tab-one"].send = func(_ context.Context, f Frame) error {
		close(entered)
		<-release
		return b.Acknowledge("tab-one", Ack{CommandID: f.CommandID, ViewRevision: f.ViewRevision, Status: "applied"})
	}
	done := make(chan error, 1)
	go func() {
		_, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`))
		done <- err
	}()
	<-entered
	disabled := make(chan error, 1)
	go func() { disabled <- b.SetControl("tab-one", false) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if _, err := b.Command(context.Background(), "navigate", json.RawMessage(`{"route":"/channels"}`)); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestSchemaNormalizationPreservesNumericLiterals(t *testing.T) {
	b, _ := rig(t)
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","maximum":9007199254740993}},"required":["n"],"additionalProperties":false}`)
	d := Descriptor{Version: 1, Route: "/inbox", Commands: []Declaration{{Name: "custom", Scope: Ephemeral, Schema: append(append(json.RawMessage(nil), schema...), []byte(strings.Repeat(" ", 2200))...)}}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal compacts RawMessage. Direct wire whitespace has only the overall
	// descriptor bound, while the schema's own bound is normalized bytes.
	raw = []byte(strings.Replace(string(raw), `"input_schema":{`, `"input_schema":{`+strings.Repeat(" ", 2200), 1))
	if _, _, validationErr := ValidateDescriptor(raw); validationErr != nil {
		t.Fatal(validationErr)
	}
	b.attachments["tab-one"].published = time.Time{}
	if _, publishErr := b.Publish("tab-one", raw); publishErr != nil {
		t.Fatal(publishErr)
	}
	projection, err := b.Observation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projection), "9007199254740993") {
		t.Fatalf("numeric literal changed: %s", projection)
	}
}
