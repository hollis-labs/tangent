package interaction

import (
	"context"
	"testing"
)

// The operator can end and remove any open item the Inbox lists, whatever its
// kind, including the forms and reviews agents send through the gateway.
func TestOperatorArchivesAndDeletesGatewayItems(t *testing.T) {
	ctx := context.Background()
	store, _ := openTestStore(t)
	service := newTestService(t, store)
	surface := createTestSurface(t, store, "operator-archive")

	archive, err := store.CreateInteraction(ctx, testInteractionParams(surface.ID, "gateway:tether-gateway", "archive", "q"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ArchiveBrowserInboxItem(ctx, archive.Interaction.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, err := store.GetInteraction(ctx, archive.Interaction.ID)
	if err != nil || got.State != InteractionStateCanceled || got.TerminalCause != TerminalCauseAdministratorCanceled {
		t.Fatalf("archived interaction = %+v, %v", got, err)
	}
	if err = service.ArchiveBrowserInboxItem(ctx, archive.Interaction.ID); err != nil {
		t.Fatalf("archiving a closed item must be a no-op, got %v", err)
	}

	del, err := store.CreateInteraction(ctx, testInteractionParams(surface.ID, "gateway:tether-gateway", "delete", "q"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteBrowserInboxItem(ctx, del.Interaction.ID); err != nil {
		t.Fatalf("delete an open item: %v", err)
	}
	hidden, err := service.HiddenInboxItems(ctx)
	if err != nil || !hidden[del.Interaction.ID] {
		t.Fatalf("a deleted item must be hidden from the Inbox: %v %v", hidden, err)
	}
}
