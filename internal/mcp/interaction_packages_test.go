package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// formCollectFixture is the shipped tangent.form-collect round trip, taken from
// the same table every other completion test runs against so the packaged kind
// cannot acquire a private fixture that flatters it.
func formCollectFixture(t *testing.T) workflowFixture {
	t.Helper()
	for _, fixture := range shippedRoomWorkflows() {
		if fixture.tool == "tangent.form-collect" {
			return fixture
		}
	}
	t.Fatal("tangent.form-collect is not in the shipped workflow table")
	return workflowFixture{}
}

// prePackageFormCollectResponse is the exact tool result tangent.form-collect
// produced at e122b23, when its response was assembled by
// internal/mcp/form_collect_handler.go.
//
// This is acceptance 3's proof, and it is a literal rather than a comparison
// against a second run of the current code on purpose: a refactor that changed
// the payload in both topologies at once would pass a self-comparison and fail
// here. `completedAt` is the one normalized field, because Tangent stamps it
// at submission time and two runs legitimately differ.
const prePackageFormCollectResponse = `{
  "completedAt": "<normalized>",
  "envelopeId": "parity-1",
  "kind": "data",
  "payload": {
    "action_id": "submit",
    "answers": { "scope": "everything" },
    "attachment_refs": [],
    "form_id": "form-1",
    "notes": "looks right"
  },
  "status": "submitted",
  "v": 1
}`

func TestPackagedWorkflowResponseIsWireIdenticalToPrePackage(t *testing.T) {
	fixture := formCollectFixture(t)
	got := runFixtureToCompletion(t, newDurableRig(t), fixture, nil)

	var want any
	if err := json.Unmarshal([]byte(prePackageFormCollectResponse), &want); err != nil {
		t.Fatalf("unmarshal pinned response: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		gotRaw, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("packaged form-collect response diverged from the pre-package wire shape\n"+
			"want:\n%s\ngot:\n%s", prePackageFormCollectResponse, gotRaw)
	}
}

// TestSessionGetProjectsPackagedStateAtTheTopLevel is the rendering half of
// acceptance 4 on the host side: the SPA and every v0.12 client read this
// projection to hydrate a form, and it now comes from the package rather than
// from a core struct field.
func TestSessionGetProjectsPackagedStateAtTheTopLevel(t *testing.T) {
	fixture := formCollectFixture(t)
	rg := newDurableRig(t)
	defer rg.cleanup()

	roomID := submitFormCollect(t, rg, fixture, "projection-1")

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, getRes))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &decoded); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}

	projection, ok := decoded["form_collect"].(map[string]any)
	if !ok {
		t.Fatalf("session_get form_collect is %T, want an object at the top level", decoded["form_collect"])
	}
	if projection["form_id"] != "form-1" {
		t.Errorf("form_collect.form_id = %v, want form-1", projection["form_id"])
	}
	summary, ok := projection["submission_summary"].(map[string]any)
	if !ok {
		t.Fatalf("form_collect.submission_summary is %T, want the package's derived summary", projection["submission_summary"])
	}
	if summary["export_name"] != "form-1-submission.json" {
		t.Errorf("submission_summary.export_name = %v, want form-1-submission.json", summary["export_name"])
	}
	// The blob the package owns is still visible through the generic phase
	// substrate, unchanged: the package did not get a private store.
	outputs, ok := decoded["phase_outputs"].(map[string]any)
	if !ok {
		t.Fatalf("phase_outputs is %T", decoded["phase_outputs"])
	}
	if _, present := outputs["form-collect"]; !present {
		t.Error("the package's state is not in phase_outputs; it is persisting somewhere core does not see")
	}
}

