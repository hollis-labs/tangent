package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRoomWorkflow_IdenticalRetryReturnsOneRoomAndOneHandle is the regression
// for the defect that shipped past CW-20260904-0068's acceptance: the tests
// behind it asserted interaction identity across a retry, and interaction
// identity was never what broke.
//
// Six identical calls that name no room. The handle each one returns has to be
// the same handle — surface, interaction, room, envelope id, and url — because
// that is what the compatibility contract promises a caller who lost its first
// pending receipt and is retrying to recover the handle. It is asserted as
// whole-body equality rather than field by field, so a future field cannot be
// added to the receipt and left unstable.
//
// The row counts are the other half of the same fact. A retry loop that mints
// a room and an envelope projection per attempt grows without bound, and every
// projection past the first is a second view of one interaction that an
// operator can be sent to.
func TestRoomWorkflow_IdenticalRetryReturnsOneRoomAndOneHandle(t *testing.T) {
	// Both completion modes, because advanceRoomEnvelope is the single shared
	// adapter: the live reproduction used async only, and wait reached the
	// same handle construction by the same path.
	for _, mode := range []string{"wait", "async"} {
		t.Run(mode, func(t *testing.T) {
			rg := newSessionRigWith(t, sessionRigOptions{durable: true, window: testWindow})
			defer rg.cleanup()
			fixture := fixtureByTool(t, "tangent.approval-queue")

			const (
				envelopeID = "acceptance-aq-001"
				retries    = 6
			)
			completion := map[string]any{"mode": mode}

			bodies := make([]string, retries)
			for attempt := range retries {
				// The room is deliberately not named. That is how the live
				// caller invoked the tool, and naming one is what hid this in
				// every existing test.
				result := callWorkflow(t, rg, fixture, "", envelopeID, completion, nil)
				if result.err != nil {
					t.Fatalf("attempt %d transport err: %v", attempt, result.err)
				}
				if result.result.IsError {
					t.Fatalf("attempt %d produced an MCP error: %s", attempt, extractText(t, result.result))
				}
				bodies[attempt] = extractText(t, result.result)
			}
			for attempt := 1; attempt < retries; attempt++ {
				if bodies[attempt] != bodies[0] {
					t.Fatalf("retry %d returned a different receipt:\nfirst: %s\nretry: %s",
						attempt, bodies[0], bodies[attempt])
				}
			}

			var receipt pendingReceipt
			if err := json.Unmarshal([]byte(bodies[0]), &receipt); err != nil {
				t.Fatalf("unmarshal pending receipt: %v (body %s)", err, bodies[0])
			}
			if receipt.Status != "pending" {
				t.Fatalf("result is not a pending receipt: %s", bodies[0])
			}

			// The handle names the interaction's own binding rather than a
			// room this call happened to route through.
			interactionID := singleInteractionID(t, rg, envelopeID)
			var boundRoom string
			if err := rg.db.QueryRow(
				`SELECT legacy_room_id FROM interactions WHERE id = ?`, interactionID,
			).Scan(&boundRoom); err != nil {
				t.Fatalf("read bound legacy room: %v", err)
			}
			if receipt.Handle.InteractionID != interactionID {
				t.Fatalf("handle names interaction %q, want %q", receipt.Handle.InteractionID, interactionID)
			}
			if receipt.Handle.RoomID != boundRoom {
				t.Fatalf("handle names room %q but the interaction is bound to %q",
					receipt.Handle.RoomID, boundRoom)
			}
			if !strings.HasSuffix(receipt.Handle.URL, "/r/"+boundRoom) {
				t.Fatalf("handle url %q does not address the bound room %q", receipt.Handle.URL, boundRoom)
			}

			assertRowCount(t, rg, 1,
				`SELECT COUNT(*) FROM interactions WHERE legacy_envelope_id = ?`, envelopeID)
			// Nothing else in this rig creates a room, so the total is what
			// the six calls produced.
			assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM rooms`)
			assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM envelopes`)
			assertNoDivergentProjections(t, rg)

			// A conflicting retry is still a hard refusal, and refusing it
			// cannot mint a room either.
			changed := map[string]any{
				"queue_id": "queue-2",
				"items": []any{
					map[string]any{
						"id": "item-2", "title": "Something else entirely",
						"summary": "A different queue", "description": "Bump package B",
						"action_options": []any{
							map[string]any{"id": "merge", "label": "Merge"},
						},
					},
				},
			}
			conflict := callWorkflow(t, rg, fixture, "", envelopeID, completion, changed)
			if conflict.err != nil {
				t.Fatalf("conflict transport err: %v", conflict.err)
			}
			if !conflict.result.IsError {
				t.Fatalf("changed payload was accepted: %s", extractText(t, conflict.result))
			}
			if code := errorCodeOf(t, conflict.result); code != "IDEMPOTENCY_CONFLICT" {
				t.Fatalf("conflict code = %q, want IDEMPOTENCY_CONFLICT", code)
			}
			if body := extractText(t, conflict.result); !strings.Contains(body, interactionID) {
				t.Fatalf("conflict does not name the bound interaction %q: %s", interactionID, body)
			}
			assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM rooms`)
			assertRowCount(t, rg, 1,
				`SELECT COUNT(*) FROM interactions WHERE legacy_envelope_id = ?`, envelopeID)
		})
	}
}

// TestRoomWorkflow_RetryIntoAnotherRoomKeepsTheBoundHandle covers the other
// direction of the same rule. The room is deliberately not part of the
// workflow identity, so routing an identical retry at a different room is
// still the same request — and it must receive the bound room's handle rather
// than acquire a second projection in the room it named.
//
// tangent.session_advance is included because it names its room as a top-level
// argument and never passes through the shared room resolution, so it is the
// case that only the adapter's own handle construction can get right.
func TestRoomWorkflow_RetryIntoAnotherRoomKeepsTheBoundHandle(t *testing.T) {
	for _, tool := range []string{"tangent.triage", "tangent.session_advance"} {
		t.Run(tool, func(t *testing.T) {
			rg := newDurableRig(t)
			defer rg.cleanup()
			fixture := fixtureByTool(t, tool)
			bound, _ := createSession(t, rg, "bound")
			other, _ := createSession(t, rg, "other")

			const envelopeID = "rebound-1"
			async := map[string]any{"mode": "async"}
			first := callWorkflow(t, rg, fixture, bound, envelopeID, async, nil)
			if first.err != nil || first.result.IsError {
				t.Fatalf("first call failed: err=%v body=%s", first.err, extractText(t, first.result))
			}
			retry := callWorkflow(t, rg, fixture, other, envelopeID, async, nil)
			if retry.err != nil || retry.result.IsError {
				t.Fatalf("retry failed: err=%v body=%s", retry.err, extractText(t, retry.result))
			}
			if got, want := extractText(t, retry.result), extractText(t, first.result); got != want {
				t.Fatalf("retry into another room changed the handle:\nfirst: %s\nretry: %s", want, got)
			}

			assertRowCount(t, rg, 1,
				`SELECT COUNT(*) FROM interactions WHERE legacy_envelope_id = ?`, envelopeID)
			assertRowCount(t, rg, 1, `SELECT COUNT(*) FROM envelopes WHERE envelope_id = ?`, envelopeID)
			assertRowCount(t, rg, 0, `SELECT COUNT(*) FROM envelopes WHERE room_id = ?`, other)
			assertNoDivergentProjections(t, rg)
		})
	}
}

// assertNoDivergentProjections asserts no legacy envelope row sits in a room
// other than the one its interaction is bound to.
//
// This is the predicate that identifies the rows the defect left behind, and
// it is the invariant that makes the room resolver lease meaningfully
// surface-scoped: the lease lives on one *room.Room, so it only serializes
// terminal actions across a surface while a surface has exactly one room
// projecting it. A divergent projection hands two operators two rooms, two
// independent leases, and two apparent permissions to answer one interaction.
// Nothing durable diverges when that happens — the interaction store admits
// exactly one resolution and refuses the rest as terminal — but the second
// operator is told they hold the lease right up until their submission is
// refused, which is not arbitration.
func assertNoDivergentProjections(t *testing.T, rg *sessionRig) {
	t.Helper()
	assertRowCount(t, rg, 0, `
SELECT COUNT(*) FROM envelopes e
JOIN interactions i ON i.legacy_envelope_id = e.envelope_id
WHERE i.legacy_room_id <> e.room_id`)
}
