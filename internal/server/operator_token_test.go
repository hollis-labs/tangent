package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/server"
)

// CW-20261016-0174: a session minted from loopback admission must not hold
// operator authority, and the only way to obtain it is the operator token.
//
// The token below is a fixture. It is long enough to be accepted and
// distinctive enough that a test can sweep logs and rows for it.
const testOperatorToken = "fixture-operator-token-7f3c9a1e5b2d4f60-a8e1c3b5d7f9"

// elevate posts a token to the elevation endpoint with a browser's
// same-origin headers and returns the status, the decoded body, and the
// rotated session cookie when one was issued.
func (app *guardedApp) elevate(t *testing.T, cookie *http.Cookie, token string) (int, map[string]any, *http.Cookie) {
	t.Helper()
	status, body, response := app.postJSON(t, server.ElevatePath, cookie, map[string]any{"token": token}, true)
	var rotated *http.Cookie
	for _, set := range response.Cookies() {
		if set.Name == participant.CookieName && set.Value != "" {
			rotated = set
		}
	}
	return status, body, rotated
}

// openElevatedDocument mints a session the way a browser does and elevates it
// with the fixture token.
func (app *guardedApp) openElevatedDocument(t *testing.T, path string) *http.Cookie {
	t.Helper()
	minted := app.openDocument(t, path)
	if minted == nil {
		t.Fatal("no session minted")
	}
	status, body, rotated := app.elevate(t, minted, testOperatorToken)
	if status != http.StatusOK || rotated == nil {
		t.Fatalf("elevate = %d %v (rotated cookie %v)", status, body, rotated != nil)
	}
	return rotated
}

// postJSON sends a JSON POST. browser adds the same-origin headers a real
// browser fetch carries; without it the request looks like a script's.
func (app *guardedApp) postJSON(
	t *testing.T, path string, cookie *http.Cookie, payload any, browser bool,
) (int, map[string]any, *http.Response) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, app.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if browser {
		request.Header.Set("Origin", app.baseURL)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body := map[string]any{}
	data, _ := io.ReadAll(response.Body)
	_ = json.Unmarshal(data, &body)
	return response.StatusCode, body, response
}

func (app *guardedApp) getJSON(t *testing.T, path string, cookie *http.Cookie, target any) int {
	t.Helper()
	response := app.do(t, http.MethodGet, path, cookie)
	defer func() { _ = response.Body.Close() }()
	if target != nil && response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return response.StatusCode
}

// enqueueApproval enqueues an approval over MCP exactly as an agent does.
func (app *guardedApp) enqueueApproval(t *testing.T, key string) hitl.ItemHandle {
	t.Helper()
	call := mustJoinedMCPCall(t, app.baseURL, "tangent_hitl_enqueue",
		joinedApprovalRequestFor("codex", "release-worker", key, "Approve "+key))
	var handle hitl.ItemHandle
	mustDecodeJoinedCall(t, call, &handle)
	return handle
}

type itemProbe struct {
	State                       string `json:"state"`
	Revision                    int64  `json:"revision"`
	PresentedProjectionRevision int64  `json:"presented_projection_revision"`
}

func (app *guardedApp) item(t *testing.T, cookie *http.Cookie, itemID string) itemProbe {
	t.Helper()
	var probe itemProbe
	if status := app.getJSON(t, "/api/hitl/items/"+itemID, cookie, &probe); status != http.StatusOK {
		t.Fatalf("GET item = %d", status)
	}
	return probe
}

func presentBody(revision int64) map[string]any {
	return map[string]any{
		"expected_revision": revision, "presented_projection_revision": revision, "connection_id": "test",
	}
}

func resolveBody(probe itemProbe, decision string) map[string]any {
	return map[string]any{
		"expected_revision":             probe.Revision,
		"presented_projection_revision": probe.PresentedProjectionRevision,
		"response":                      map[string]any{"kind": "approval", "decision": decision},
	}
}

func assertCode(t *testing.T, label string, status int, body map[string]any, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus || body["code"] != wantCode {
		t.Fatalf("%s = %d %v, want %d %s", label, status, body, wantStatus, wantCode)
	}
}

