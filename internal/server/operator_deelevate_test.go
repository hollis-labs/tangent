package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/participant"
	"github.com/hollis-labs/tangent/internal/server"
)

// CW-20261020-0033: the elevation lifetime is an operator setting, and the
// operator can end an elevation at any time.

type statusProbe struct {
	Assurance          string     `json:"assurance"`
	Elevated           bool       `json:"elevated"`
	ElevationTTL       string     `json:"elevation_ttl"`
	ElevationExpiresAt *time.Time `json:"elevation_expires_at"`
}

func (app *guardedApp) status(t *testing.T, cookie *http.Cookie) statusProbe {
	t.Helper()
	var probe statusProbe
	if status := app.getJSON(t, server.SessionStatusPath, cookie, &probe); status != http.StatusOK {
		t.Fatalf("GET %s = %d", server.SessionStatusPath, status)
	}
	return probe
}

func rotatedCookie(response *http.Response) *http.Cookie {
	for _, set := range response.Cookies() {
		if set.Name == participant.CookieName && set.Value != "" {
			return set
		}
	}
	return nil
}

func TestSessionStatusReportsThePolicyAndWhenElevationEnds(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	minted := app.openDocument(t, "/")
	if probe := app.status(t, minted); probe.Elevated || probe.ElevationTTL != "always" || probe.ElevationExpiresAt != nil {
		t.Fatalf("minted status = %+v; want not elevated, ttl always, no expiry", probe)
	}

	elevated := app.openElevatedDocument(t, "/")
	always := app.status(t, elevated)
	if !always.Elevated || always.ElevationTTL != "always" || always.ElevationExpiresAt == nil {
		t.Fatalf("elevated status under always = %+v", always)
	}
	// 'always' ends with the browser session, which lives 30 days.
	if left := time.Until(*always.ElevationExpiresAt); left < 29*24*time.Hour || left > 30*24*time.Hour+time.Minute {
		t.Fatalf("always: elevation_expires_at is %s away; want the session's ~30 days", left)
	}

	app.sessions.SetElevationPolicy(participant.ElevationNever())
	never := app.status(t, elevated)
	if !never.Elevated || never.ElevationTTL != "never" || never.ElevationExpiresAt == nil {
		t.Fatalf("elevated status under never = %+v", never)
	}
	if left := time.Until(*never.ElevationExpiresAt); left <= 0 || left > participant.NeverElevationWindow+time.Second {
		t.Fatalf("never: elevation_expires_at is %s away; want at most %s", left, participant.NeverElevationWindow)
	}

	app.sessions.SetElevationPolicy(participant.ElevationDays(7))
	if probe := app.status(t, elevated); probe.ElevationTTL != "7d" || probe.ElevationExpiresAt == nil ||
		time.Until(*probe.ElevationExpiresAt) < 6*24*time.Hour {
		t.Fatalf("7d status = %+v", probe)
	}
}

func TestDeElevateEndsOperatorAuthorityAndTheTokenElevatesAgain(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	handle := app.enqueueApproval(t, "de-elevate")
	elevated := app.openElevatedDocument(t, "/")
	if !app.status(t, elevated).Elevated {
		t.Fatal("setup: not elevated")
	}

	status, body, response := app.postJSON(t, server.DeElevatePath, elevated, map[string]any{}, true)
	if status != http.StatusOK || body["elevated"] != false || body["assurance"] != participant.AssuranceLoopbackUnverified {
		t.Fatalf("de-elevate = %d %v", status, body)
	}
	if _, present := body["elevation_expires_at"]; present {
		t.Fatalf("a minted session must not report an elevation expiry: %v", body)
	}
	minted := rotatedCookie(response)
	if minted == nil || minted.Value == elevated.Value || !minted.HttpOnly {
		t.Fatal("de-elevate did not rotate to a fresh HttpOnly session cookie")
	}

	// The elevated cookie is dead; the new one is a live, unelevated session.
	if code := app.getJSON(t, "/api/hitl", elevated, nil); code != http.StatusForbidden {
		t.Fatalf("the elevated cookie still works after de-elevate: %d", code)
	}
	if code := app.getJSON(t, "/api/hitl", minted, nil); code != http.StatusOK {
		t.Fatalf("the new session cannot read the inbox: %d", code)
	}
	if probe := app.status(t, minted); probe.Elevated {
		t.Fatalf("status after de-elevate = %+v", probe)
	}
	// And it cannot decide anything.
	item := app.item(t, minted, handle.ItemID)
	status, body, _ = app.postJSON(t, "/api/hitl/items/"+handle.ItemID+"/present", minted, presentBody(item.Revision), true)
	assertCode(t, "present after de-elevate", status, body, http.StatusForbidden, "assurance_insufficient")

	// The operator token elevates it again.
	status, body, again := app.elevate(t, minted, testOperatorToken)
	if status != http.StatusOK || body["elevated"] != true || again == nil {
		t.Fatalf("re-elevate = %d %v", status, body)
	}
}

func TestDeElevateRefusesWithoutASessionOrFromAnotherSite(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	elevated := app.openElevatedDocument(t, "/")

	status, body, _ := app.postJSON(t, server.DeElevatePath, nil, map[string]any{}, true)
	assertCode(t, "de-elevate without a session", status, body, http.StatusForbidden, "forbidden")

	request, err := http.NewRequest(http.MethodPost, app.baseURL+server.DeElevatePath, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.Header.Set("Origin", "https://evil.example")
	request.AddCookie(elevated)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site de-elevate = %d, want 403", response.StatusCode)
	}
	if !app.status(t, elevated).Elevated {
		t.Fatal("a refused de-elevate changed the session")
	}
}

// Archie's ruling on #139: the end of an elevation is stamped when it is
// granted, so a lapsed one never revives when the lifetime is lengthened, and
// the status the browser sees agrees.
func TestStatusAgreesThatALapsedElevationStaysLapsedWhenTheLifetimeGrows(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	app.sessions.SetElevationPolicy(participant.ElevationFor(2 * time.Second))
	elevated := app.openElevatedDocument(t, "/")

	live := app.status(t, elevated)
	if !live.Elevated || live.ElevationExpiresAt == nil || time.Until(*live.ElevationExpiresAt) > 3*time.Second {
		t.Fatalf("a 2s elevation reports %+v", live)
	}
	// A longer lifetime now must not stretch the stamped end of this elevation.
	app.sessions.SetElevationPolicy(participant.ElevationAlways())
	if probe := app.status(t, elevated); probe.ElevationExpiresAt == nil || time.Until(*probe.ElevationExpiresAt) > 3*time.Second {
		t.Fatalf("lengthening moved the end of an existing elevation: %+v", probe)
	}
	time.Sleep(2500 * time.Millisecond)
	if probe := app.status(t, elevated); probe.Elevated || probe.ElevationExpiresAt != nil {
		t.Fatalf("a lapsed elevation revived under a longer lifetime: %+v", probe)
	}

	// New elevations do get the longer lifetime.
	fresh := app.openElevatedDocument(t, "/")
	if probe := app.status(t, fresh); !probe.Elevated || probe.ElevationExpiresAt == nil || time.Until(*probe.ElevationExpiresAt) < 24*time.Hour {
		t.Fatalf("a new elevation under 'always' reports %+v", probe)
	}
}
