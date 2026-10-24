package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/artifacts"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
)

// artifactRig is a Tangent MCP server with the artifact tools wired to a real
// artifacts service on a throwaway database, plus an in-memory client. The
// tools are exercised the way an agent reaches them: arguments in, tool result
// out. The service's own behaviour is covered in internal/artifacts; these tests
// cover what only the tool layer owns: registration, schemas, argument handling
// and the error vocabulary.
type artifactRig struct {
	client *mcpsdk.ClientSession
	svc    *artifacts.Service
}

func newArtifactRig(t *testing.T) *artifactRig {
	t.Helper()
	ctx := context.Background()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "artifact-mcp.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = tangentdb.Close(database) })
	if err = tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	envSvc, err := envelope.New(ctx)
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err = extensions.RegisterArtifactItem(envSvc); err != nil {
		t.Fatalf("RegisterArtifactItem: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envSvc, tangentmcp.HostVersion),
		interaction.WithSurfaceAccessPolicy(artifacts.SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	svc, err := artifacts.NewService(interactions, artifacts.NewStore(database, filepath.Join(t.TempDir(), "files")), docs.NewReadStore(database))
	if err != nil {
		t.Fatalf("artifacts.NewService: %v", err)
	}
	server, err := tangentmcp.New(envSvc, envelope.NewDispatcher(envSvc), room.NewManager(nil), "",
		tangentmcp.WithInteractionService(interactions), tangentmcp.WithArtifactsService(svc))
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	client, closeClient := connectInteractionClient(t, server)
	t.Cleanup(closeClient)
	return &artifactRig{client: client, svc: svc}
}

func artifactSource(agent string) map[string]any {
	return map[string]any{"application_id": "artifact-mcp-test", "agent_id": agent}
}

// otherApplication is a caller in a different application partition. Access is
// partitioned by application_id; agent_id is attribution only (ADR 0004 §1), so
// a different agent in the same application is not a different caller.
func otherApplication() map[string]any {
	return map[string]any{"application_id": "some-other-app", "agent_id": "designer"}
}

func (r *artifactRig) call(t *testing.T, tool string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	t.Helper()
	return r.client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
}

// ok calls a tool that must succeed and decodes its JSON result.
func artifactOK[T any](t *testing.T, r *artifactRig, tool string, args map[string]any) T {
	t.Helper()
	return callInteractionTool[T](t, r.client, tool, args)
}

// refused calls a tool that must be refused and returns the error body's code.
// A refusal can arrive as a tool error result or, for arguments the schema
// rejects before the handler runs, as a protocol error; either is a refusal.
func (r *artifactRig) refused(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	res, err := r.call(t, tool, args)
	if err != nil {
		return "protocol_error"
	}
	if !res.IsError {
		t.Fatalf("%s was accepted, want a refusal: %s", tool, extractText(t, res))
	}
	var body struct {
		Code string `json:"code"`
	}
	if jsonErr := json.Unmarshal([]byte(extractText(t, res)), &body); jsonErr != nil || body.Code == "" {
		return "schema_error"
	}
	return body.Code
}

func (r *artifactRig) create(t *testing.T, key, artifactType string, extra map[string]any) artifacts.Handle {
	t.Helper()
	args := map[string]any{
		"idempotency_key": key,
		"source":          artifactSource("designer"),
		"title":           "Landing page",
		"type":            artifactType,
	}
	for k, v := range extra {
		args[k] = v
	}
	return artifactOK[artifacts.Handle](t, r, "tangent_artifact_create", args)
}

func TestArtifactTools_AreRegisteredWithContractSchemas(t *testing.T) {
	r := newArtifactRig(t)
	tools, err := r.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcpsdk.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"tangent_artifact_create", "tangent_artifact_update", "tangent_artifact_get",
		"tangent_artifact_message", "tangent_artifact_await", "tangent_artifact_withdraw",
	} {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("%s is not registered", name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", name)
		}
		schema, marshalErr := json.Marshal(tool.InputSchema)
		if marshalErr != nil {
			t.Fatalf("%s schema: %v", name, marshalErr)
		}
		var decoded struct {
			Required             []string       `json:"required"`
			Properties           map[string]any `json:"properties"`
			AdditionalProperties any            `json:"additionalProperties"`
		}
		if jsonErr := json.Unmarshal(schema, &decoded); jsonErr != nil {
			t.Fatalf("%s schema is not JSON: %v", name, jsonErr)
		}
		if _, hasSource := decoded.Properties["source"]; !hasSource {
			t.Errorf("%s does not take a source", name)
		}
		if decoded.AdditionalProperties == nil {
			t.Errorf("%s accepts unknown arguments; a typo would be silently ignored", name)
		}
	}

	// The create tool advertises exactly the types this build accepts, and
	// defaults contract_version rather than requiring it.
	var create struct {
		Required   []string `json:"required"`
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	raw, _ := json.Marshal(byName["tangent_artifact_create"].InputSchema)
	if err = json.Unmarshal(raw, &create); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	want := artifacts.SupportedTypes()
	if strings.Join(create.Properties.Type.Enum, ",") != strings.Join(want, ",") {
		t.Errorf("create type enum = %v, want the supported types %v", create.Properties.Type.Enum, want)
	}
	for _, name := range create.Required {
		if name == "contract_version" {
			t.Error("create requires contract_version; it should default")
		}
	}
	if tool := byName["tangent_artifact_get"]; tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Error("tangent_artifact_get is not annotated read-only")
	}
}

// TestArtifactTools_DocumentThemselvesForAgents pins what an agent reads while
// building a call: every create field is described, the type help names every
// type it accepts, and each tool advertises its result shape.
func TestArtifactTools_DocumentThemselvesForAgents(t *testing.T) {
	r := newArtifactRig(t)
	tools, err := r.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcpsdk.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"tangent_artifact_create", "tangent_artifact_update", "tangent_artifact_get",
		"tangent_artifact_message", "tangent_artifact_await", "tangent_artifact_withdraw",
	} {
		if byName[name].OutputSchema == nil {
			t.Errorf("%s advertises no result shape", name)
		}
	}
	var create struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	raw, _ := json.Marshal(byName["tangent_artifact_create"].InputSchema)
	if err = json.Unmarshal(raw, &create); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	for name, property := range create.Properties {
		if strings.TrimSpace(property.Description) == "" {
			t.Errorf("create field %q has no description", name)
		}
	}
	for _, artifactType := range artifacts.SupportedTypes() {
		if !strings.Contains(create.Properties["type"].Description, artifactType+":") {
			t.Errorf("the type help does not describe %q's content", artifactType)
		}
	}
}

func TestArtifactCreate_ReturnsAHandleAndReplaysAnIdempotencyKey(t *testing.T) {
	r := newArtifactRig(t)
	first := r.create(t, "create-1", "html", map[string]any{"content": "<h1>Hello</h1>", "filename": "hello.html"})
	if first.ItemID == "" || first.ArtifactRev != 1 || !strings.HasPrefix(first.InboxPath, "/inbox/items/") {
		t.Fatalf("handle = %+v", first)
	}
	replay := r.create(t, "create-1", "html", map[string]any{"content": "<h1>Hello</h1>", "filename": "hello.html"})
	if replay.ItemID != first.ItemID || replay.ArtifactRev != 1 {
		t.Fatalf("replay = %+v, want item %s at revision 1", replay, first.ItemID)
	}
	// A key names one request: reusing it for a different title is a conflict.
	if code := r.refused(t, "tangent_artifact_create", map[string]any{
		"idempotency_key": "create-1", "source": artifactSource("designer"), "title": "A different title",
		"type": "html", "content": "<h1>Hello</h1>", "filename": "hello.html",
	}); code != "idempotency_conflict" {
		t.Errorf("reusing a key for a different request = %q, want idempotency_conflict", code)
	}
}

func TestArtifactCreate_AcceptsEveryAdvertisedType(t *testing.T) {
	r := newArtifactRig(t)
	pixel := base64.StdEncoding.EncodeToString(mustDecodeBase64(t, "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="))
	content := map[string]map[string]any{
		"html":       {"content": "<p>hi</p>"},
		"markdown":   {"content": "# Hi"},
		"mermaid":    {"content": "graph TD; A-->B"},
		"excalidraw": {"content": `{"type":"excalidraw","elements":[{"type":"rectangle","x":0,"y":0,"width":100,"height":50}]}`},
		"flow":       {"content": `{"direction":"LR","nodes":[{"id":"a","label":"Start"},{"id":"b","label":"Do it"}],"edges":[{"source":"a","target":"b"}]}`},
		"vega-lite":  {"content": `{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"x":{"field":"a"},"y":{"field":"b"}}}`},
		"figma":      {"content": "https://www.figma.com/design/abc/Mock"},
		"image":      {"content_base64": pixel, "filename": "pixel.png"},
		"d2":         {"content": "a -> b"},
	}
	for _, artifactType := range artifacts.SupportedTypes() {
		extra, ok := content[artifactType]
		if !ok {
			// pdf and video need real files; their validation is covered in
			// internal/artifacts. Listing the gap here keeps a new type from
			// being added without a decision about its tool-level test.
			if artifactType == "pdf" || artifactType == "video" {
				continue
			}
			t.Errorf("type %q is advertised but has no tool-level create fixture", artifactType)
			continue
		}
		handle := r.create(t, "type-"+artifactType, artifactType, extra)
		if handle.ItemID == "" {
			t.Errorf("%s: no item id", artifactType)
		}
	}
}

func TestArtifactCreate_RefusesBadInput(t *testing.T) {
	r := newArtifactRig(t)
	base := func(extra map[string]any) map[string]any {
		args := map[string]any{
			"idempotency_key": "bad-" + time.Now().Format("150405.000000"),
			"source":          artifactSource("designer"),
			"title":           "Bad",
			"type":            "html",
			"content":         "<p>x</p>",
		}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	for name, args := range map[string]map[string]any{
		"unknown type":          base(map[string]any{"type": "tldraw"}),
		"no content":            base(map[string]any{"content": nil}),
		"both content kinds":    base(map[string]any{"content_base64": "AAAA"}),
		"unknown argument":      base(map[string]any{"colour": "red"}),
		"missing source":        base(map[string]any{"source": nil}),
		"flow with a bad edge":  base(map[string]any{"type": "flow", "content": `{"nodes":[{"id":"a"}],"edges":[{"source":"a","target":"zzz"}]}`}),
		"vega-lite loading URL": base(map[string]any{"type": "vega-lite", "content": `{"data":{"url":"https://example.test/x.json"},"mark":"bar"}`}),
		"figma on another host": base(map[string]any{"type": "figma", "content": "https://example.test/design/abc"}),
	} {
		for key, value := range args {
			if value == nil {
				delete(args, key)
			}
		}
		if code := r.refused(t, "tangent_artifact_create", args); code == "" {
			t.Errorf("%s: refused without a code", name)
		}
	}
}

func TestArtifactUpdate_AddsARevisionOnlyWithinItsApplication(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "upd-1", "html", map[string]any{"content": "<p>one</p>"})
	updated := artifactOK[artifacts.Handle](t, r, "tangent_artifact_update", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "content": "<p>two</p>", "note": "bigger header",
	})
	if updated.ItemID != item.ItemID || updated.ArtifactRev != 2 {
		t.Fatalf("update = %+v, want item %s at revision 2", updated, item.ItemID)
	}
	view := artifactOK[artifacts.View](t, r, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": item.ItemID})
	if view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[1].Note != "bigger header" {
		t.Fatalf("view after update = %+v", view.Revisions)
	}

	if code := r.refused(t, "tangent_artifact_update", map[string]any{
		"source": otherApplication(), "item_id": item.ItemID, "content": "<p>hijack</p>",
	}); code != "not_found" {
		t.Errorf("update from another application = %q, want not_found", code)
	}
	if code := r.refused(t, "tangent_artifact_update", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "content_base64": "AAAA",
	}); code == "" {
		t.Error("changing the content kind of an html artifact was accepted")
	}
}

