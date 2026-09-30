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
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/retention"
	"github.com/hollis-labs/tangent/internal/room"
)

// TestRetentionStatusIsReadOnlyAndPayloadBounded asserts the two properties
// that made this a read-only tool: it reports the custody posture, and it
// carries nothing ADR 0002 §8 forbids.
func TestRetentionStatusIsReadOnlyAndPayloadBounded(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "retention-tool.db")
	database, err := tangentdb.Open(databasePath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrateErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	server, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		tangentmcp.WithRetentionReporter(retentionReporter(t, database, databasePath)),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()

	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	registered := false
	for _, tool := range listed.Tools {
		if tool.Name == "tangent.retention_status" {
			registered = true
		}
		// The retention *operations* must never appear as tools. Erasure
		// authority belongs to the local user at the machine.
		for _, forbidden := range []string{
			"tangent.erase", "tangent.purge", "tangent.redact", "tangent.retention_apply",
			"tangent.db_restore", "tangent.db_repair",
		} {
			if tool.Name == forbidden {
				t.Fatalf("a mutating retention operation is exposed as an MCP tool: %s", tool.Name)
			}
		}
	}
	if !registered {
		t.Fatal("tangent.retention_status was not registered")
	}

	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.retention_status", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("retention_status returned an error result: %+v", result.Content)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var status tangentdb.RetentionStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("decode retention status: %v", err)
	}
	if !status.GuardsIntact || status.GuardCount == 0 {
		t.Fatalf("a freshly migrated database reports guards %v / %d",
			status.GuardsIntact, status.GuardCount)
	}
	if status.InteractionWindowHours == 0 || status.SurfaceWindowHours == 0 {
		t.Fatalf("windows are not reported: %+v", status)
	}
	if len(status.Limitations) == 0 {
		t.Fatal("the standing limitations are not reported")
	}
	// The database path is the one identifier this tool holds and must never
	// return: internal/db.Status uses it to probe the lock sidecar and reports
	// only whether the lock is held.
	body := string(raw)
	if strings.Contains(body, databasePath) || strings.Contains(body, filepath.Dir(databasePath)) {
		t.Fatalf("the retention status leaked a filesystem path: %s", body)
	}
}

func retentionReporter(t *testing.T, database *sql.DB, databasePath string) *retention.Reporter {
	t.Helper()
	reporter, err := retention.NewReporter(database, databasePath)
	if err != nil {
		t.Fatalf("retention.NewReporter: %v", err)
	}
	return reporter
}
