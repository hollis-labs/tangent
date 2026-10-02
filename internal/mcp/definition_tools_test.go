package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

// definitionToolsClient boots the minimum server the registry diagnostics need.
// They deliberately do not require the durable interaction substrate: an
// embedder without it still has to be able to ask why a kind is not being
// served.
func definitionToolsClient(t *testing.T) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registerErr := extensions.RegisterAll(envelopeService); registerErr != nil {
		t.Fatalf("RegisterAll: %v", registerErr)
	}
	// The plugin door is NOT walked here, and that is correct rather than a
	// gap. It contributes no kind in this build — CW-20260911-0036 established
	// that the one kind it appeared to contribute was the host's — and since
	// CW-20260911-0070 a plugin is installed rather than compiled in, so
	// walking it would make this rig depend on what happens to be installed on
	// the machine running the test.
	server, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "")
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	return connectInteractionClient(t, server)
}

// callDefinitionTool decodes one tool result into out. The interaction tests'
// generic helper returns a value, which cannot name the anonymous response
// structs these assertions use; taking a pointer keeps each test's expected
// shape declared beside the assertions that read it.
func callDefinitionTool(
	t *testing.T,
	client *mcpsdk.ClientSession,
	name string,
	arguments map[string]any,
	out any,
) {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool %s returned error: %s", name, extractText(t, result))
	}
	body := extractText(t, result)
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode %s result: %v (body %s)", name, err, body)
	}
}

// TestDefinitionRegistryListIsPagedAndCarriesTheSourceDigest covers ADR 0003
// §4.4 and §5 of the acceptance criteria: registry diagnostics are queryable
// and payload-bounded.
//
// The bound is not cosmetic. Eighteen definitions carry roughly 90 KB of schema
// between them, and tangent.hitl-item's request bundle alone is 38 KB; a
// listing that inlined schemas would be a diagnostic nobody runs twice.
func TestDefinitionRegistryListIsPagedAndCarriesTheSourceDigest(t *testing.T) {
	client, done := definitionToolsClient(t)
	defer done()

	var page struct {
		Definitions []struct {
			Kind           string `json:"kind"`
			PackageID      string `json:"package_id"`
			State          string `json:"materialization_state"`
			Available      bool   `json:"available"`
			ManifestDigest string `json:"manifest_digest"`
			ContractDigest string `json:"contract_digest"`
		} `json:"definitions"`
		Total        int    `json:"total"`
		NextCursor   string `json:"next_cursor"`
		SourceDigest string `json:"definition_source_digest"`
		HostVersion  string `json:"host_version"`
	}
	callDefinitionTool(t, client, "tangent.definition_registry_list", map[string]any{"limit": 5}, &page)

	if page.Total != len(extensions.RegisteredTypes()) {
		t.Fatalf("total = %d, want %d", page.Total, len(extensions.RegisteredTypes()))
	}
	if len(page.Definitions) != 5 {
		t.Fatalf("page carried %d definitions, want the requested 5", len(page.Definitions))
	}
	if page.NextCursor == "" {
		t.Fatal("a truncated listing reported no cursor, so the rest is unreachable")
	}
	if page.SourceDigest == "" || page.HostVersion == "" {
		t.Fatalf("listing carries no source digest or host version: %+v", page)
	}
	for _, entry := range page.Definitions {
		if entry.ManifestDigest == "" || entry.ContractDigest == "" {
			t.Errorf("%s is listed without its digests", entry.Kind)
		}
		if !entry.Available || entry.State != "available" {
			t.Errorf("%s is listed as %q/%v", entry.Kind, entry.State, entry.Available)
		}
	}

	// The cursor resumes rather than repeating.
	var second struct {
		Definitions []struct {
			Kind string `json:"kind"`
		} `json:"definitions"`
	}
	callDefinitionTool(t, client, "tangent.definition_registry_list",
		map[string]any{"limit": 5, "cursor": page.NextCursor}, &second)
	if len(second.Definitions) == 0 {
		t.Fatal("the cursor returned an empty page")
	}
	if second.Definitions[0].Kind == page.Definitions[0].Kind {
		t.Fatal("the cursor restarted the listing instead of resuming it")
	}

	// Filtering by package is what makes the ADR 0003 §5 ownership boundary
	// inspectable at runtime rather than only in the source tree.
	var review struct {
		Definitions []struct {
			PackageID string `json:"package_id"`
		} `json:"definitions"`
		Total int `json:"total"`
	}
	callDefinitionTool(t, client, "tangent.definition_registry_list",
		map[string]any{"package_id": "tangent.review"}, &review)
	if review.Total != 4 {
		t.Fatalf("tangent.review holds %d definitions, want 4", review.Total)
	}
	for _, entry := range review.Definitions {
		if entry.PackageID != "tangent.review" {
			t.Errorf("filter leaked %q", entry.PackageID)
		}
	}
}