func TestArtifactGet_ReadsStateAndMessagesAfterACursor(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "get-1", "markdown", map[string]any{"content": "# Notes", "requires_signoff": true})
	view := artifactOK[artifacts.View](t, r, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": item.ItemID})
	if view.Type != "markdown" || view.Title != "Landing page" || !view.RequiresSignoff || len(view.Messages) != 0 {
		t.Fatalf("view = %+v", view)
	}
	for _, body := range []string{"first", "second"} {
		if _, err := r.svc.OperatorMessage(context.Background(), artifacts.OperatorMessageInput{ItemID: item.ItemID, Body: body, Kind: artifacts.KindMessage}); err != nil {
			t.Fatalf("operator message: %v", err)
		}
	}
	all := artifactOK[artifacts.View](t, r, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": item.ItemID})
	if len(all.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(all.Messages))
	}
	newer := artifactOK[artifacts.View](t, r, "tangent_artifact_get", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "after_seq": all.Messages[0].Seq,
	})
	if len(newer.Messages) != 1 || newer.Messages[0].Body != "second" {
		t.Fatalf("after_seq messages = %+v", newer.Messages)
	}
}

func TestArtifactGet_AnotherApplicationCannotTellAnArtifactExists(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "priv-1", "html", map[string]any{"content": "<p>private</p>"})
	foreign := r.refused(t, "tangent_artifact_get", map[string]any{"source": otherApplication(), "item_id": item.ItemID})
	missing := r.refused(t, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": "does-not-exist"})
	if foreign != "not_found" || missing != "not_found" {
		t.Fatalf("foreign = %q, missing = %q, want both not_found", foreign, missing)
	}
	res, err := r.call(t, "tangent_artifact_get", map[string]any{"source": otherApplication(), "item_id": item.ItemID})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	foreignText := extractText(t, res)
	res, err = r.call(t, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": "does-not-exist"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if foreignText != extractText(t, res) {
		t.Errorf("a foreign artifact and a missing one read differently:\n%s\n%s", foreignText, extractText(t, res))
	}
}

