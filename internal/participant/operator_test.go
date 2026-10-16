package participant

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/authz"
)

const fixtureOperatorToken = "fixture-operator-token-1d4e7a2c9b6f3e58-c0ffee12"

// TestUnusableOperatorTokenWarnsOnceByNameAndNeverByValue covers the startup
// half of fail-closed: an unset or short token disables elevation and logs one
// warning that names the variable, never the value.
func TestUnusableOperatorTokenWarnsOnceByNameAndNeverByValue(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	gate, err := NewGate(store)
	if err != nil {
		t.Fatal(err)
	}
	short := "short-but-secret-value"
	for _, raw := range []string{"", short} {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		if gate.ConfigureOperatorToken(raw, logger) {
			t.Fatalf("a %d-byte token was accepted", len(raw))
		}
		if gate.OperatorTokenConfigured() {
			t.Fatal("an unusable token left elevation enabled")
		}
		output := logs.String()
		if strings.Count(output, "level=WARN") != 1 || !strings.Contains(output, EnvOperatorToken) {
			t.Fatalf("want one warning naming %s, got:\n%s", EnvOperatorToken, output)
		}
		if raw != "" && strings.Contains(output, raw) {
			t.Fatal("the warning logged the token value")
		}
	}

	var logs bytes.Buffer
	if !gate.ConfigureOperatorToken(fixtureOperatorToken, slog.New(slog.NewTextHandler(&logs, nil))) {
		t.Fatal("a usable token was refused")
	}
	if logs.Len() != 0 {
		t.Fatalf("a usable token logged something:\n%s", logs.String())
	}
}

func TestElevateRotatesToOperatorAssuranceOnlyWithTheToken(t *testing.T) {
	t.Parallel()
	store, database := openTestStore(t)
	gate, err := NewGate(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	minted, _, err := store.Mint(ctx, DefaultTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// Not configured: even the right value is refused.
	if _, err := gate.Elevate(ctx, httptest.NewRecorder(), minted, fixtureOperatorToken); !errors.Is(err, ErrElevationRefused) {
		t.Fatalf("elevation with no token configured = %v, want refused", err)
	}
	gate.ConfigureOperatorToken(fixtureOperatorToken, nil)
	if _, err := gate.Elevate(ctx, httptest.NewRecorder(), minted, fixtureOperatorToken+"x"); !errors.Is(err, ErrElevationRefused) {
		t.Fatalf("elevation with a wrong token = %v, want refused", err)
	}

	recorder := httptest.NewRecorder()
	elevated, err := gate.Elevate(ctx, recorder, minted, fixtureOperatorToken)
	if err != nil {
		t.Fatalf("Elevate: %v", err)
	}
	if !elevated.Elevated() || elevated.ID == minted.ID || !elevated.Holds(authz.Resolve) {
		t.Fatalf("elevated session = %+v", elevated)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName || cookies[0].Value == "" {
		t.Fatalf("elevation set cookies %v", cookies)
	}
	if err := gate.Authorize(elevated, LocalSurfaces(), authz.Resolve); err != nil {
		t.Fatalf("an elevated session cannot resolve: %v", err)
	}
	if err := gate.Authorize(minted, LocalSurfaces(), authz.Resolve); !errors.Is(err, ErrAssuranceInsufficient) {
		t.Fatalf("a minted session resolving = %v, want ErrAssuranceInsufficient", err)
	}
	// The predecessor cannot be elevated again: it was revoked.
	if _, err := gate.Elevate(ctx, httptest.NewRecorder(), minted, fixtureOperatorToken); !errors.Is(err, ErrElevationRefused) {
		t.Fatalf("re-elevating a rotated session = %v, want refused", err)
	}
	assertValueAbsentFromDatabase(t, database, fixtureOperatorToken)

	// The ws binding of an unelevated session never advertises resolve, even
	// when its stored grants (a pre-change row) list it.
	legacy := DefaultTemplate
	legacy.Grants = authz.DefaultParticipantCapabilities()
	_, value, err := store.Mint(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: value})
	binding, err := gate.ResolveBinding(request)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Holds(string(authz.Resolve)) {
		t.Fatalf("an unelevated binding advertises resolve: %v", binding.Capabilities)
	}
}
