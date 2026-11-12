package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	libauth "github.com/hollis-labs/libs/security/auth"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/effect"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/server"
	"github.com/hollis-labs/tangent/internal/testdb"
)

// The effect route's transport-level proof.
//
// Everything the broker decides is tested in internal/effect. What is tested
// here is what the *transport* adds and could get wrong: that a request with
// no participant session reaches nothing, that a cross-origin one is refused
// before the model is consulted, that the binding comes from the interaction
// rather than the body, and that no refusal names the thing it refused.

// TestAnEffectRequestNeedsAParticipantSession. An effect is a participant act,
// so it is admitted on exactly the evidence a resolution is.
func TestAnEffectRequestNeedsAParticipantSession(t *testing.T) {
	t.Parallel()
	key := ownerPrincipal()
	key.ID, key.Kind, key.SessionID, key.TokenID = "key:k_1", "service", "", "k_1"
	app := startEffectAppAs(t, key, effectContextStub{
		binding: effect.Binding{
			BindingDigest: "sha256:binding",
			Required:      []effect.Capability{effect.FileReadScoped},
			Granted:       []effect.Capability{effect.FileReadScoped},
		},
	})

	response := app.postEffect(t, nil, map[string]any{
		"capability":      string(effect.FileReadScoped),
		"interaction_id":  "surface-1/interaction-1",
		"idempotency_key": "no-session",
		"intent":          map[string]any{"control_id": "preview"},
	})
	if response.status != http.StatusForbidden {
		t.Fatalf("effect request without a session = %d, want 403", response.status)
	}
	// Nothing was recorded: a request that never reached the model is not a
	// participant act, and an audit table full of unauthenticated shapes is a
	// denial-of-service on whoever reads it.
	if app.receiptCount(t) != 0 {
		t.Fatal("a session-less request wrote a receipt")
	}
}

// TestACrossOriginEffectRequestIsRefused. The origin guard runs before the
// session guard, which runs before the broker.
func TestACrossOriginEffectRequestIsRefused(t *testing.T) {
	t.Parallel()
	app := startEffectApp(t, effectContextStub{})
	cookie := app.openDocument(t)

	request := app.buildEffect(t, cookie, map[string]any{
		"capability":      string(effect.ClipboardWrite),
		"interaction_id":  "surface-1/interaction-1",
		"idempotency_key": "cross-origin",
		"intent":          map[string]any{"control_id": "copy"},
	})
	request.Header.Set("Origin", "http://evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	raw, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("cross-origin effect request: %v", err)
	}
	defer func() { _ = raw.Body.Close() }()
	if raw.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin effect request = %d, want 403", raw.StatusCode)
	}
	if app.receiptCount(t) != 0 {
		t.Fatal("a cross-origin request wrote a receipt")
	}
}

// TestAnUndeclaredCapabilityIsRefusedAndAudited is the shipped state, asserted
// end to end: no definition declares a `required_capability`, so every effect
// request through this route is refused — and the refusal is a durable receipt
// rather than silence.
func TestAnUndeclaredCapabilityIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	app := startEffectApp(t, effectContextStub{
		binding: effect.Binding{BindingDigest: "sha256:binding"},
	})
	cookie := app.openDocument(t)

	response := app.postEffect(t, cookie, map[string]any{
		"capability":      string(effect.ClipboardWrite),
		"interaction_id":  "surface-1/interaction-1",
		"idempotency_key": "undeclared",
		"intent":          map[string]any{"control_id": "copy-summary"},
	})
	if response.status != http.StatusForbidden {
		t.Fatalf("undeclared capability = %d, want 403", response.status)
	}
	if response.receipt.Decision != effect.DecisionRefused ||
		response.receipt.Code != effect.CodeCapabilityUndeclared {
		t.Fatalf("receipt = %s/%s, want refused/%s",
			response.receipt.Decision, response.receipt.Code, effect.CodeCapabilityUndeclared)
	}
	if app.receiptCount(t) != 1 {
		t.Fatalf("receipts = %d, want the refusal recorded", app.receiptCount(t))
	}
}

