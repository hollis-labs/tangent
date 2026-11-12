package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const testReplyTo = "msg://agent/agent-mux/agt_test000001"

func submitWithReplyTo(t *testing.T, service *Service, surfaceID, key, replyTo string) InteractionHandle {
	t.Helper()
	input := testSubmitInput(surfaceID, key)
	input.ExternalRefs = json.RawMessage(`{"reply_to":"` + replyTo + `"}`)
	handle, err := service.SubmitInteraction(context.Background(), input)
	if err != nil {
		t.Fatalf("SubmitInteraction %s: %v", key, err)
	}
	return handle
}

func presentHandle(t *testing.T, service *Service, handle InteractionHandle) InteractionHandle {
	t.Helper()
	presented, err := service.AcknowledgePresentation(context.Background(), PresentInteractionInput{
		InteractionID: handle.InteractionID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: 1, Participant: testParticipant(),
	})
	if err != nil {
		t.Fatalf("AcknowledgePresentation: %v", err)
	}
	return presented
}

// An interaction that names a reply address gets a tether_mail delivery beside
// the caller-pull one, on resolution and on every terminal path.
func TestReplyToQueuesTetherMailOnResolutionAndTerminalPaths(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "reply-to")

	presented := presentHandle(t, service, submitWithReplyTo(t, service, surface.SurfaceID, "answered", testReplyTo))
	resolved, err := service.ResolveInteraction(ctx, ResolveInteractionInput{
		InteractionID: presented.InteractionID, ExpectedInteractionRevision: presented.Revision,
		PresentedProjectionRevision: 1, Participant: testParticipant(), ResponseKind: "data",
		ResponsePayload: json.RawMessage(`{"decision":"yes"}`),
	})
	if err != nil {
		t.Fatalf("ResolveInteraction: %v", err)
	}
	kinds := map[string]bool{}
	for _, d := range resolved.Deliveries {
		var b struct {
			Kind    string `json:"kind"`
			ReplyTo string `json:"reply_to"`
		}
		_ = json.Unmarshal(d.DestinationBinding, &b)
		kinds[b.Kind] = true
		if b.Kind == DestinationKindTetherMail && b.ReplyTo != testReplyTo {
			t.Fatalf("tether_mail reply_to = %q", b.ReplyTo)
		}
	}
	if !kinds["caller_pull"] || !kinds[DestinationKindTetherMail] {
		t.Fatalf("a resolution with reply_to needs caller_pull and tether_mail deliveries, got %v", kinds)
	}

	canceled := submitWithReplyTo(t, service, surface.SurfaceID, "canceled", testReplyTo)
	if _, err := service.CancelInteraction(ctx, CancelInteractionInput{
		InteractionID: canceled.InteractionID, ExpectedRevision: canceled.Revision,
		Requester: testCaller(), Cause: TerminalCauseCallerCanceled,
	}); err != nil {
		t.Fatalf("CancelInteraction: %v", err)
	}
	notes, listErr := store.ListTerminalNotifications(ctx, canceled.InteractionID)
	if listErr != nil || len(notes) != 2 {
		t.Fatalf("a canceled interaction with reply_to needs 2 notifications, got %d, %v", len(notes), listErr)
	}
}

// Without a reply address, or with Tangent's own internal one, nothing is mailed.
func TestNoReplyMailWithoutAUsableReplyAddress(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "no-reply-to")

	for name, handle := range map[string]InteractionHandle{
		"none":     mustSubmit(t, service, surface.SurfaceID, "none"),
		"internal": submitWithReplyTo(t, service, surface.SurfaceID, "internal", "msg://app/tangent/internal"),
	} {
		presented := presentHandle(t, service, handle)
		resolved, err := service.ResolveInteraction(ctx, ResolveInteractionInput{
			InteractionID: presented.InteractionID, ExpectedInteractionRevision: presented.Revision,
			PresentedProjectionRevision: 1, Participant: testParticipant(), ResponseKind: "data",
			ResponsePayload: json.RawMessage(`{"decision":"yes"}`),
		})
		if err != nil {
			t.Fatalf("%s: ResolveInteraction: %v", name, err)
		}
		if len(resolved.Deliveries) != 1 {
			t.Fatalf("%s: want only the caller-pull delivery, got %d", name, len(resolved.Deliveries))
		}
	}
}

// Only the reply-mail worker may claim tether_mail; any other claimer skips it,
// and the worker skips caller-pull rows.
func TestTetherMailIsClaimedOnlyByKind(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "claim-kind")
	presented := presentHandle(t, service, submitWithReplyTo(t, service, surface.SurfaceID, "k", testReplyTo))
	if _, err := service.ResolveInteraction(ctx, ResolveInteractionInput{
		InteractionID: presented.InteractionID, ExpectedInteractionRevision: presented.Revision,
		PresentedProjectionRevision: 1, Participant: testParticipant(), ResponseKind: "data",
		ResponsePayload: json.RawMessage(`{"decision":"yes"}`),
	}); err != nil {
		t.Fatalf("ResolveInteraction: %v", err)
	}

	mail, claimErr := store.ClaimNextDelivery(ctx, ClaimDeliveryParams{
		LeaseOwner: "w", LeaseDuration: time.Minute, DestinationKind: DestinationKindTetherMail,
	})
	if claimErr != nil {
		t.Fatalf("claim tether_mail: %v", claimErr)
	}
	var b struct{ Kind string }
	_ = json.Unmarshal(mail.DestinationBinding, &b)
	if b.Kind != DestinationKindTetherMail {
		t.Fatalf("claimed %q, want tether_mail", b.Kind)
	}
	if _, err := store.ClaimNextDelivery(ctx, ClaimDeliveryParams{
		LeaseOwner: "w", LeaseDuration: time.Minute, DestinationKind: DestinationKindTetherMail,
	}); !errors.Is(err, ErrNoDeliveryAvailable) {
		t.Fatalf("second tether_mail claim = %v, want none", err)
	}
	other, err := store.ClaimNextDelivery(ctx, ClaimDeliveryParams{LeaseOwner: "w", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("claim any: %v", err)
	}
	_ = json.Unmarshal(other.DestinationBinding, &b)
	if b.Kind != "caller_pull" {
		t.Fatalf("an unfiltered claim got %q, want caller_pull", b.Kind)
	}
}
