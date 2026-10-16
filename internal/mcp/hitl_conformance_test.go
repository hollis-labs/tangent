package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/libs/mesh/core/hitl/hitltest"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	"github.com/hollis-labs/tangent/internal/room"
)

// mcpHITLAdapter drives Tangent's tangent_hitl_* MCP tools through the
// hitltest conformance kit. Enqueue, Get, Await and Withdraw go over the real
// MCP tool boundary; ParticipantResolve is the operator path (present, then
// resolve) on the HITL service, which has no MCP tool by design.
type mcpHITLAdapter struct {
	server  *Server
	client  *mcpsdk.ClientSession
	service *hitl.Service
}

var _ hitltest.Adapter = (*mcpHITLAdapter)(nil)

func (a *mcpHITLAdapter) call(ctx context.Context, tool string, doc []byte) ([]byte, error) {
	var arguments map[string]any
	if err := json.Unmarshal(doc, &arguments); err != nil {
		return nil, err
	}
	result, err := a.client.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	var body []byte
	if len(result.Content) > 0 {
		if text, ok := result.Content[0].(*mcpsdk.TextContent); ok {
			body = []byte(text.Text)
		}
	}
	if result.IsError {
		return body, errors.New("tool error: " + string(body))
	}
	return body, nil
}

func (a *mcpHITLAdapter) Enqueue(ctx context.Context, doc []byte) ([]byte, error) {
	return a.call(ctx, "tangent_hitl_enqueue", doc)
}

func (a *mcpHITLAdapter) Get(ctx context.Context, doc []byte) ([]byte, error) {
	return a.call(ctx, "tangent_hitl_get", doc)
}

func (a *mcpHITLAdapter) Await(ctx context.Context, doc []byte) ([]byte, error) {
	return a.call(ctx, "tangent_hitl_await", doc)
}

func (a *mcpHITLAdapter) Withdraw(ctx context.Context, doc []byte) ([]byte, error) {
	return a.call(ctx, "tangent_hitl_withdraw", doc)
}

func (a *mcpHITLAdapter) ParticipantResolve(ctx context.Context, itemID string, response []byte) ([]byte, error) {
	fail := func(err error) ([]byte, error) {
		var wire *rawToolError
		if herr := a.server.hitlError(err); errors.As(herr, &wire) {
			body, _ := json.Marshal(wire.body)
			return body, err
		}
		return nil, err
	}
	view, err := a.service.InspectOperatorItem(ctx, itemID)
	if err != nil {
		return fail(err)
	}
	projection := int64(1)
	if view.TerminalOutcome == nil { // a terminal item cannot be presented
		if _, presentErr := a.service.Present(ctx, hitl.PresentInput{
			ItemID: itemID, ExpectedRevision: view.Revision,
			PresentedProjectionRevision: projection, ConnectionID: "conformance",
		}); presentErr != nil {
			return fail(presentErr)
		}
		view, err = a.service.InspectOperatorItem(ctx, itemID)
		if err != nil || view.PresentedProjectionRevision == nil {
			return fail(err)
		}
		projection = *view.PresentedProjectionRevision
	}
	outcome, err := a.service.Resolve(ctx, hitl.ResolveInput{
		ItemID: itemID, ExpectedRevision: view.Revision,
		PresentedProjectionRevision: projection, Response: response,
	})
	if err != nil {
		return fail(err)
	}
	return json.Marshal(outcome)
}

func (a *mcpHITLAdapter) ExpireDue(ctx context.Context) error {
	_, err := a.service.ExpireDue(ctx)
	return err
}

func newConformanceAdapter(t *testing.T) *mcpHITLAdapter {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "conformance.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	for name, register := range map[string]func(*envelope.Service) error{
		"approval-queue": extensions.RegisterApprovalQueue,
		"hitl-item":      extensions.RegisterHITLItem,
	} {
		if registrationErr := register(envelopeService); registrationErr != nil {
			t.Fatalf("Register %s: %v", name, registrationErr)
		}
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
		interaction.WithPrivilegedActorPolicy(hitl.ExpiryPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatalf("hitl.NewService: %v", err)
	}
	server, err := New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		WithInteractionService(interactions), WithHITLService(hitlService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "conformance", Version: "v1"}, nil).
		Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = serverSession.Close() })
	return &mcpHITLAdapter{server: server, client: client, service: hitlService}
}

// TestHITLToolsPassLibraryConformance runs the shared go-hitl conformance
// scenarios (libs/mesh/core/hitl/hitltest) against the tangent_hitl_* tools.
func TestHITLToolsPassLibraryConformance(t *testing.T) {
	hitltest.RunConformance(t, newConformanceAdapter(t),
		hitltest.CapCallerIsolation, hitltest.CapExpiry)
}
