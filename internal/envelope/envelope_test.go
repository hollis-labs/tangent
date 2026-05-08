package envelope_test

import (
	"context"
	"errors"
	"testing"

	envelopes "github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// newService is a small helper that constructs a fresh Service per test
// so tests don't share registry state. Registry load is fast enough
// (~25 schemas) that paying it per test is fine.
func newService(t *testing.T) *envelope.Service {
	t.Helper()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	return svc
}

func TestService_LoadCore(t *testing.T) {
	svc := newService(t)
	if svc.Len() == 0 {
		t.Fatal("expected core registry to load at least one type, got 0")
	}
	// Sanity: a known core type is present. info-card is the touchstone
	// envelopestest.RunContract uses, so confirming it here keeps the
	// signal close to the unit-test layer.
	if _, ok := svc.Lookup("info-card"); !ok {
		t.Error("expected core type info-card to be registered")
	}
}

func TestService_Validate_Valid(t *testing.T) {
	svc := newService(t)
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-valid-1",
		Type: "info-card",
		Data: map[string]any{"title": "Hello", "body": "World"},
	}
	if err := svc.Validate(env); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestService_Validate_UnknownType(t *testing.T) {
	svc := newService(t)
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-unknown-1",
		Type: "tangent.does-not-exist",
	}
	err := svc.Validate(env)
	if !errors.Is(err, envelopes.ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
}

func TestService_Validate_InvalidData(t *testing.T) {
	svc := newService(t)
	// info-card requires title+body strings; an empty data map fails the
	// schema's required check.
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-bad-data-1",
		Type: "info-card",
		Data: map[string]any{},
	}
	err := svc.Validate(env)
	if err == nil {
		t.Fatal("expected schema-validation error, got nil")
	}
	if !errors.Is(err, envelopes.ErrSchemaValidation) {
		t.Fatalf("expected ErrSchemaValidation, got %v", err)
	}
}

func TestDispatcher_Register_UnknownType(t *testing.T) {
	svc := newService(t)
	d := envelope.NewDispatcher(svc)
	stub := envelope.HandlerFunc(func(_ context.Context, _ *envelopes.Envelope) (*envelopes.Response, error) {
		return nil, nil
	})
	err := d.Register("tangent.not-a-real-type", stub)
	if !errors.Is(err, envelopes.ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
}

func TestDispatcher_Register_DuplicateHandler(t *testing.T) {
	svc := newService(t)
	d := envelope.NewDispatcher(svc)
	stub := envelope.HandlerFunc(func(_ context.Context, _ *envelopes.Envelope) (*envelopes.Response, error) {
		return nil, nil
	})
	if err := d.Register("info-card", stub); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := d.Register("info-card", stub)
	if !errors.Is(err, envelope.ErrHandlerExists) {
		t.Fatalf("expected ErrHandlerExists, got %v", err)
	}
}

func TestDispatcher_Dispatch_NoHandler(t *testing.T) {
	svc := newService(t)
	d := envelope.NewDispatcher(svc)
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-nohandler-1",
		Type: "info-card",
		Data: map[string]any{"title": "T", "body": "B"},
	}
	_, err := d.Dispatch(context.Background(), env)
	if !errors.Is(err, envelope.ErrNoHandler) {
		t.Fatalf("expected ErrNoHandler, got %v", err)
	}
}

func TestDispatcher_Dispatch_RoundTrip(t *testing.T) {
	svc := newService(t)
	d := envelope.NewDispatcher(svc)
	called := false
	stub := envelope.HandlerFunc(func(_ context.Context, e *envelopes.Envelope) (*envelopes.Response, error) {
		called = true
		return &envelopes.Response{
			V:          envelopes.ProtocolVersion,
			EnvelopeID: e.ID,
			Kind:       envelopes.ResponseKindAck,
			Status:     envelopes.ResponseStatusSubmitted,
		}, nil
	})
	if err := d.Register("info-card", stub); err != nil {
		t.Fatalf("Register: %v", err)
	}
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-rt-1",
		Type: "info-card",
		Data: map[string]any{"title": "T", "body": "B"},
	}
	resp, err := d.Dispatch(context.Background(), env)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !called {
		t.Error("expected handler to be invoked")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.EnvelopeID != env.ID {
		t.Errorf("response envelopeId = %q, want %q", resp.EnvelopeID, env.ID)
	}
	if resp.Kind != envelopes.ResponseKindAck {
		t.Errorf("response kind = %q, want %q", resp.Kind, envelopes.ResponseKindAck)
	}
}

func TestDispatcher_Dispatch_NilResponseFromHandler(t *testing.T) {
	svc := newService(t)
	d := envelope.NewDispatcher(svc)
	stub := envelope.HandlerFunc(func(_ context.Context, _ *envelopes.Envelope) (*envelopes.Response, error) {
		return nil, nil
	})
	if err := d.Register("info-card", stub); err != nil {
		t.Fatalf("Register: %v", err)
	}
	env := &envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "test-nilresp-1",
		Type: "info-card",
		Data: map[string]any{"title": "T", "body": "B"},
	}
	_, err := d.Dispatch(context.Background(), env)
	if !errors.Is(err, envelope.ErrNilResponse) {
		t.Fatalf("expected ErrNilResponse, got %v", err)
	}
}