func TestArtifactMessage_AppearsInTheThreadAsTheAgent(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "msg-1", "html", map[string]any{"content": "<p>x</p>"})
	msg := artifactOK[artifacts.Message](t, r, "tangent_artifact_message", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "body": "Revision 2 has the larger header.",
	})
	if msg.Author != artifacts.AuthorAgent || msg.Body != "Revision 2 has the larger header." || msg.Seq < 1 {
		t.Fatalf("message = %+v", msg)
	}
	view := artifactOK[artifacts.View](t, r, "tangent_artifact_get", map[string]any{"source": artifactSource("designer"), "item_id": item.ItemID})
	if len(view.Messages) != 1 || view.Messages[0].Body != msg.Body {
		t.Fatalf("thread = %+v", view.Messages)
	}
	for name, args := range map[string]map[string]any{
		"empty body":          {"source": artifactSource("designer"), "item_id": item.ItemID, "body": ""},
		"another application": {"source": otherApplication(), "item_id": item.ItemID, "body": "hi"},
		"body over the cap":   {"source": artifactSource("designer"), "item_id": item.ItemID, "body": strings.Repeat("x", artifacts.MaxMessageBody+1)},
	} {
		if code := r.refused(t, "tangent_artifact_message", args); code == "" {
			t.Errorf("%s: refused without a code", name)
		}
	}
}