// TestDefinitionGetProjectsTheWholeManifestAndBoundsSchemas covers acceptance
// criterion 2 — manifest identity covers schemas, renderer, capabilities,
// persistence, trust, compatibility, and source digest — from the outside, over
// the wire, which is where a caller actually has to be able to see it.
func TestDefinitionGetProjectsTheWholeManifestAndBoundsSchemas(t *testing.T) {
	client, done := definitionToolsClient(t)
	defer done()

	var detail struct {
		Kind                        string            `json:"kind"`
		Revision                    int64             `json:"revision"`
		PackageID                   string            `json:"package_id"`
		OwnershipClass              string            `json:"ownership_class"`
		RendererID                  string            `json:"renderer_id"`
		RendererClass               string            `json:"renderer_class"`
		RendererIsolation           string            `json:"renderer_isolation"`
		RendererEntry               string            `json:"renderer_entry"`
		TrustAssurance              string            `json:"trust_assurance"`
		TrustSourceLocator          string            `json:"trust_source_locator"`
		DraftCustody                string            `json:"draft_custody"`
		RetentionClassAuthored      bool              `json:"retention_class_authored"`
		SensitivityDefault          string            `json:"sensitivity_default"`
		ClientPersistenceProhibited bool              `json:"client_persistence_prohibited"`
		CompatibleHostVersions      string            `json:"compatible_host_versions"`
		CompatibilityResponseSchema string            `json:"compatibility_response_schema"`
		NamedDefinitions            map[string]string `json:"named_definitions"`
		Required                    []any             `json:"required_renderer_effect_capabilities"`
		Granted                     []any             `json:"granted_renderer_effect_capabilities"`
		ManifestDigest              string            `json:"manifest_digest"`
		ContractDigest              string            `json:"contract_digest"`
		SchemaIdentity              string            `json:"request_schema_identity"`
		RequestSchema               struct {
			Present       bool            `json:"present"`
			Digest        string          `json:"digest"`
			Bytes         int             `json:"bytes"`
			Body          json.RawMessage `json:"body"`
			OmittedReason string          `json:"omitted_reason"`
		} `json:"request_schema"`
		ResponseSchema struct {
			Present bool            `json:"present"`
			Body    json.RawMessage `json:"body"`
		} `json:"response_schema"`
	}
	callDefinitionTool(t, client, "tangent.definition_get", map[string]any{
		"kind": extensions.HITLItemEnvelopeType, "include_schemas": true,
	}, &detail)

	if detail.Kind != extensions.HITLItemEnvelopeType || detail.Revision != 1 {
		t.Fatalf("identity = %s rev %d", detail.Kind, detail.Revision)
	}
	if detail.PackageID != extensions.HITLPackageID || detail.OwnershipClass != "host-package" {
		t.Errorf("ownership = %s/%s", detail.PackageID, detail.OwnershipClass)
	}
	if detail.RendererID == "" || detail.RendererClass == "" || detail.RendererIsolation == "" ||
		detail.RendererEntry == "" {
		t.Errorf("renderer binding is incomplete: %+v", detail)
	}
	if detail.TrustAssurance == "" || detail.TrustSourceLocator == "" {
		t.Errorf("trust evidence is incomplete: %q %q", detail.TrustAssurance, detail.TrustSourceLocator)
	}
	if detail.DraftCustody == "" || detail.SensitivityDefault == "" || detail.CompatibleHostVersions == "" {
		t.Errorf("persistence or compatibility is incomplete: %+v", detail)
	}
	// Every shipped kind leaves retention_class unauthored so ADR 0002 §4's
	// host default governs. Reporting the fact — rather than reporting a value
	// Tangent invented — is what lets an operator see that.
	if detail.RetentionClassAuthored {
		t.Error("a shipped kind reports an authored retention_class")
	}
	if detail.ManifestDigest == "" || detail.ContractDigest == "" || detail.SchemaIdentity == "" {
		t.Errorf("derived identity is incomplete: %+v", detail)
	}
	if len(detail.NamedDefinitions) == 0 {
		t.Error("the reference package reports no named definitions")
	}
	if detail.Required == nil || detail.Granted == nil {
		t.Error("capability sets must be present and empty, not absent")
	}

	// The 38 KB request bundle is above the inline ceiling: reported, not
	// streamed, and the reason is explicit so "no schema" and "too big" never
	// look alike.
	if !detail.RequestSchema.Present || detail.RequestSchema.Digest == "" || detail.RequestSchema.Bytes == 0 {
		t.Fatalf("request schema is not described: %+v", detail.RequestSchema)
	}
	if len(detail.RequestSchema.Body) != 0 {
		t.Errorf("a %d-byte schema was inlined past the ceiling", detail.RequestSchema.Bytes)
	}
	if detail.RequestSchema.OmittedReason == "" {
		t.Error("a withheld schema body gave no reason")
	}
	// The response projection is small enough to inline, and it exists at all
	// only because tangent.hitl-item is the one kind ADR 0003 §9 S2 does not
	// leave for the CW-20260825-0074 backfill.
	if detail.CompatibilityResponseSchema != "present" || !detail.ResponseSchema.Present {
		t.Fatalf("response schema = %q/%v", detail.CompatibilityResponseSchema, detail.ResponseSchema.Present)
	}
	if len(detail.ResponseSchema.Body) == 0 {
		t.Error("the response schema fits under the ceiling but was not returned")
	}
}

