package mcp_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

// newHITLAuthServer builds an MCP server with verified producer credentials
// (alpha, beta) and the HITL tools backed by Tangent's own service or, with
// goHITL, by the go-hitl Service. It returns the database for inspection.
func newHITLAuthServer(t *testing.T, goHITL bool) (*tangentmcp.Server, *sql.DB) {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatal(migrationErr)
	}
	env, err := envelope.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if registerErr := extensions.RegisterHITLItem(env); registerErr != nil {
		t.Fatal(registerErr)
	}
	store := interaction.NewStore(database)
	catalog := interaction.NewEnvelopeDefinitionCatalog(env, tangentmcp.HostVersion, interaction.WithRetainedMaterialStore(store))
	interactions, err := interaction.NewService(store, catalog, interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := tangentmcp.ParseCallerAuth(fmt.Sprintf("alpha=%x,beta=%x", sha256.Sum256([]byte(producerAToken)), sha256.Sum256([]byte(producerBToken))), "")
	if err != nil {
		t.Fatal(err)
	}
	options := []tangentmcp.Option{
		tangentmcp.WithCallerAuth(auth), tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService),
	}
	if goHITL {
		options = append(options, tangentmcp.WithHITLWire(gohitl.New(database, catalog)))
	}
	server, err := tangentmcp.New(env, envelope.NewDispatcher(env), room.NewManager(database), "", options...)
	if err != nil {
		t.Fatal(err)
	}
	return server, database
}

// A verified gateway producer's scope comes from its token, never from the
// arguments, and the HITL rows record it identically on both backends: a
// producer that asserts another producer's application id in the request is
// still its own caller, and the other producer cannot read its items.
func TestVerifiedGatewayCallerScopeIsIdenticalOnBothBackends(t *testing.T) {
	type recorded struct{ scope, authority string }
	seen := map[string]recorded{}
	for name, goHITL := range map[string]bool{"native": false, "gohitl": true} {
		t.Run(name, func(t *testing.T) {
			server, database := newHITLAuthServer(t, goHITL)
			httpServer := httptest.NewServer(server.HTTPHandler())
			t.Cleanup(httpServer.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			alpha := connectCallerAuthClient(ctx, t, httpServer.URL, "http", producerAToken)
			beta := connectCallerAuthClient(ctx, t, httpServer.URL, "http", producerBToken)

			request := map[string]any{
				"contract_version": "1.0", "kind": "approval", "idempotency_key": "gw-1", "title": "T", "summary": "S",
				"request": "Approve?", "source": map[string]any{"application_id": "beta", "agent_id": "forged"},
			}
			item := callInteractionTool[map[string]any](t, alpha, "tangent_hitl_enqueue", request)
			itemID, _ := item["item_id"].(string)
			if itemID == "" {
				t.Fatalf("no item id in %v", item)
			}
			var got recorded
			if err := database.QueryRow(`SELECT caller_scope, caller_authority FROM interactions WHERE id = ?`, itemID).Scan(&got.scope, &got.authority); err != nil {
				t.Fatal(err)
			}
			if got.scope != "gateway:alpha" || got.authority != "gateway" {
				t.Fatalf("a verified producer must be recorded as gateway:alpha, got %+v", got)
			}
			seen[name] = got

			args := map[string]any{"contract_version": "1.0", "item_id": itemID, "caller": map[string]any{"application_id": "alpha", "principal_ref": "forged"}}
			_ = callInteractionTool[map[string]any](t, alpha, "tangent_hitl_get", args)
			for _, tool := range []string{"tangent_hitl_get", "tangent_hitl_withdraw", "tangent_hitl_await"} {
				requireAuthNotFound(ctx, t, beta, tool, args)
			}
			// the same idempotency key from the other producer is its own item
			other := callInteractionTool[map[string]any](t, beta, "tangent_hitl_enqueue", request)
			if other["item_id"] == itemID {
				t.Fatal("idempotency crossed producers")
			}
		})
	}
	if seen["native"] != seen["gohitl"] {
		t.Fatalf("backends record the verified caller differently: %+v vs %+v", seen["native"], seen["gohitl"])
	}
}
