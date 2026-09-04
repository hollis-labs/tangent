package interaction

import (
	"context"
	"errors"
	"testing"
)

type referenceSurfacePolicy struct{ allowed string }

func (policy referenceSurfacePolicy) Authorize(_ string, capability string) bool {
	return capability == policy.allowed
}

func TestInspectSurfaceReferenceIsCapabilityGatedAndReadOnly(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service, err := NewService(
		store,
		testDefinitionCatalog{},
		WithSurfaceAccessPolicy(referenceSurfacePolicy{allowed: "read-evidence"}),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	surface, err := service.OpenSurface(ctx, OpenSurfaceInput{
		ID: "surface-reference", OwnerScope: "owner", IdempotencyKey: "surface-reference",
		Caller: ActorBinding{
			Scope: "owner", PrincipalRef: "reference-test", Authority: "test", Assurance: "asserted",
		},
		Capability: "read-evidence",
	})
	if err != nil {
		t.Fatalf("OpenSurface: %v", err)
	}
	handle, err := service.SubmitInteraction(ctx, SubmitInteractionInput{
		SurfaceID: surface.SurfaceID,
		Caller: ActorBinding{
			Scope: "owner", PrincipalRef: "reference-test", Authority: "test", Assurance: "asserted",
		},
		IdempotencyKey: "reference-1",
		Definition:     DefinitionRef{Kind: "test.generic", Version: "1.0"},
		Request:        []byte(`{"prompt":"durable evidence"}`),
		Capability:     "read-evidence",
	})
	if err != nil {
		t.Fatalf("SubmitInteraction: %v", err)
	}

	_, unauthorizedErr := service.InspectSurfaceReference(ctx, surface.SurfaceID, handle.InteractionID, "wire-value")
	if !errors.Is(unauthorizedErr, ErrUnauthorized) {
		t.Fatalf("unauthorized inspect error = %v", unauthorizedErr)
	}
	before, err := store.GetInteraction(ctx, handle.InteractionID)
	if err != nil {
		t.Fatalf("GetInteraction before: %v", err)
	}
	projection, err := service.InspectSurfaceReference(ctx, surface.SurfaceID, handle.InteractionID, "read-evidence")
	if err != nil {
		t.Fatalf("InspectSurfaceReference: %v", err)
	}
	if projection.Interaction == nil || string(projection.Interaction.RequestSnapshot) != `{"prompt":"durable evidence"}` {
		t.Fatalf("projection = %#v", projection)
	}
	after, err := store.GetInteraction(ctx, handle.InteractionID)
	if err != nil {
		t.Fatalf("GetInteraction after: %v", err)
	}
	if after.Revision != before.Revision || after.State != before.State || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("inspection mutated interaction: before=%#v after=%#v", before, after)
	}
}