func TestMintedLoopbackSessionCannotPresentOrResolve(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "minted-refused")
	minted := app.openDocument(t, "/")

	// The minted session still loads the inbox and the item.
	if status := app.getJSON(t, "/api/hitl", minted, nil); status != http.StatusOK {
		t.Fatalf("GET /api/hitl with a minted session = %d", status)
	}
	probe := app.item(t, minted, handle.ItemID)

	status, body, _ := app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/present", minted, presentBody(probe.Revision), true)
	assertCode(t, "present with a minted session", status, body, http.StatusForbidden, "assurance_insufficient")
	status, body, _ = app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/resolve", minted,
		resolveBody(itemProbe{Revision: probe.Revision, PresentedProjectionRevision: probe.Revision}, "approved"), true)
	assertCode(t, "resolve with a minted session", status, body, http.StatusForbidden, "assurance_insufficient")

	if after := app.item(t, minted, handle.ItemID); after.State != "staged" || after.Revision != probe.Revision {
		t.Fatalf("a refused session changed the item: %+v -> %+v", probe, after)
	}

	var session map[string]any
	if status := app.getJSON(t, server.SessionStatusPath, minted, &session); status != http.StatusOK ||
		session["elevated"] != false || session["assurance"] != participant.AssuranceLoopbackUnverified ||
		session["operator_token_configured"] != true {
		t.Fatalf("session status = %d %v", status, session)
	}
}

// A session row written before this change lists `resolve` in its grants.
// The assurance check, not the grant set, is what refuses it.
func TestLegacyUnverifiedSessionWithResolveGrantIsStillRefused(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "legacy-row")
	legacy := participant.DefaultTemplate
	legacy.Grants = participant.OperatorTemplate.Grants
	_, value, err := app.sessions.Mint(t.Context(), legacy)
	if err != nil {
		t.Fatalf("mint legacy row: %v", err)
	}
	cookie := &http.Cookie{Name: participant.CookieName, Value: value}
	probe := app.item(t, cookie, handle.ItemID)
	status, body, _ := app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/present", cookie, presentBody(probe.Revision), true)
	assertCode(t, "present with a legacy unverified session", status, body, http.StatusForbidden, "assurance_insufficient")
}

func TestElevationRejectsWrongTokenThenAcceptsAndRotates(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	minted := app.openDocument(t, "/")

	status, body, rotated := app.elevate(t, minted, "wrong-"+testOperatorToken)
	assertCode(t, "elevate with a wrong token", status, body, http.StatusForbidden, "elevation_refused")
	if rotated != nil {
		t.Fatal("a refused elevation issued a cookie")
	}
	// No session at all: refused before any token is examined.
	status, body, _ = app.elevate(t, nil, testOperatorToken)
	assertCode(t, "elevate without a session", status, body, http.StatusForbidden, "forbidden")

	status, body, rotated = app.elevate(t, minted, testOperatorToken)
	if status != http.StatusOK || body["elevated"] != true || body["assurance"] != participant.AssuranceOperatorToken {
		t.Fatalf("elevate with the right token = %d %v", status, body)
	}
	if rotated == nil || rotated.Value == minted.Value || !rotated.HttpOnly {
		t.Fatal("elevation did not rotate to a fresh HttpOnly session cookie")
	}

	// The predecessor is dead; the successor is elevated.
	if status := app.getJSON(t, "/api/hitl", minted, nil); status != http.StatusForbidden {
		t.Fatalf("the pre-elevation cookie still works: %d", status)
	}
	var session map[string]any
	if status := app.getJSON(t, server.SessionStatusPath, rotated, &session); status != http.StatusOK || session["elevated"] != true {
		t.Fatalf("session status after elevation = %d %v", status, session)
	}
	var reason string
	if err := app.database.QueryRow(
		`SELECT revoked_reason FROM participant_sessions WHERE rotated_to IS NOT NULL`).Scan(&reason); err != nil || reason != "assurance-change" {
		t.Fatalf("predecessor revocation = %q, %v", reason, err)
	}
}

func TestElevatedSessionResolvesAndRecordsOperatorToken(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "elevated-resolve")
	cookie := app.openElevatedDocument(t, "/")

	probe := app.item(t, cookie, handle.ItemID)
	status, body, _ := app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/present", cookie, presentBody(probe.Revision), true)
	if status != http.StatusOK {
		t.Fatalf("present with an elevated session = %d %v", status, body)
	}
	presented := app.item(t, cookie, handle.ItemID)
	status, body, _ = app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/resolve", cookie, resolveBody(presented, "approved"), true)
	if status != http.StatusOK || body["state"] != "resolved" {
		t.Fatalf("resolve with an elevated session = %d %v", status, body)
	}
	resolution, _ := body["resolution"].(map[string]any)
	captured, _ := resolution["participant"].(map[string]any)
	if captured["assurance"] != participant.AssuranceOperatorToken {
		t.Fatalf("resolution participant = %v, want operator-token assurance", captured)
	}

	var resolutionAssurance, interactionAssurance string
	if err := app.database.QueryRow(`SELECT participant_assurance FROM resolutions WHERE interaction_id = ?`,
		handle.ItemID).Scan(&resolutionAssurance); err != nil {
		t.Fatalf("read resolution row: %v", err)
	}
	if err := app.database.QueryRow(`SELECT participant_assurance FROM interactions WHERE id = ?`,
		handle.ItemID).Scan(&interactionAssurance); err != nil {
		t.Fatalf("read interaction row: %v", err)
	}
	if resolutionAssurance != participant.AssuranceOperatorToken || interactionAssurance != participant.AssuranceOperatorToken {
		t.Fatalf("recorded assurance = resolution %q, interaction %q; want operator-token",
			resolutionAssurance, interactionAssurance)
	}
}