// TestDefinitionGetWithholdsSchemasByDefault keeps the bound the default rather
// than an option a caller has to know to ask for.
func TestDefinitionGetWithholdsSchemasByDefault(t *testing.T) {
	client, done := definitionToolsClient(t)
	defer done()

	var detail struct {
		RequestSchema struct {
			Present       bool            `json:"present"`
			Body          json.RawMessage `json:"body"`
			OmittedReason string          `json:"omitted_reason"`
		} `json:"request_schema"`
		ResponseSchema struct {
			Present       bool   `json:"present"`
			OmittedReason string `json:"omitted_reason"`
		} `json:"response_schema"`
	}
	callDefinitionTool(t, client, "tangent.definition_get",
		map[string]any{"kind": extensions.TriageEnvelopeType}, &detail)

	if !detail.RequestSchema.Present || len(detail.RequestSchema.Body) != 0 {
		t.Fatalf("request schema body was returned without include_schemas: %+v", detail.RequestSchema)
	}
	// tangent.triage declares compatibility_response_schema: absent, so its
	// response schema is genuinely missing rather than merely withheld, and the
	// projection says which.
	if detail.ResponseSchema.Present {
		t.Error("a kind marked absent reports a present response schema")
	}
	if detail.ResponseSchema.OmittedReason == "" {
		t.Error("an absent response schema gave no reason")
	}
}