func TestArtifactAwait_TimesOutCleanlyAndWakesOnAnOperatorMessage(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "await-1", "html", map[string]any{"content": "<p>x</p>"})

	quiet := artifactOK[artifacts.AwaitResult](t, r, "tangent_artifact_await", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "wait_ms": 200,
	})
	if !quiet.TimedOut || len(quiet.View.Messages) != 0 {
		t.Fatalf("quiet await = %+v, want a clean timeout", quiet)
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = r.svc.OperatorMessage(context.Background(), artifacts.OperatorMessageInput{
			ItemID: item.ItemID, Body: "Make the header bigger", Kind: artifacts.KindChangesRequested,
		})
	}()
	woken := artifactOK[artifacts.AwaitResult](t, r, "tangent_artifact_await", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "wait_ms": 10000,
	})
	if woken.TimedOut || woken.Reason != "operator_message" || len(woken.View.Messages) != 1 || woken.View.Messages[0].Kind != artifacts.KindChangesRequested {
		t.Fatalf("woken await = %+v", woken)
	}

	if code := r.refused(t, "tangent_artifact_await", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "wait_ms": 120001,
	}); code == "" {
		t.Error("a wait over two minutes was accepted")
	}
}

func TestArtifactWithdraw_MovesItToHistoryAndBlocksFurtherChange(t *testing.T) {
	r := newArtifactRig(t)
	item := r.create(t, "wd-1", "html", map[string]any{"content": "<p>x</p>"})
	if code := r.refused(t, "tangent_artifact_withdraw", map[string]any{"source": otherApplication(), "item_id": item.ItemID}); code != "not_found" {
		t.Errorf("withdraw from another application = %q, want not_found", code)
	}
	view := artifactOK[artifacts.View](t, r, "tangent_artifact_withdraw", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "reason": "superseded",
	})
	if view.State != interaction.InteractionStateCanceled {
		t.Fatalf("state after withdraw = %q, want canceled", view.State)
	}
	if code := r.refused(t, "tangent_artifact_update", map[string]any{
		"source": artifactSource("designer"), "item_id": item.ItemID, "content": "<p>late</p>",
	}); code != "terminal_conflict" {
		t.Errorf("update after withdraw = %q, want terminal_conflict", code)
	}
	if code := r.refused(t, "tangent_artifact_withdraw", map[string]any{"source": artifactSource("designer"), "item_id": item.ItemID}); code != "terminal_conflict" {
		t.Errorf("second withdraw = %q, want terminal_conflict", code)
	}
}

func mustDecodeBase64(t *testing.T, encoded string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return decoded
}
