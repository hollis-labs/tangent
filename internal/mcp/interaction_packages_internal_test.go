package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/tangent/internal/packages"
)

// prePackageSessionGetOutputSchemaDigest is sha256 over the canonicalized
// tangent.session_get output schema the MCP SDK inferred at e122b23 — before
// tangent.form-collect's projection type moved out of internal/room and into
// its interaction package.
//
// It is the whole wire-compatibility claim for ADR 0003 §8 C6's freeze on
// tangent.session_*, reduced to one value. The SDK derives a tool's output
// schema from its Go return type by reflection, so deleting
// `FormCollect *room.FormStateView` from sessionGetResult silently deleted
// `form_collect` from what tools/list advertises. sessionGetOutputSchema
// composes it back from the package's own contribution.
//
// Canonicalized, not raw: jsonschema.Schema carries a PropertyOrder derived
// from Go struct field order, and a composed property is appended rather than
// slotted where the deleted field used to sit. `form_collect` therefore moves
// from between `approval_queue` and `spreadsheet_review` to the end of the
// object. JSON object member order carries no meaning — to the JSON spec, to
// JSON Schema, or to any conforming MCP client — and every property, type,
// required entry, and additionalProperties value is unchanged, which is what
// canonicalizeSchema compares. Restoring the exact byte order would require
// core to declare where in its own struct a package's property belongs, which
// is the coupling this task exists to remove.
//
// If this digest ever needs changing, the change is a tools/list contract
// change and needs the separate accepted ADR that ADR 0001 §11.7 requires —
// not a regenerated constant.
const prePackageSessionGetOutputSchemaDigest = "sha256:3e08c98facfe770c638b4ca089a0ec07ff0ba05f541e95f51d8ff3d390755f54"

// canonicalizeSchema renders a schema with every object's members in sorted
// order, so the comparison is over what the schema says rather than over the
// order a Go struct happened to declare it in.
func canonicalizeSchema(t *testing.T, schema any) string {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var value any
	if unmarshalErr := json.Unmarshal(raw, &value); unmarshalErr != nil {
		t.Fatalf("unmarshal schema: %v", unmarshalErr)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("canonicalize schema: %v", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestSessionGetOutputSchemaMatchesPrePackageContract(t *testing.T) {
	t.Parallel()
	registry, err := packages.NewRegistry()
	if err != nil {
		t.Fatalf("packages.NewRegistry: %v", err)
	}
	server := &Server{packages: registry}

	schema, err := server.sessionGetOutputSchema()
	if err != nil {
		t.Fatalf("sessionGetOutputSchema: %v", err)
	}
	got := canonicalizeSchema(t, schema)
	if got != prePackageSessionGetOutputSchemaDigest {
		t.Fatalf("composed session_get output schema digest = %s, want the pre-package value %s;\n"+
			"a package's projection schema no longer reproduces the field it replaced, "+
			"which is a tools/list contract change",
			got, prePackageSessionGetOutputSchemaDigest)
	}
}

// TestSessionGetOutputSchemaStillAdvertisesPackagedProjections states the same
// fact in the form a reader debugging a failure wants: the property is present,
// and it came from the package rather than from a core struct field.
func TestSessionGetOutputSchemaStillAdvertisesPackagedProjections(t *testing.T) {
	t.Parallel()
	registry, err := packages.NewRegistry()
	if err != nil {
		t.Fatalf("packages.NewRegistry: %v", err)
	}
	server := &Server{packages: registry}

	withPackages, err := server.sessionGetOutputSchema()
	if err != nil {
		t.Fatalf("sessionGetOutputSchema: %v", err)
	}
	if _, ok := withPackages.Properties["form_collect"]; !ok {
		t.Fatal("composed schema has no form_collect property")
	}

	bare := &Server{}
	withoutPackages, err := bare.sessionGetOutputSchema()
	if err != nil {
		t.Fatalf("sessionGetOutputSchema without packages: %v", err)
	}
	if _, ok := withoutPackages.Properties["form_collect"]; ok {
		t.Fatal("form_collect is still reflected from a core struct field; " +
			"the projection type has not actually left core")
	}
}

// TestSessionGetProjectionSchemaSurvivesDisabling holds the rule that a
// runtime toggle is not a contract change: an operator disabling a kind must
// not reshape what tools/list advertises.
func TestSessionGetProjectionSchemaSurvivesDisabling(t *testing.T) {
	t.Parallel()
	registry, err := packages.NewRegistry()
	if err != nil {
		t.Fatalf("packages.NewRegistry: %v", err)
	}
	registry.SetEnabled("tangent.form-collect", false)
	server := &Server{packages: registry}

	schema, err := server.sessionGetOutputSchema()
	if err != nil {
		t.Fatalf("sessionGetOutputSchema: %v", err)
	}
	if got := canonicalizeSchema(t, schema); got != prePackageSessionGetOutputSchemaDigest {
		t.Fatalf("disabling a kind changed the advertised session_get output schema (%s)", got)
	}
}

// TestSessionGetResultSplicesPackageProjectionsAtTheTopLevel is the marshal
// half: the SPA and every v0.12 client read `form_collect` as a sibling of
// `status`, and nesting it would be a wire break dressed as a refactor.
func TestSessionGetResultSplicesPackageProjectionsAtTheTopLevel(t *testing.T) {
	t.Parallel()
	result := emptySessionGetResult()
	result.packageProjections = map[string]any{
		"form_collect": map[string]any{"form_id": "form-1"},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal session_get result: %v", err)
	}
	var decoded map[string]any
	if unmarshalErr := json.Unmarshal(raw, &decoded); unmarshalErr != nil {
		t.Fatalf("unmarshal session_get result: %v", unmarshalErr)
	}
	projection, ok := decoded["form_collect"].(map[string]any)
	if !ok {
		t.Fatalf("form_collect is %T at the top level, want an object", decoded["form_collect"])
	}
	if projection["form_id"] != "form-1" {
		t.Errorf("form_collect.form_id = %v, want form-1", projection["form_id"])
	}
	if _, leaked := decoded["packageProjections"]; leaked {
		t.Error("the projection carrier leaked into the wire shape")
	}

	// A room with no package state emits nothing, which is what the shipped
	// `omitempty` did.
	bare, err := json.Marshal(emptySessionGetResult())
	if err != nil {
		t.Fatalf("marshal empty session_get result: %v", err)
	}
	var bareDecoded map[string]any
	if err := json.Unmarshal(bare, &bareDecoded); err != nil {
		t.Fatalf("unmarshal empty session_get result: %v", err)
	}
	if _, present := bareDecoded["form_collect"]; present {
		t.Error("form_collect is present for a room with no form state")
	}
}

// TestPackageProjectionCannotShadowACoreField is the collision rule. A package
// declaring ProjectionKey "status" must not be able to overwrite the room's
// own lifecycle field.
func TestPackageProjectionCannotShadowACoreField(t *testing.T) {
	t.Parallel()
	result := emptySessionGetResult()
	result.Status = "active"
	result.packageProjections = map[string]any{"status": "hijacked"}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal session_get result: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal session_get result: %v", err)
	}
	if decoded["status"] != "active" {
		t.Fatalf("status = %v; a package projection overwrote a core field", decoded["status"])
	}
}