// TestDefinitionRegistryDiagnosticsReportsStateAndCoverage is the integration
// point CW-20260825-0066 extends with readiness and capability health. It
// answers the two questions an operator has before anything else: is anything
// not being served, and does the pin in the record store still have material to
// resolve against.
func TestDefinitionRegistryDiagnosticsReportsStateAndCoverage(t *testing.T) {
	client, done := definitionToolsClient(t)
	defer done()

	var diagnostics struct {
		HostVersion         string         `json:"host_version"`
		ProtocolVersion     string         `json:"protocol_version"`
		SourceDigest        string         `json:"definition_source_digest"`
		RegisteredKinds     int            `json:"registered_kinds"`
		ManagedDefinitions  int            `json:"managed_definitions"`
		StateCounts         map[string]int `json:"materialization_state_counts"`
		Unservable          []any          `json:"unservable"`
		RetainedDefinitions int64          `json:"retained_definitions"`
		PackageCounts       map[string]int `json:"package_counts"`
		CapabilityRequests  []string       `json:"renderer_effect_capability_requests"`
	}
	callDefinitionTool(t, client, "tangent.definition_registry_diagnostics", map[string]any{}, &diagnostics)

	managed := len(extensions.RegisteredTypes())
	if diagnostics.ManagedDefinitions != managed {
		t.Fatalf("managed definitions = %d, want %d", diagnostics.ManagedDefinitions, managed)
	}
	// 26 go-envelopes core kinds carry no manifest, so registered strictly
	// exceeds managed. Collapsing the two would hide that core definitions
	// cannot be bound at all until upstream retains source bytes.
	if diagnostics.RegisteredKinds <= diagnostics.ManagedDefinitions {
		t.Errorf("registered = %d, managed = %d; core kinds are unaccounted for",
			diagnostics.RegisteredKinds, diagnostics.ManagedDefinitions)
	}
	if diagnostics.StateCounts["available"] != managed {
		t.Errorf("available = %d, want %d (%v)",
			diagnostics.StateCounts["available"], managed, diagnostics.StateCounts)
	}
	if len(diagnostics.Unservable) != 0 {
		t.Errorf("shipped definitions are unservable: %v", diagnostics.Unservable)
	}
	if diagnostics.SourceDigest == "" || diagnostics.HostVersion == "" || diagnostics.ProtocolVersion == "" {
		t.Errorf("diagnostics omit the identity a client compares against: %+v", diagnostics)
	}
	// No material store is installed on this server, which is a different
	// answer from "installed and empty".
	if diagnostics.RetainedDefinitions != -1 {
		t.Errorf("retained definitions = %d, want -1 without a material store",
			diagnostics.RetainedDefinitions)
	}
	// v0.x grants no host-mediated effect capabilities, and no shipped
	// definition requests one. CW-20260825-0077 is what changes this line;
	// until then a non-empty list would mean something is quarantined.
	if len(diagnostics.CapabilityRequests) != 0 {
		t.Errorf("shipped definitions request effect capabilities: %v", diagnostics.CapabilityRequests)
	}
	if diagnostics.PackageCounts["tangent.generic-candidate"] != 6 {
		t.Errorf("generic-catalog destinations = %d, want the ADR 0003 §6 count of 6",
			diagnostics.PackageCounts["tangent.generic-candidate"])
	}
}

// TestHostVersionHasOneSource keeps the alias in server.go honest. The version
// definitions declare compatibility against and the version the MCP server
// advertises are the same release; declaring them separately would let a
// release bump one and silently make every manifest's host range wrong.
func TestHostVersionHasOneSource(t *testing.T) {
	if tangentmcp.HostVersion != envelope.HostVersion {
		t.Fatalf("mcp.HostVersion = %q, envelope.HostVersion = %q",
			tangentmcp.HostVersion, envelope.HostVersion)
	}
}
