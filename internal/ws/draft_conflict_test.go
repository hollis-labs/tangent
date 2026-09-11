package ws_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	"github.com/hollis-labs/tangent/internal/room"
)

// The seam CW-20260910-0134 lives or dies on.
//
// The store knows which revision it wanted and the browser needs that number.
// Everything between them — roomflow's mapping, this handler's frame — is an
// `errors.As` and a field assignment, which is exactly the kind of plumbing
// that fails silently: a wrong type assertion yields a zero, the frame ships
// without the number, the client goes back to guessing, and nothing anywhere
// reports a failure. So this drives the real transport and reads the bytes.

// draftConflictDisposition refuses every draft the way the store does when a
// client's sequence has fallen behind: with the revision that would have been
// accepted attached.
type draftConflictDisposition struct {
	expected int64
}

func (d *draftConflictDisposition) Presented(
	context.Context, string, *envelopes.Envelope, int64, string,
) error {
	return nil
}

func (d *draftConflictDisposition) Resolve(
	context.Context, string, *envelopes.Envelope, *envelopes.Response,
) error {
	return nil
}

func (d *draftConflictDisposition) Cancel(context.Context, string, *envelopes.Envelope) error {
	return nil
}

func (d *draftConflictDisposition) DurableRevision(context.Context) (room.DurableRevision, error) {
	return room.DurableRevision{}, nil
}

func (d *draftConflictDisposition) Draft(
	_ context.Context, _ string, env *envelopes.Envelope, sent int64, _ json.RawMessage,
) error {
	id := ""
	if env != nil {
		id = env.ID
	}
	return &room.DraftConflictError{InteractionID: id, Sent: sent, Expected: d.expected}
}

// TestHandler_StaleDraftCarriesTheExpectedRevision.
func TestHandler_StaleDraftCarriesTheExpectedRevision(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	tab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "draft-wire", Type: "triage"}
	if err := rm.Present(env, nil, &draftConflictDisposition{expected: 6}); err != nil {
		t.Fatalf("present: %v", err)
	}
	frame := readFrame(t, tab, 2*time.Second)
	if frame["envelopeId"] != "draft-wire" {
		t.Fatalf("first frame = %+v, want the envelope", frame)
	}

	// A client whose count was reset — a reload is enough — starts at 1.
	writeFrame(t, tab, map[string]any{
		"type":          "draft",
		"envelopeId":    "draft-wire",
		"draftRevision": 1,
		"draft":         map[string]any{"filters": []string{"a"}},
	})

	refused := readFrameOfType(t, tab, "error", 2*time.Second)
	if refused["code"] != "stale_draft" {
		t.Fatalf("refusal frame = %+v, want code stale_draft", refused)
	}
	// The number the client sent, which it already knew, and the number it
	// needs, which it had no other way to learn.
	if got, ok := refused["revision"].(float64); !ok || int64(got) != 1 {
		t.Errorf("revision = %v, want the revision the client sent", refused["revision"])
	}
	expected, ok := refused["expectedRevision"].(float64)
	if !ok {
		t.Fatalf("refusal frame carries no expectedRevision: %+v — the client is back to guessing", refused)
	}
	if int64(expected) != 6 {
		t.Errorf("expectedRevision = %v, want 6", expected)
	}

	// A refused draft settles nothing: the board is still on screen and still
	// answerable, which is what makes this recoverable rather than fatal.
	if !rm.IsPresenting("draft-wire") {
		t.Error("a refused draft terminalized the presentation")
	}
}

// TestHandler_StaleDraftWithoutAnExpectedRevisionOmitsTheField.
//
// A conflict the store could not put a number on must not ship a zero: the
// client reads a present-but-zero field as "resynchronize to revision 0", which
// is a worse guess than the one it would have made on its own. `omitempty` is
// carrying that, so it is worth a test rather than a reading of the struct tag.
func TestHandler_StaleDraftWithoutAnExpectedRevisionOmitsTheField(t *testing.T) {
	mgr, base, cleanup := newTestRig(t)
	defer cleanup()
	rm := mgr.Create(nil)

	tab := dialTab(t, base, rm.ID, "tab-a")
	waitForConnections(t, rm, 1, 2*time.Second)

	env := &envelopes.Envelope{V: 1, ID: "draft-bare", Type: "triage"}
	if err := rm.Present(env, nil, &bareDraftConflictDisposition{}); err != nil {
		t.Fatalf("present: %v", err)
	}
	readFrame(t, tab, 2*time.Second)

	writeFrame(t, tab, map[string]any{
		"type": "draft", "envelopeId": "draft-bare", "draftRevision": 1,
		"draft": map[string]any{},
	})
	refused := readFrameOfType(t, tab, "error", 2*time.Second)
	if refused["code"] != "stale_draft" {
		t.Fatalf("refusal frame = %+v, want code stale_draft", refused)
	}
	if _, present := refused["expectedRevision"]; present {
		t.Errorf("frame = %+v, want expectedRevision absent rather than zero", refused)
	}
}

type bareDraftConflictDisposition struct{}

func (bareDraftConflictDisposition) Presented(
	context.Context, string, *envelopes.Envelope, int64, string,
) error {
	return nil
}

func (bareDraftConflictDisposition) Resolve(
	context.Context, string, *envelopes.Envelope, *envelopes.Response,
) error {
	return nil
}

func (bareDraftConflictDisposition) Cancel(context.Context, string, *envelopes.Envelope) error {
	return nil
}

func (bareDraftConflictDisposition) DurableRevision(context.Context) (room.DurableRevision, error) {
	return room.DurableRevision{}, nil
}

func (bareDraftConflictDisposition) Draft(
	context.Context, string, *envelopes.Envelope, int64, json.RawMessage,
) error {
	return room.ErrDispositionDraftConflict
}
