package participant

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
)

// TestElevationLapsesAfterTwelveHoursWithoutRefreshingTheSession: operator
// authority lasts ElevatedLifetime from the elevation, the rotated session
// keeps its predecessor's absolute expiry, and a lapsed session reads as a
// minted one (so the UI's elevation prompt handles it).
func TestElevationLapsesAfterTwelveHoursWithoutRefreshingTheSession(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	clock := time.Now().UTC()
	store.now = func() time.Time { return clock }
	gate, err := NewGate(store)
	if err != nil {
		t.Fatal(err)
	}
	gate.ConfigureOperatorToken(fixtureOperatorToken, nil)
	ctx := context.Background()
	minted, _, err := store.Mint(ctx, DefaultTemplate)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(48 * time.Hour)
	recorder := httptest.NewRecorder()
	elevated, err := gate.Elevate(ctx, recorder, minted, fixtureOperatorToken)
	if err != nil {
		t.Fatalf("Elevate: %v", err)
	}
	if !elevated.ExpiresAt.Equal(minted.ExpiresAt) {
		t.Fatalf("elevation refreshed the absolute expiry: %s -> %s", minted.ExpiresAt, elevated.ExpiresAt)
	}
	cookie := recorder.Result().Cookies()[0].Value

	clock = clock.Add(ElevatedLifetime - time.Minute)
	if current, err := store.Lookup(ctx, cookie); err != nil || !current.Elevated() {
		t.Fatalf("inside the window: %+v, %v; want elevated", current, err)
	}
	clock = clock.Add(2 * time.Minute)
	lapsed, err := store.Lookup(ctx, cookie)
	if err != nil {
		t.Fatalf("a lapsed elevation invalidated the session: %v", err)
	}
	if lapsed.Elevated() || lapsed.Holds(authz.Resolve) {
		t.Fatalf("lapsed session = %+v, want minted assurance without resolve", lapsed)
	}
	if err := gate.Authorize(lapsed, LocalSurfaces(), authz.Resolve); !errors.Is(err, ErrAssuranceInsufficient) {
		t.Fatalf("lapsed resolve = %v, want ErrAssuranceInsufficient", err)
	}
	byID, err := store.LookupByID(ctx, elevated.ID)
	if err != nil || byID.Elevated() {
		t.Fatalf("LookupByID after lapse = %+v, %v", byID, err)
	}
	// It can be elevated again.
	if again, err := gate.Elevate(ctx, httptest.NewRecorder(), lapsed, fixtureOperatorToken); err != nil || !again.Elevated() {
		t.Fatalf("re-elevation = %+v, %v", again, err)
	}
}