func TestUnsetOrShortOperatorTokenFailsClosed(t *testing.T) {
	t.Parallel()
	for _, configured := range []string{"", "short-token", strings.Repeat("x", participant.MinOperatorTokenBytes-1)} {
		t.Run(fmt.Sprintf("len=%d", len(configured)), func(t *testing.T) {
			t.Parallel()
			app := startGuardedAppWith(t, func(cfg *server.Config) {
				cfg.Participants.ConfigureOperatorToken(configured, nil)
			})
			handle := app.enqueueApproval(t, "fail-closed")
			minted := app.openDocument(t, "/")
			// The configured value, the old fixture, and the empty string
			// are all refused: there is no token to match.
			for _, candidate := range []string{configured, testOperatorToken, ""} {
				status, body, rotated := app.elevate(t, minted, candidate)
				assertCode(t, "elevate with no usable token configured", status, body, http.StatusForbidden, "elevation_refused")
				if rotated != nil {
					t.Fatal("a fail-closed elevation issued a cookie")
				}
			}
			var session map[string]any
			if status := app.getJSON(t, server.SessionStatusPath, minted, &session); status != http.StatusOK ||
				session["operator_token_configured"] != false {
				t.Fatalf("session status = %d %v", status, session)
			}
			// Everything else keeps working.
			if status := app.getJSON(t, "/api/hitl/items/"+handle.ItemID, minted, nil); status != http.StatusOK {
				t.Fatalf("viewing an item with no operator token configured = %d", status)
			}
		})
	}
}

func TestElevationRateLimit(t *testing.T) {
	t.Parallel()
	t.Run("per session", func(t *testing.T) {
		t.Parallel()
		app := startGuardedApp(t)
		minted := app.openDocument(t, "/")
		for attempt := range 5 {
			status, body, _ := app.elevate(t, minted, fmt.Sprintf("guess-%d", attempt))
			assertCode(t, "wrong guess", status, body, http.StatusForbidden, "elevation_refused")
		}
		// Limited: even the right token is not examined.
		status, body, rotated := app.elevate(t, minted, testOperatorToken)
		assertCode(t, "attempt past the per-session limit", status, body, http.StatusTooManyRequests, "elevation_rate_limited")
		if rotated != nil {
			t.Fatal("a rate-limited attempt elevated")
		}
		// Another session is unaffected by this one's failures.
		if other := app.openElevatedDocument(t, "/"); other == nil {
			t.Fatal("a fresh session could not elevate")
		}
	})
	t.Run("flood alerts but never locks the operator out", func(t *testing.T) {
		t.Parallel()
		app := startGuardedApp(t)
		// A local agent minting a fresh session per guess: every guess is
		// refused, the process-wide rate raises one alert, and nobody else is
		// locked out by it.
		var guesses []string
		for attempt := range 25 {
			guess := fmt.Sprintf("flood-guess-%02d", attempt)
			guesses = append(guesses, guess)
			status, body, _ := app.elevate(t, app.openDocument(t, "/"), guess)
			assertCode(t, "flood guess", status, body, http.StatusForbidden, "elevation_refused")
		}
		status, body, rotated := app.elevate(t, app.openDocument(t, "/"), testOperatorToken)
		if status != http.StatusOK || rotated == nil {
			t.Fatalf("the real operator was locked out by a flood: %d %v", status, body)
		}
		logs := app.logs.String()
		if strings.Count(logs, "failure rate above alert threshold") != 1 {
			t.Fatalf("want exactly one rate-limited flood alert line, logs:\n%s", logs)
		}
		for _, secret := range append(guesses, testOperatorToken) {
			if strings.Contains(logs, secret) {
				t.Fatal("a candidate or the token reached the log")
			}
		}
	})
}