// TestABrokeredReadCrossesTheTransportIntact walks the whole path with a
// composed authority: mint a handle host-side, request the read over HTTP, and
// get back the content plus a verifiable receipt.
func TestABrokeredReadCrossesTheTransportIntact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("brokered"), 0o600); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	app := startEffectApp(t, effectContextStub{
		binding: effect.Binding{
			BindingDigest: "sha256:binding",
			Required:      []effect.Capability{effect.FileReadScoped},
			Granted:       []effect.Capability{effect.FileReadScoped},
		},
	}, composedAuthority{root: root})
	cookie := app.openDocument(t)

	handle, view, err := app.broker.Mint(context.Background(), effect.Mint{
		Class:            effect.ClassWorkspaceFile,
		RootID:           "ws",
		RelativePath:     "notes.md",
		InteractionID:    "surface-1/interaction-1",
		ParticipantScope: "operator:local",
		BindingDigest:    "sha256:binding",
		Capabilities:     []effect.Capability{effect.FileReadScoped},
		Label:            "notes.md",
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// The renderer-visible projection carries no location.
	encodedView, _ := json.Marshal(view)
	if strings.Contains(string(encodedView), root) || strings.Contains(string(encodedView), "\"ws\"") {
		t.Fatalf("the handle view leaked a location: %s", encodedView)
	}

	response := app.postEffect(t, cookie, map[string]any{
		"capability":      string(effect.FileReadScoped),
		"interaction_id":  "surface-1/interaction-1",
		"handle_id":       handle.ID,
		"idempotency_key": "read-1",
		"intent":          map[string]any{"control_id": "preview", "presented_revision": 3},
	})
	if response.status != http.StatusOK {
		t.Fatalf("brokered read = %d, want 200 (%s)", response.status, response.body)
	}
	if response.receipt.Decision != effect.DecisionGranted {
		t.Fatalf("receipt = %s/%s, want granted", response.receipt.Decision, response.receipt.Code)
	}
	content, err := base64.StdEncoding.DecodeString(response.content)
	if err != nil || string(content) != "brokered" {
		t.Fatalf("content = %q, %v", content, err)
	}
	if response.receipt.ContentSHA256 == "" {
		t.Error("the receipt carries no content digest and is not verifiable")
	}
	// Nothing about where the file lives crossed the wire.
	if strings.Contains(response.body, root) || strings.Contains(response.body, "notes.md") {
		t.Errorf("the effect response leaked a location: %s", response.body)
	}
}

// TestAnEffectRefusalNamesNothing. ADR 0004 §6.5 fixes the text of an
// authorization failure; an effect refusal follows the same rule, because
// "which capability am I missing" is a probe on either axis.
func TestAnEffectRefusalNamesNothing(t *testing.T) {
	t.Parallel()
	app := startEffectApp(t, effectContextStub{err: server.ErrEffectContextUnknown})
	cookie := app.openDocument(t)

	response := app.postEffect(t, cookie, map[string]any{
		"capability":      string(effect.FileReadScoped),
		"interaction_id":  "someone-elses-surface/interaction-9",
		"handle_id":       "eh_00000000-0000-0000-0000-000000000000",
		"idempotency_key": "probe",
		"intent":          map[string]any{"control_id": "probe"},
	})
	if response.status != http.StatusForbidden {
		t.Fatalf("unknown interaction = %d, want 403", response.status)
	}
	for _, needle := range []string{
		"someone-elses-surface", "eh_00000000", "file.read_scoped", "operator:local", "binding",
	} {
		if strings.Contains(response.body, needle) {
			t.Errorf("the refusal named %q: %s", needle, response.body)
		}
	}
}

// ── harness ────────────────────────────────────────────────────────────

type effectContextStub struct {
	binding    effect.Binding
	ownerScope string
	err        error
}

func (s effectContextStub) ResolveEffectContext(
	context.Context, string,
) (effect.Binding, string, error) {
	if s.err != nil {
		return effect.Binding{}, "", s.err
	}
	scope := s.ownerScope
	if scope == "" {
		scope = "standalone-local:tests"
	}
	return s.binding, scope, nil
}

// composedAuthority stands in for a Cerberus Workspace: one registered root
// and one granted capability. It exists to prove the seam carries something,
// because a composition point only ever tested with the standalone answer is
// a composition point that has not been tested.
type composedAuthority struct{ root string }

func (a composedAuthority) WorkspaceRoots(context.Context, string) ([]effect.Root, error) {
	return []effect.Root{{ID: "ws", Label: "Workspace", Path: a.root}}, nil
}
func (composedAuthority) GrantableCapabilities(context.Context) []effect.Capability {
	return []effect.Capability{effect.FileReadScoped}
}
func (composedAuthority) AuthorizesHostPolicy(effect.ActorRef) bool    { return false }
func (composedAuthority) AuthorizesAdministrator(effect.ActorRef) bool { return false }

type effectApp struct {
	database *sql.DB
	broker   *effect.Broker
	baseURL  string
}

func startEffectApp(
	t *testing.T,
	contexts server.EffectContextResolver,
	authorities ...effect.Authority,
) *effectApp {
	t.Helper()
	return startEffectAppAs(t, ownerPrincipal(), contexts, authorities...)
}

// startEffectAppAs serves the route as the given principal: the owner's user
// session, or whatever credential a test wants refused.
func startEffectAppAs(
	t *testing.T,
	principal libauth.Principal,
	contexts server.EffectContextResolver,
	authorities ...effect.Authority,
) *effectApp {
	t.Helper()
	database := testdb.Open(t, filepath.Join(t.TempDir(), "effects.db"))
	store, err := effect.NewSQLStore(database)
	if err != nil {
		t.Fatalf("effect.NewSQLStore: %v", err)
	}
	authority := effect.Standalone()
	if len(authorities) == 1 {
		authority = authorities[0]
	}
	broker, err := effect.NewBroker(store, authority)
	if err != nil {
		t.Fatalf("effect.NewBroker: %v", err)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	baseURL := serveAs(t, server.Config{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Envelope:      envelopeService,
		Effects:       broker,
		EffectContext: contexts,
	}, principal)
	app := &effectApp{database: database, broker: broker, baseURL: baseURL}
	return app
}

// openDocument is what a browser did to get a session; the host's guard does
// that now, so there is nothing to fetch and no cookie to hold.
func (app *effectApp) openDocument(t *testing.T) *http.Cookie {
	t.Helper()
	return nil
}
func (app *effectApp) buildEffect(t *testing.T, cookie *http.Cookie, body map[string]any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode effect command: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, app.baseURL+"/api/effects", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build effect request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

type effectHTTPResult struct {
	status  int
	body    string
	receipt effect.Receipt
	content string
}

func (app *effectApp) postEffect(t *testing.T, cookie *http.Cookie, body map[string]any) effectHTTPResult {
	t.Helper()
	response, err := http.DefaultClient.Do(app.buildEffect(t, cookie, body))
	if err != nil {
		t.Fatalf("effect request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read effect response: %v", err)
	}
	result := effectHTTPResult{status: response.StatusCode, body: string(raw)}
	var decoded struct {
		Receipt effect.Receipt `json:"receipt"`
		Content string         `json:"content"`
	}
	if json.Unmarshal(raw, &decoded) == nil {
		result.receipt, result.content = decoded.Receipt, decoded.Content
	}
	return result
}

func (app *effectApp) receiptCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := app.database.QueryRow(`SELECT COUNT(*) FROM effect_receipts`).Scan(&count); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	return count
}
