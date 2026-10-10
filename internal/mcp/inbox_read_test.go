package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInboxReadToolsWireAuthorityAndReservedMutationRefusal(t *testing.T) {
	ctx := context.Background()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "inbox-mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = tangentdb.RunMigrations(database); err != nil {
		t.Fatal(err)
	}
	envelopes, err := envelope.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store := interaction.NewStore(database)
	service, err := interaction.NewService(store, interaction.NewEnvelopeDefinitionCatalog(envelopes, tangentmcp.HostVersion), interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	server, err := tangentmcp.New(envelopes, envelope.NewDispatcher(envelopes), room.NewManager(nil), "", tangentmcp.WithInteractionService(service))
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateSurface(ctx, interaction.CreateSurfaceParams{ID: hitl.DefaultSurfaceID, OwnerScope: "operator:local", Metadata: json.RawMessage(`{}`), Policy: json.RawMessage(`{}`), ActorRef: "test", Authority: "test"})
	if err != nil {
		t.Fatal(err)
	}
	surface, err = store.AdvanceSurface(ctx, interaction.AdvanceSurfaceParams{SurfaceID: surface.ID, ExpectedRevision: surface.Revision, To: interaction.SurfaceStateActive, ActorRef: "test", Authority: "test"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(scope, key string) interaction.InteractionRecord {
		t.Helper()
		result, createErr := store.CreateInteraction(ctx, interaction.CreateInteractionParams{
			SurfaceID: surface.ID, CallerScope: scope, CallerPrincipalRef: "synthetic-agent", CallerAuthority: "test", CallerAssurance: "test", IdempotencyKey: key,
			Definition:      interaction.DefinitionBinding{Publisher: "test", Kind: "synthetic.read", Version: "1.0", Revision: 1, Digest: "sha256:test", Source: "test", SchemaIdentity: "test", SchemaDigest: "sha256:test", HostVersion: "test", Assurance: "test"},
			RequestSnapshot: json.RawMessage(`{"text":"synthetic inbox message"}`), ExternalRefs: json.RawMessage(`{"private_binding":"do-not-project"}`), Policy: json.RawMessage(`{"credential":"do-not-project"}`), ActorRef: "test", Authority: "test",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return result.Interaction
	}
	own := create("standalone-local:reader", "own")
	foreign := create("gateway:verified:reader", "foreign")
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tangent.inbox_list", "tangent.inbox_search", "tangent.inbox_get"} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == name {
				found = true
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
					t.Fatal("read tool missing read-only annotation")
				}
			}
		}
		if !found {
			t.Fatalf("tool %s not registered", name)
		}
	}
	page := callInteractionTool[interaction.InboxReadPage](t, client, "tangent.inbox_list", map[string]any{"requester_scope": "reader", "limit": 1})
	if len(page.Items) != 1 || page.Items[0].ItemID != own.ID || page.NextCursor != "" {
		t.Fatalf("listing: %#v", page)
	}
	item := callInteractionTool[interaction.InboxReadItem](t, client, "tangent.inbox_get", map[string]any{"requester_scope": "reader", "item_id": own.ID})
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "synthetic inbox message") || strings.Contains(string(encoded), "do-not-project") {
		t.Fatalf("unsafe projection: %s", encoded)
	}
	searched := callInteractionTool[interaction.InboxReadPage](t, client, "tangent.inbox_search", map[string]any{"requester_scope": "reader", "query": "INBOX MESSAGE"})
	if len(searched.Items) != 1 || searched.Items[0].ItemID != own.ID {
		t.Fatal("search failed")
	}
	for _, test := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"tangent.inbox_get", map[string]any{"requester_scope": "nonsubscriber", "item_id": own.ID}, "unauthorized"},
		{"tangent.inbox_get", map[string]any{"requester_scope": "gateway:verified:reader", "item_id": foreign.ID}, "not_found"},
		{"tangent.inbox_get", map[string]any{"requester_scope": "operator:local", "item_id": foreign.ID}, "not_found"},
		{"tangent.inbox_list", map[string]any{"requester_scope": "reader", "limit": 101}, "invalid_request"},
		{"tangent.inbox_search", map[string]any{"requester_scope": "reader", "query": " "}, "invalid_request"},
		{"tangent.inbox_list", map[string]any{"requester_scope": "reader", "created_from": "not-a-time"}, "invalid_request"},
		{"tangent.surface_close", map[string]any{"surface_id": surface.ID, "expected_revision": surface.Revision, "requester": map[string]any{"scope": "reader", "principal_ref": "synthetic-agent"}, "policy_ref": "test"}, "unauthorized"},
	} {
		result, callErr := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: test.name, Arguments: test.args})
		if callErr != nil {
			t.Fatal(callErr)
		}
		if !result.IsError || !strings.Contains(extractText(t, result), test.code) {
			t.Fatalf("refusal %s: %s", test.name, extractText(t, result))
		}
	}
	// Valid arguments plus forged context metadata must reach the ordinary
	// refusal, independently of the additional-property schema checks below.
	for _, refusal := range []struct{ scope, item, code string }{
		{"reader", foreign.ID, "not_found"},
		{"nonsubscriber", own.ID, "unauthorized"},
		{"operator:local", own.ID, "unauthorized"},
	} {
		result, callErr := client.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.inbox_get",
			Arguments: map[string]any{"requester_scope": refusal.scope, "item_id": refusal.item},
			Meta:      map[string]any{"authority": "operator", "participant_scope": "operator:local", "capability": "tangent:inbox-read:v1"},
		})
		if callErr != nil {
			t.Fatal(callErr)
		}
		if !result.IsError || !strings.Contains(extractText(t, result), refusal.code) {
			t.Fatal("context metadata widened read authority")
		}
	}
	for _, name := range []string{"tangent.inbox_get", "tangent.inbox_list"} {
		args := map[string]any{"requester_scope": "reader", "item_id": foreign.ID, "authority": "gateway:verified", "capability": "tangent:inbox-read:v1"}
		result, callErr := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args, Meta: map[string]any{"authority": "operator"}})
		if callErr == nil && !result.IsError {
			t.Fatalf("accepted wire authority/capability %s", name)
		}
	}
	current, err := store.GetSurface(ctx, surface.ID)
	if err != nil || current.State != interaction.SurfaceStateActive {
		t.Fatal("read capability widened mutation")
	}
}