// TestPackagedKindFailsClosedWithoutItsPackage is the removal half of
// acceptance 4.
//
// A build with the package removed still registers the definition — the
// registry has to be able to list and explain a kind it will not serve (ADR
// 0003 §1, §8 C7) — and the tool refuses with `unsupported-type` rather than
// presenting a form without the room's persisted answers. A degraded form is
// not a degraded decision surface, it is a different one, which is why C5
// forbids the silent fallback.
func TestPackagedKindFailsClosedWithoutItsPackage(t *testing.T) {
	fixture := formCollectFixture(t)
	rg := newSessionRigWith(t, sessionRigOptions{
		durable: true, window: testWindow, withoutPackages: true,
	})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, fixture.tool)

	result := callWorkflow(t, rg, fixture, roomID, "removed-1", nil, nil)
	if result.err != nil {
		t.Fatalf("transport err: %v", result.err)
	}
	if !result.result.IsError {
		t.Fatal("a removed package still served a submission")
	}
	body := extractText(t, result.result)
	if !strings.Contains(body, "unsupported-type") {
		t.Fatalf("removal error = %s, want the upstream unsupported-type code", body)
	}

	// The definition is still registered and still listed. Removing behavior
	// must not remove the host's ability to explain the kind.
	listRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.definition_registry_list", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("definition_registry_list: %v", err)
	}
	if listRes.IsError {
		t.Fatalf("definition_registry_list IsError=true: %s", extractText(t, listRes))
	}
	if !strings.Contains(extractText(t, listRes), "tangent.form-collect") {
		t.Error("removing the package also removed the definition from the registry listing")
	}

	// Every unpackaged kind is untouched: removal is scoped to the package.
	triage := workflowFixture{}
	for _, candidate := range shippedRoomWorkflows() {
		if candidate.tool == "tangent.triage" {
			triage = candidate
		}
	}
	triageResult := callWorkflow(t, rg, triage, roomID, "removed-triage-1",
		map[string]any{"mode": "async"}, nil)
	if triageResult.err != nil {
		t.Fatalf("triage transport err: %v", triageResult.err)
	}
	if triageResult.result.IsError {
		t.Fatalf("removing the form-collect package broke an unrelated kind: %s",
			extractText(t, triageResult.result))
	}
}

// TestPackagedKindFailsClosedWhenDisabled is the disable half: the same
// refusal, without uninstalling anything, which is the operator-facing form of
// definition.HostPolicy.DisabledKinds.
func TestPackagedKindFailsClosedWhenDisabled(t *testing.T) {
	fixture := formCollectFixture(t)
	rg := newSessionRigWith(t, sessionRigOptions{
		durable: true, window: testWindow,
		disabledPackages: []string{"tangent.form-collect"},
	})
	defer rg.cleanup()
	roomID, _ := createSession(t, rg, fixture.tool)

	result := callWorkflow(t, rg, fixture, roomID, "disabled-1", nil, nil)
	if result.err != nil {
		t.Fatalf("transport err: %v", result.err)
	}
	if !result.result.IsError {
		t.Fatal("a disabled package still served a submission")
	}
	if body := extractText(t, result.result); !strings.Contains(body, "unsupported-type") {
		t.Fatalf("disable error = %s, want the upstream unsupported-type code", body)
	}
}

// submitFormCollect drives one form-collect request to a participant
// submission and returns the room it ran in.
func submitFormCollect(
	t *testing.T,
	rg *sessionRig,
	fixture workflowFixture,
	envelopeID string,
) string {
	t.Helper()
	roomID, _ := createSession(t, rg, fixture.tool)
	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	done := make(chan advanceResult, 1)
	go func() { done <- callWorkflow(t, rg, fixture, roomID, envelopeID, nil, nil) }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["envelopeId"] != envelopeID {
		t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], envelopeID)
	}
	response := fixture.participantResponse(envelopeID)
	response["revision"] = frame["revision"]
	writeWSFrame(t, conn, response)

	result := <-done
	if result.err != nil {
		t.Fatalf("transport err: %v", result.err)
	}
	if result.result.IsError {
		t.Fatalf("submit IsError=true: %s", extractText(t, result.result))
	}
	return roomID
}