func TestOperatorTokenNeverReachesALogLineOrADurableRow(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	wrong := "wrong-candidate-" + testOperatorToken[:20]
	minted := app.openDocument(t, "/")
	if status, _, _ := app.elevate(t, minted, wrong); status != http.StatusForbidden {
		t.Fatalf("wrong token status = %d", status)
	}
	status, body, rotated := app.elevate(t, minted, testOperatorToken)
	if status != http.StatusOK || rotated == nil {
		t.Fatalf("elevate = %d %v", status, body)
	}
	logs := app.logs.String()
	if !strings.Contains(logs, server.ElevatePath) {
		t.Fatal("the request log did not record the elevation requests; the sweep below would prove nothing")
	}
	for _, secret := range []string{testOperatorToken, wrong, rotated.Value, minted.Value} {
		if strings.Contains(logs, secret) {
			t.Fatal("an operator token or session cookie reached the log")
		}
		assertNoDatabaseCell(t, app.database, secret)
	}
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), testOperatorToken) {
		t.Fatal("the elevation response echoed the token")
	}
}

// TestRequesterCannotAnswerItsOwnApproval is the hitltest-style self-approval
// scenario: a script-like client with no browser headers enqueues an approval
// over MCP, mints a session over loopback the way any local process can, and
// tries to present and resolve its own item.
func TestRequesterCannotAnswerItsOwnApproval(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "self-approval")

	// A plain GET asking for HTML: no Sec-Fetch-*, no Origin. This is all an
	// agent's curl needs to be treated as a document navigation.
	request, err := http.NewRequest(http.MethodGet, app.baseURL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/html")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	var minted *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == participant.CookieName {
			minted = cookie
		}
	}
	if minted == nil {
		t.Fatal("the script did not mint a session; the scenario's premise changed")
	}

	probe := app.item(t, minted, handle.ItemID)
	status, body, _ := app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/present", minted, presentBody(probe.Revision), false)
	assertCode(t, "requester present", status, body, http.StatusForbidden, "assurance_insufficient")
	status, body, _ = app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/resolve", minted,
		resolveBody(itemProbe{Revision: probe.Revision, PresentedProjectionRevision: probe.Revision}, "approved"), false)
	assertCode(t, "requester resolve", status, body, http.StatusForbidden, "assurance_insufficient")
	// Guessing at elevation does not help either.
	status, body, _ = app.postJSON(t, server.ElevatePath, minted, map[string]any{"token": "guess"}, false)
	assertCode(t, "requester elevate", status, body, http.StatusForbidden, "elevation_refused")

	// The caller still sees its item pending: nothing was decided.
	get := mustJoinedMCPCall(t, app.baseURL, "tangent_hitl_get", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID,
		"caller": map[string]any{"application_id": "codex", "principal_ref": "release-worker"},
	})
	var outcome hitl.RetrievalResult
	mustDecodeJoinedCall(t, get, &outcome)
	if outcome.Item.State != "staged" || outcome.Item.TerminalOutcome != nil {
		t.Fatalf("the requester's item is %q after a refused self-approval, want staged", outcome.Item.State)
	}
}

// TestElevatedSessionResolvesAnItemPresentedBeforeTheUpgrade covers the live
// pilot's backlog: items presented by the loopback-unverified operator before
// elevation existed. An operator-token session takes them over by assurance
// order, and the resolution records operator-token.
func TestElevatedSessionResolvesAnItemPresentedBeforeTheUpgrade(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "pre-upgrade")
	// Present it the way the pre-upgrade browser path did: as the historical
	// loopback-unverified OperatorParticipant.
	if _, err := app.hitl.Present(t.Context(), hitl.PresentInput{
		ItemID: handle.ItemID, ExpectedRevision: handle.Revision,
		PresentedProjectionRevision: handle.Revision, ConnectionID: "legacy",
	}); err != nil {
		t.Fatalf("legacy present: %v", err)
	}
	cookie := app.openElevatedDocument(t, "/")
	presented := app.item(t, cookie, handle.ItemID)
	status, body, _ := app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/resolve", cookie, resolveBody(presented, "denied"), true)
	if status != http.StatusOK || body["state"] != "resolved" {
		t.Fatalf("resolve a pre-upgrade item with an elevated session = %d %v", status, body)
	}
	var assurance string
	var superseded int
	if err := app.database.QueryRow(`SELECT participant_assurance FROM resolutions WHERE interaction_id = ?`,
		handle.ItemID).Scan(&assurance); err != nil || assurance != participant.AssuranceOperatorToken {
		t.Fatalf("resolution assurance = %q, %v", assurance, err)
	}
	if err := app.database.QueryRow(`SELECT COUNT(*) FROM interaction_events WHERE interaction_id = ? AND event_type = 'interaction.presenter_superseded'`,
		handle.ItemID).Scan(&superseded); err != nil || superseded != 1 {
		t.Fatalf("presenter_superseded events = %d, %v", superseded, err)
	}
	// A retry is a terminal conflict, not a second resolution.
	status, body, _ = app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/resolve", cookie, resolveBody(presented, "approved"), true)
	if status != http.StatusConflict {
		t.Fatalf("retry after takeover = %d %v, want 409", status, body)
	}
}
