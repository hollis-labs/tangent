package room_test

import (
	"context"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/room"
)

// The cancellation vocabulary migration (CW-20260904-0168) moved the protocol
// to the US spelling, and the two tests here are the ones the suite could not
// construct before it. Both exist because the failure mode is silence: a
// change to a persisted VALUE type-checks perfectly, passes vet and
// staticcheck, and stops matching rows without any of them noticing. A green
// build proves nothing about a value change, so these assert against rows and
// bytes rather than against constants.

// TestHistory_ReadsLegacyCancellationRowsCanonically seeds a row exactly as
// the pre-migration code wrote one and reads it back through the real path.
//
// The row deliberately mixes vocabularies, because a real one does:
// `status` carries Tangent's own envelopeStatusCancelled, which is spelled the
// British way and is NOT the protocol's value; `error_code` and the error body
// carry the protocol's legacy "user-cancelled". A reader must canonicalize the
// second without being confused by the first.
//
// This is the case migration 0017 cannot cover on its own — a database
// restored from a backup taken before it ran, or rolled back, still holds
// these bytes.
func TestHistory_ReadsLegacyCancellationRowsCanonically(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"test": t.Name()})

	const ts = "2026-09-10T00:00:00Z"
	if _, err := db.Exec(`
INSERT INTO envelopes (
  room_id, envelope_id, type, request_payload,
  response_kind, response_payload, status, error_code, error_message,
  created_at, resolved_at
) VALUES (
  ?, 'legacy-1', 'triage', '{}',
  'error', '{"code":"user-cancelled","message":"context canceled"}',
  'cancelled', 'user-cancelled', 'context canceled', ?, ?)`,
		rm.ID, ts, ts); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	history, err := mgr.History(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	item := history[0]

	if item.ErrorCode != envelopes.ErrorCodeUserCanceled {
		t.Errorf("error_code read back as %q, want the canonical %q",
			item.ErrorCode, envelopes.ErrorCodeUserCanceled)
	}
	if item.Response == nil {
		t.Fatalf("legacy row produced no response")
	}
	if !item.Response.Status.IsCanonical() {
		t.Errorf("status read back as %q, which IsCanonical rejects", item.Response.Status)
	}
	if item.Response.Status != envelopes.ResponseStatusCanceled {
		t.Errorf("status read back as %q, want %q",
			item.Response.Status, envelopes.ResponseStatusCanceled)
	}
	if item.Response.Error == nil {
		t.Fatalf("error response carried no error body")
	}
	if item.Response.Error.Code != envelopes.ErrorCodeUserCanceled {
		t.Errorf("error body code read back as %q, want the canonical %q",
			item.Response.Error.Code, envelopes.ErrorCodeUserCanceled)
	}

	// The seeded bytes are still the legacy ones. This pins that the
	// canonicalization above happened on READ and is not an artifact of the
	// test having quietly written the new spelling.
	var persistedCode string
	if err := db.QueryRow(
		`SELECT error_code FROM envelopes WHERE room_id = ? AND envelope_id = 'legacy-1'`,
		rm.ID).Scan(&persistedCode); err != nil {
		t.Fatalf("read persisted error_code: %v", err)
	}
	if persistedCode != "user-cancelled" {
		t.Fatalf("the seeded row no longer holds the legacy value (%q); "+
			"this test is no longer testing a legacy read", persistedCode)
	}
}

// TestCancel_RoundTripsAcrossTheVocabularyBoundary drives a real cancellation
// through the live path and follows the value all the way round: what the
// caller is handed, what lands in the database, and what comes back out.
//
// The three assertions are deliberately different in kind. The emitted status
// is checked as a literal string, because that is the wire value an MCP client
// and a browser actually see and a constant would follow a future change
// instead of catching it. The persisted code is checked against the column.
// The read-back is checked for canonicality.
func TestCancel_RoundTripsAcrossTheVocabularyBoundary(t *testing.T) {
	rm, clientConn, db, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "round-trip-1", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)
	writeClientFrame(t, clientConn, map[string]any{
		"type":       "cancel",
		"envelopeId": env.ID,
	})
	if res := <-pushDone; res.err == nil {
		t.Fatalf("expected a cancellation error, got resp=%+v", res.resp)
	}

	// 1. What was persisted. The status column keeps Tangent's own
	//    vocabulary; the error code is the protocol's, and it is the US
	//    spelling now.
	assertEnvelopeStatus(t, db, rm.ID, env.ID, "cancelled", "user-canceled", true)

	// 2. What comes back out, through the same read path a browser uses.
	history, err := room.NewManager(db).History(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var found bool
	for _, item := range history {
		if item.EnvelopeID != env.ID {
			continue
		}
		found = true
		if item.ErrorCode != envelopes.ErrorCodeUserCanceled {
			t.Errorf("error_code = %q, want %q", item.ErrorCode, envelopes.ErrorCodeUserCanceled)
		}
		if item.Response == nil {
			t.Fatalf("cancelled envelope %q read back with no response", env.ID)
		}
		// Asserting the exact status, not merely IsCanonical(). A read path
		// that stops matching cancelled rows falls through to
		// ResponseStatusError, and "error" is canonical too — so IsCanonical
		// alone would let the silent-mismatch regression pass.
		if item.Response.Status != envelopes.ResponseStatusCanceled {
			t.Errorf("status = %q, want %q", item.Response.Status, envelopes.ResponseStatusCanceled)
		}
	}
	if !found {
		t.Fatalf("cancelled envelope %q is missing from history", env.ID)
	}
}
