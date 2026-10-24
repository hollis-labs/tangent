package artifacts

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/docs"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/interaction"
)

func newTestService(t *testing.T, opts ...Option) (*Service, *sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	database, err := tangentdb.Open(filepath.Join(root, "artifacts-test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = tangentdb.RunMigrations(database); err != nil {
		t.Fatalf("db.RunMigrations: %v", err)
	}
	envSvc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if err = extensions.RegisterArtifactItem(envSvc); err != nil {
		t.Fatalf("RegisterArtifactItem: %v", err)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envSvc, "artifacts-test-host"),
		interaction.WithSurfaceAccessPolicy(SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	dir := filepath.Join(root, "artifacts")
	svc, err := NewService(interactions, NewStore(database, dir), docs.NewReadStore(database), opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, database, dir
}

func caller(app string) interaction.ActorBinding {
	return interaction.ActorBinding{
		Scope:        "standalone-local:" + app,
		PrincipalRef: app,
		Authority:    "standalone-local",
		Assurance:    "loopback-unverified",
	}
}

func createJSON(key, artifactType string, extra map[string]any) json.RawMessage {
	body := map[string]any{
		"contract_version": "1.0",
		"idempotency_key":  key,
		"source":           map[string]any{"agent_id": "designer", "agent_label": "Designer"},
		"title":            "Landing page v1",
		"summary":          "First pass",
		"type":             artifactType,
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return raw
}

// onePixelPNG is a valid 1x1 PNG.
var onePixelPNG = mustDecode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestCreateHTMLStoresRevisionOnDisk(t *testing.T) {
	svc, _, dir := newTestService(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{
		Request: createJSON("k1", "html", map[string]any{"content": "<h1>Hi</h1>"}),
		Caller:  caller("designer"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.ArtifactRev != 1 || h.State != interaction.InteractionStatePresented {
		t.Fatalf("handle = %+v, want revision 1 presented", h)
	}
	if !strings.HasPrefix(h.InboxPath, "/inbox/items/") {
		t.Fatalf("InboxPath = %q", h.InboxPath)
	}
	rev, file, err := svc.OpenRevision(ctx, h.ItemID, 1)
	if err != nil {
		t.Fatalf("OpenRevision: %v", err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	if string(data) != "<h1>Hi</h1>" || rev.MediaType != "text/html" || rev.Filename != "artifact.html" {
		t.Fatalf("revision = %+v data=%q", rev, data)
	}
	if !strings.HasPrefix(file.Name(), dir) {
		t.Fatalf("file %q is outside the artifact dir %q", file.Name(), dir)
	}
}

func TestIdentityExcludesContent(t *testing.T) {
	svc, db, _ := newTestService(t)
	h, err := svc.Create(context.Background(), CreateInput{
		Request: createJSON("k2", "html", map[string]any{"content": strings.Repeat("x", 4096)}),
		Caller:  caller("designer"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var snapshot string
	if err = db.QueryRow(`SELECT request_snapshot FROM interactions WHERE id = ?`, h.ItemID).Scan(&snapshot); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if strings.Contains(snapshot, "xxxx") || strings.Contains(snapshot, `"content"`) {
		t.Fatalf("request snapshot carries content: %.200s", snapshot)
	}
}

func TestCreateIdempotentReplay(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	req := createJSON("same", "html", map[string]any{"content": "<p>a</p>"})
	a, err := svc.Create(ctx, CreateInput{Request: req, Caller: caller("designer")})
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	b, err := svc.Create(ctx, CreateInput{Request: req, Caller: caller("designer")})
	if err != nil {
		t.Fatalf("replay Create: %v", err)
	}
	if a.ItemID != b.ItemID || b.ArtifactRev != 1 {
		t.Fatalf("replay = %+v, want item %s at revision 1", b, a.ItemID)
	}
}

func TestUpdateAddsRevisionAndCreatorOnly(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{
		Request: createJSON("k3", "html", map[string]any{"content": "<p>v1</p>"}),
		Caller:  caller("designer"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	up, err := svc.Update(ctx, UpdateInput{ItemID: h.ItemID, Caller: caller("designer"),
		Content: ContentFields{Content: "<p>v2</p>", Note: "tightened spacing"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.ArtifactRev != 2 {
		t.Fatalf("ArtifactRev = %d, want 2", up.ArtifactRev)
	}
	view, err := svc.Inspect(ctx, h.ItemID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[1].Note != "tightened spacing" {
		t.Fatalf("view revisions = %+v", view.Revisions)
	}
	if _, err = svc.Update(ctx, UpdateInput{ItemID: h.ItemID, Caller: caller("intruder"),
		Content: ContentFields{Content: "<p>evil</p>"}}); err == nil {
		t.Fatal("a different caller updated someone else's artifact")
	}
}

func TestTypeValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	cases := []struct {
		name  string
		typ   string
		extra map[string]any
		want  string
	}{
		{"image needs base64", "image", map[string]any{"content": "not an image"}, "base64"},
		{"image sniffed wrong", "image", map[string]any{"content_base64": base64.StdEncoding.EncodeToString([]byte("plain text"))}, "sniffed"},
		{"both fields", "html", map[string]any{"content": "a", "content_base64": "YQ=="}, "exactly one"},
		{"neither field", "html", map[string]any{}, "exactly one"},
		{"bad json", "excalidraw", map[string]any{"content": "{nope"}, "valid JSON"},
		{"figma wrong host", "figma", map[string]any{"content": "https://example.com/file/abc"}, "figma.com"},
		{"excalidraw needs elements", "excalidraw", map[string]any{"content": `{"type":"excalidraw"}`}, "elements array"},
		{"flow duplicate id", "flow", map[string]any{"content": `{"nodes":[{"id":"a","label":"A"},{"id":"a","label":"B"}]}`}, "used twice"},
		{"flow dangling edge", "flow", map[string]any{"content": `{"nodes":[{"id":"a","label":"A"}],"edges":[{"source":"a","target":"zzz"}]}`}, "both must be node ids"},
		{"flow bad kind", "flow", map[string]any{"content": `{"nodes":[{"id":"a","label":"A","kind":"banana"}]}`}, "banana"},
		{"vega may not load a url", "vega-lite", map[string]any{"content": `{"data":{"url":"https://x/y.csv"},"mark":"bar"}`}, "inline"},
		{"empty mermaid", "mermaid", map[string]any{"content": "   "}, "empty"},
		{"tldraw is not a type", "tldraw", map[string]any{"content": "{}"}, "unsupported artifact type"},
		{"media mismatch", "html", map[string]any{"content": "<p>x</p>", "media_type": "image/png"}, "does not match"},
	}
	for i, tc := range cases {
		_, err := svc.Create(ctx, CreateInput{Request: createJSON("bad-"+string(rune('a'+i)), tc.typ, tc.extra), Caller: caller("designer")})
		if err == nil || !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want invalid request containing %q", tc.name, err, tc.want)
		}
	}
}

func TestImageRoundTrip(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{
		Request: createJSON("img", "image", map[string]any{"content_base64": base64.StdEncoding.EncodeToString(onePixelPNG), "filename": "../../etc/shot one.png"}),
		Caller:  caller("designer"),
	})
	if err != nil {
		t.Fatalf("Create image: %v", err)
	}
	rev, file, err := svc.OpenRevision(ctx, h.ItemID, 1)
	if err != nil {
		t.Fatalf("OpenRevision: %v", err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	if rev.MediaType != "image/png" || string(data) != string(onePixelPNG) {
		t.Fatalf("revision = %+v", rev)
	}
	if strings.Contains(rev.Filename, "/") || strings.Contains(rev.Filename, "..") || strings.HasPrefix(rev.Filename, ".") {
		t.Fatalf("filename not sanitized: %q", rev.Filename)
	}
}

func TestMessagesSignoffAndAwait(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, CreateInput{
		Request: createJSON("so", "html", map[string]any{"content": "<p>x</p>", "requires_signoff": true}),
		Caller:  caller("designer"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Nothing to read yet: await times out cleanly.
	res, err := svc.Await(ctx, AwaitInput{ItemID: h.ItemID, Caller: caller("designer"), Wait: 600 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Fatalf("Await = %+v, %v; want timeout", res, err)
	}
	if _, err = svc.OperatorMessage(ctx, OperatorMessageInput{ItemID: h.ItemID, Body: "Make the header bigger", Kind: KindChangesRequested}); err != nil {
		t.Fatalf("request changes: %v", err)
	}
	res, err = svc.Await(ctx, AwaitInput{ItemID: h.ItemID, Caller: caller("designer"), Wait: 2 * time.Second})
	if err != nil || res.Reason != "operator_message" || len(res.View.Messages) != 1 || res.View.Messages[0].Kind != KindChangesRequested {
		t.Fatalf("Await = %+v, %v", res, err)
	}
	// Request-changes leaves the artifact open.
	view, _ := svc.Inspect(ctx, h.ItemID)
	if view.State == interaction.InteractionStateResolved {
		t.Fatal("request changes resolved the artifact")
	}
	if _, err = svc.AgentMessage(ctx, MessageInput{ItemID: h.ItemID, Caller: caller("designer"), Body: "Done, see revision 2"}); err != nil {
		t.Fatalf("agent message: %v", err)
	}
	// Messages after the cursor only.
	got, err := svc.Get(ctx, GetInput{ItemID: h.ItemID, Caller: caller("designer"), AfterSeq: 1})
	if err != nil || len(got.Messages) != 1 || got.Messages[0].Author != AuthorAgent {
		t.Fatalf("Get after seq = %+v, %v", got.Messages, err)
	}
	signed, err := svc.Signoff(ctx, SignoffInput{ItemID: h.ItemID, ExpectedRevision: view.Revision, Note: "Ship it"})
	if err != nil {
		t.Fatalf("Signoff: %v", err)
	}
	if signed.State != interaction.InteractionStateResolved || signed.Resolution == nil || signed.Resolution.Action != "approved" {
		t.Fatalf("signed = %+v", signed)
	}
	if _, err = svc.Update(ctx, UpdateInput{ItemID: h.ItemID, Caller: caller("designer"), Content: ContentFields{Content: "<p>late</p>"}}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("Update after sign-off = %v, want terminal conflict", err)
	}
	if _, err = svc.Signoff(ctx, SignoffInput{ItemID: h.ItemID, ExpectedRevision: signed.Revision}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("second Signoff = %v, want terminal conflict", err)
	}
}

func TestInboxArchiveDeleteAndRead(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, CreateInput{Request: createJSON("a", "html", map[string]any{"content": "<p>a</p>"}), Caller: caller("designer")})
	b, _ := svc.Create(ctx, CreateInput{Request: createJSON("b", "markdown", map[string]any{"content": "# b"}), Caller: caller("designer")})
	inbox, err := svc.Inbox(ctx)
	if err != nil || inbox.TotalPending != 2 || inbox.Pending[0].ItemID != a.ItemID {
		t.Fatalf("Inbox = %+v, %v", inbox, err)
	}
	before, _ := svc.Revision(ctx)
	if _, err = svc.MarkRead(ctx, a.ItemID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if after, _ := svc.Revision(ctx); after == before {
		t.Fatal("marking read did not move the inbox revision")
	}
	before, _ = svc.Revision(ctx)
	if _, err = svc.Update(ctx, UpdateInput{ItemID: a.ItemID, Caller: caller("designer"), Content: ContentFields{Content: "<p>a2</p>"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if after, _ := svc.Revision(ctx); after == before {
		t.Fatal("an agent update did not move the inbox revision")
	}
	if _, err = svc.Archive(ctx, ArchiveInput{ItemID: a.ItemID}); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	inbox, _ = svc.Inbox(ctx)
	if inbox.TotalPending != 1 || inbox.TotalTerminal != 1 {
		t.Fatalf("after archive pending=%d terminal=%d", inbox.TotalPending, inbox.TotalTerminal)
	}
	cur, _ := svc.Inspect(ctx, b.ItemID)
	if _, err = svc.Delete(ctx, ArchiveInput{ItemID: b.ItemID, ExpectedRevision: cur.Revision}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	inbox, _ = svc.Inbox(ctx)
	if inbox.TotalPending != 0 {
		t.Fatalf("deleted item still pending: %d", inbox.TotalPending)
	}
}

func TestWithdraw(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	h, _ := svc.Create(ctx, CreateInput{Request: createJSON("w", "html", map[string]any{"content": "<p>w</p>"}), Caller: caller("designer")})
	view, err := svc.Withdraw(ctx, WithdrawInput{ItemID: h.ItemID, Caller: caller("designer")})
	if err != nil || view.State != interaction.InteractionStateCanceled {
		t.Fatalf("Withdraw = %+v, %v", view, err)
	}
}

func TestPathTraversalIsRefused(t *testing.T) {
	svc, db, _ := newTestService(t)
	ctx := context.Background()
	h, _ := svc.Create(ctx, CreateInput{Request: createJSON("p", "html", map[string]any{"content": "<p>p</p>"}), Caller: caller("designer")})
	if _, err := db.Exec(`UPDATE artifact_revisions SET rel_path = '../../../etc/passwd' WHERE interaction_id = ?`, h.ItemID); err != nil {
		t.Fatal(err)
	}
	if _, f, err := svc.OpenRevision(ctx, h.ItemID, 1); err == nil {
		_ = f.Close()
		t.Fatal("a stored path outside the artifact dir was opened")
	}
}

func TestStructuredTypesAccepted(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	good := map[string]string{
		"excalidraw": `{"type":"excalidraw","elements":[{"type":"rectangle","x":0,"y":0,"width":100,"height":50}]}`,
		"flow":       `{"direction":"LR","nodes":[{"id":"a","label":"Start","kind":"trigger"},{"id":"b","label":"Do it"}],"edges":[{"source":"a","target":"b","label":"go"}]}`,
		"mermaid":    "graph TD; A-->B",
		"vega-lite":  `{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"x":{"field":"a"},"y":{"field":"b"}}}`,
		"d2":         "a -> b",
		"markdown":   "# Hi",
		"figma":      "https://www.figma.com/design/abc/Mock",
	}
	for typ, content := range good {
		if _, err := svc.Create(ctx, CreateInput{Request: createJSON("good-"+typ, typ, map[string]any{"content": content}), Caller: caller("designer")}); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}
