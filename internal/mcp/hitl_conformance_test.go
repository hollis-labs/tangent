package mcp

import (
	"context"
	"database/sql"
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
	"github.com/hollis-labs/tangent/internal/hitl/gohitl"
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
	// libRevisions is true when the go-hitl backend serves the tools. Its
	// revisions are interactions.revision - 3; the operator's native Resolve
	// answers in interactions revisions, so the adapter states them in the
	// lib's numbering before the kit compares the two observations of one
	// outcome. (The operator's response goes to the browser, never to the
	// agent caller, so nothing in the product compares them.)
	libRevisions bool
	db           *sql.DB
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
			return a.inLibRevisions(body), err
		}
		return nil, err
	}
	view, err := a.service.InspectOperatorItem(ctx, itemID)
	if err != nil {
		return fail(err)
	}
	projection := int64(1)
	// A terminal item cannot be presented; a row the go-hitl store created is
	// already presented, with its participant binding.
	if view.TerminalOutcome == nil && view.State == interaction.InteractionStateStaged {
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
	}
	if view.PresentedProjectionRevision != nil {
		projection = *view.PresentedProjectionRevision
	}
	outcome, err := a.service.Resolve(ctx, hitl.ResolveInput{
		ItemID: itemID, ExpectedRevision: view.Revision,
		PresentedProjectionRevision: projection, Response: response,
	})
	if err != nil {
		return fail(err)
	}
	body, err := json.Marshal(outcome)
	return a.inLibRevisions(body), err
}

// inLibRevisions rewrites every interaction_revision in body from Tangent's
// numbering to the go-hitl lib's (minus the staged-row offset of 3).
func (a *mcpHITLAdapter) inLibRevisions(body []byte) []byte {
	if !a.libRevisions {
		return body
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return body
	}
	var shift func(any)
	shift = func(v any) {
		switch typed := v.(type) {
		case map[string]any:
			for key, child := range typed {
				if number, ok := child.(float64); ok && key == "interaction_revision" {
					typed[key] = number - 3
					continue
				}
				shift(child)
			}
		case []any:
			for _, child := range typed {
				shift(child)
			}
		}
	}
	shift(value)
	out, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return out
}

func (a *mcpHITLAdapter) ExpireDue(ctx context.Context) error {
	_, err := a.service.ExpireDue(ctx)
	return err
}

func newConformanceAdapter(t *testing.T) *mcpHITLAdapter {
	t.Helper()
	return newConformanceAdapterFor(t, false)
}

// newConformanceAdapterFor builds the adapter over Tangent's own HITL service
// (goHITL false) or with the go-hitl Service behind the tools (goHITL true).
// The operator's resolve path is Tangent's own in both.
func newConformanceAdapterFor(t *testing.T, goHITL bool) *mcpHITLAdapter {
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
	interactionStore := interaction.NewStore(database)
	catalog := interaction.NewEnvelopeDefinitionCatalog(envelopeService, HostVersion,
		interaction.WithRetainedMaterialStore(interactionStore))
	interactions, err := interaction.NewService(
		interactionStore,
		catalog,
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
	options := []Option{WithInteractionService(interactions), WithHITLService(hitlService)}
	if goHITL {
		options = append(options, WithHITLWire(gohitl.New(database, catalog)))
	}
	server, err := New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		options...,
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
	return &mcpHITLAdapter{server: server, client: client, service: hitlService, libRevisions: goHITL, db: database}
}

// TestHITLToolsPassLibraryConformance runs the shared go-hitl conformance
// scenarios (libs/mesh/core/hitl/hitltest) against the tangent_hitl_* tools.
func TestHITLToolsPassLibraryConformance(t *testing.T) {
	hitltest.RunConformance(t, newConformanceAdapter(t),
		hitltest.CapCallerIsolation, hitltest.CapExpiry)
}

// TestHITLToolsPassLibraryConformanceOnTheGoHITLBackend is the same suite with
// TANGENT_HITL_BACKEND=gohitl: the go-hitl Service over Tangent's tables behind
// the tools, with Tangent's own Resolve as the participant. 17/17 must hold on
// both backends.
func TestHITLToolsPassLibraryConformanceOnTheGoHITLBackend(t *testing.T) {
	hitltest.RunConformance(t, newConformanceAdapterFor(t, true),
		hitltest.CapCallerIsolation, hitltest.CapExpiry)
}

// The same caller must land in the same partition whichever backend serves the
// tools: the vault's creator check and caller isolation compare caller_scope.
func TestHITLCallerScopeIsIdenticalOnBothBackends(t *testing.T) {
	scopes := map[string]string{}
	for name, goHITL := range map[string]bool{"native": false, "gohitl": true} {
		adapter := newConformanceAdapterFor(t, goHITL)
		doc, _ := json.Marshal(map[string]any{
			"contract_version": hitl.ContractVersion, "kind": "approval", "idempotency_key": "scope-1", "title": "T",
			"summary": "S", "request": "Approve?", "source": map[string]any{"application_id": "Scope-App", "agent_id": "agent-1"},
		})
		reply, err := adapter.Enqueue(context.Background(), doc)
		if err != nil {
			t.Fatalf("%s enqueue: %v (%s)", name, err, reply)
		}
		var handle struct {
			ItemID string `json:"item_id"`
		}
		if err = json.Unmarshal(reply, &handle); err != nil || handle.ItemID == "" {
			t.Fatalf("%s handle = %s (%v)", name, reply, err)
		}
		var scope string
		if err = adapter.db.QueryRow(`SELECT caller_scope FROM interactions WHERE id = ?`, handle.ItemID).Scan(&scope); err != nil {
			t.Fatalf("%s caller_scope: %v", name, err)
		}
		scopes[name] = scope
	}
	if scopes["native"] == "" || scopes["native"] != scopes["gohitl"] {
		t.Fatalf("caller_scope differs between backends: native=%q gohitl=%q", scopes["native"], scopes["gohitl"])
	}
}
