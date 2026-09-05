package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/health"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

// tangent.health_report is how a Tether-connected agent asks the operability
// questions. It reaches Tangent over MCP and has no reason to hold an HTTP
// client, so a readiness answer that only exists on /readyz is an answer that
// consumer never sees — and "the database is gone" becomes "the workflow is
// flaky".

// connectWithHealth builds a real MCP server over a real envelope registry and
// a real migrated database, so the readiness the tool reports is measured
// rather than described.
func connectWithHealth(
	t *testing.T,
	options func(*envelope.Service) []tangentmcp.Option,
) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterAll(envSvc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}
	var serverOptions []tangentmcp.Option
	if options != nil {
		serverOptions = options(envSvc)
	}
	srv, err := tangentmcp.New(envSvc, envelope.NewDispatcher(envSvc), room.NewManager(nil), "", serverOptions...)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-test", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func migratedDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := tangentdb.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(db) })
	if err := tangentdb.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

// healthOptions wires a reporter over the same envelope service the MCP server
// is built on, so capability health reports the registry that is actually
// serving rather than a second one assembled for the test.
func healthOptions(db *sql.DB) func(*envelope.Service) []tangentmcp.Option {
	return func(envSvc *envelope.Service) []tangentmcp.Option {
		return []tangentmcp.Option{tangentmcp.WithHealthReporter(health.NewReporter(
			health.WithDatabase(db),
			health.WithDefinitionRegistry(envSvc),
			health.WithRendererHost(func() health.RendererHost {
				return health.RendererHost{Mode: "embedded", Present: true, Assets: 3}
			}),
			health.WithDeliveryWorker(func() health.DeliveryWorker {
				return health.DeliveryWorker{Authorized: true}
			}),
		))}
	}
}

func TestHealthReportAnswersAllThreeQuestions(t *testing.T) {
	db := migratedDatabase(t)
	client := connectWithHealth(t, healthOptions(db))

	var report struct {
		HostVersion string                `json:"host_version"`
		Liveness    health.LivenessReport `json:"liveness"`
		Readiness   struct {
			Status string `json:"status"`
			Probe  string `json:"probe"`
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Action string `json:"operator_action"`
			} `json:"checks"`
		} `json:"readiness"`
		CapabilitySummary struct {
			ManagedDefinitions int    `json:"managed_definitions"`
			Usable             int    `json:"usable_definitions"`
			EffectPostureNote  string `json:"effect_posture_note"`
		} `json:"capability_summary"`
		Capability map[string]any `json:"capability"`
	}
	callDefinitionTool(t, client, "tangent.health_report", map[string]any{}, &report)

	if report.Liveness.Status != "ok" || report.Liveness.Probe != health.ProbeLiveness {
		t.Fatalf("liveness = %+v, want the frozen ok token", report.Liveness)
	}
	if report.Readiness.Probe != health.ProbeReadiness || len(report.Readiness.Checks) != 5 {
		t.Fatalf("readiness = %+v, want the fixed five checks", report.Readiness)
	}
	if report.CapabilitySummary.ManagedDefinitions == 0 {
		t.Fatal("capability summary reported no managed definitions in a fully-registered build")
	}
	if report.CapabilitySummary.Usable != report.CapabilitySummary.ManagedDefinitions {
		t.Errorf("summary reports %d usable of %d managed; the shipped registry is fully available",
			report.CapabilitySummary.Usable, report.CapabilitySummary.ManagedDefinitions)
	}
	// No shipped manifest declares a host-mediated capability, so the note has
	// to say what an effect request actually meets.
	if !strings.Contains(report.CapabilitySummary.EffectPostureNote, "effect_capability_undeclared") {
		t.Errorf("effect posture note = %q, want the refusal code named",
			report.CapabilitySummary.EffectPostureNote)
	}
	if report.Capability != nil {
		t.Error("a kind-less request returned a per-kind report as well as a summary")
	}
}

func TestHealthReportNarrowsToOneRequestedKind(t *testing.T) {
	db := migratedDatabase(t)
	client := connectWithHealth(t, healthOptions(db))

	var report struct {
		Capability struct {
			Kind     string `json:"kind"`
			Presence string `json:"presence"`
			Usable   bool   `json:"usable"`
			State    string `json:"materialization_state"`
			Effects  struct {
				RequestOutcome string `json:"effect_request_outcome"`
			} `json:"effects"`
		} `json:"capability"`
		CapabilitySummary map[string]any `json:"capability_summary"`
	}
	callDefinitionTool(t, client, "tangent.health_report",
		map[string]any{"kind": "tangent.triage"}, &report)

	if report.Capability.Kind != "tangent.triage" {
		t.Fatalf("capability kind = %q, want tangent.triage", report.Capability.Kind)
	}
	if report.Capability.Presence != string(health.PresenceManaged) || !report.Capability.Usable {
		t.Fatalf("capability = %+v, want a usable managed kind", report.Capability)
	}
	if report.Capability.State != "available" {
		t.Fatalf("state = %q, want the definition vocabulary's available", report.Capability.State)
	}
	if report.Capability.Effects.RequestOutcome != "effect_capability_undeclared" {
		t.Errorf("effect request outcome = %q, want the truthful refusal for an undeclaring kind",
			report.Capability.Effects.RequestOutcome)
	}
	if report.CapabilitySummary != nil {
		t.Error("a kind-scoped request returned the whole summary as well")
	}
}

func TestHealthReportSaysSoWhenNoReporterIsWired(t *testing.T) {
	client := connectWithHealth(t, nil)
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.health_report",
	})
	if err != nil {
		t.Fatalf("CallTool health_report: %v", err)
	}
	if !result.IsError {
		t.Fatal("a build with no health reporter returned a health document; " +
			"a report nothing measured is the same lie as a 200 with no database")
	}
	body := extractText(t, result)
	if !strings.Contains(body, "health_unavailable") {
		t.Fatalf("error body = %s, want the health_unavailable code", body)
	}
}

// TestHealthReportRefusesToInventReadinessForAClosedDatabase is the MCP-side
// version of the defect this task closed: the tool call succeeds, and the
// document it returns says the process is not ready.
func TestHealthReportRefusesToInventReadinessForAClosedDatabase(t *testing.T) {
	db := migratedDatabase(t)
	client := connectWithHealth(t, healthOptions(db))
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	var report struct {
		Readiness struct {
			Status string `json:"status"`
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Action string `json:"operator_action"`
			} `json:"checks"`
		} `json:"readiness"`
	}
	callDefinitionTool(t, client, "tangent.health_report", map[string]any{}, &report)

	if report.Readiness.Status != string(health.SummaryUnavailable) {
		t.Fatalf("readiness status = %q, want unavailable with the database closed",
			report.Readiness.Status)
	}
	var database struct {
		status, action string
	}
	for _, check := range report.Readiness.Checks {
		if check.Name == health.CheckDatabase {
			database.status, database.action = check.Status, check.Action
		}
	}
	if database.status != string(health.StatusFail) || database.action == "" {
		t.Fatalf("database check = %+v, want a failure carrying an operator action", database)
	}
	// And nothing in the document says where the database lives.
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, forbidden := range []string{".db", "/var/", "/tmp/", "sqlite3", t.TempDir()} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("readiness document contains %q, which is filesystem detail: %s",
				forbidden, encoded)
		}
	}
}
