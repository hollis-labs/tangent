package participant

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
)

func TestParseElevationTTL(t *testing.T) {
	t.Parallel()
	valid := map[string]string{
		"": "always", "always": "always", "ALWAYS": "always", "  Always ": "always",
		"never": "never", " NEVER": "never",
		"1d": "1d", "7d": "7d", "30d": "30d", "45D": "45d", "3650d": "3650d",
	}
	for in, want := range valid {
		policy, err := ParseElevationTTL(in)
		if err != nil || policy.String() != want {
			t.Errorf("ParseElevationTTL(%q) = %q, %v; want %q", in, policy.String(), err, want)
		}
	}
	for _, in := range []string{
		"7", "d", "0d", "00d", "-1d", "+1d", "1.5d", "7days", "7 d", "12h", "forever", "yes", "3651d",
		"99999999999999999999d", "always!", "d7",
	} {
		_, err := ParseElevationTTL(in)
		if err == nil {
			t.Errorf("ParseElevationTTL(%q) accepted", in)
			continue
		}
		if !strings.Contains(err.Error(), EnvElevationTTL) {
			t.Errorf("ParseElevationTTL(%q) error does not name %s: %v", in, EnvElevationTTL, err)
		}
	}
}

func TestElevationPolicyWindows(t *testing.T) {
	t.Parallel()
	if w, finite := ElevationAlways().Window(); finite || w != 0 {
		t.Errorf("always = %v, %v", w, finite)
	}
	if w, finite := (ElevationPolicy{}).Window(); finite || w != 0 || (ElevationPolicy{}).String() != "always" {
		t.Errorf("zero value must be always: %v %v %q", w, finite, (ElevationPolicy{}).String())
	}
	if w, finite := ElevationNever().Window(); !finite || w != NeverElevationWindow || w <= 0 {
		t.Errorf("never = %v, %v (must be a positive window, not zero)", w, finite)
	}
	if w, finite := ElevationDays(7).Window(); !finite || w != 7*24*time.Hour {
		t.Errorf("7d = %v, %v", w, finite)
	}
	if ElevationDays(7).ExceedsSessionCap() || !ElevationDays(31).ExceedsSessionCap() || ElevationAlways().ExceedsSessionCap() {
		t.Error("ExceedsSessionCap disagrees with the 30 day session cap")
	}
}

type policyHarness struct {
	t     *testing.T
	store *Store
	db    *sql.DB
	gate  *Gate
	clock time.Time
	ctx   context.Context
}

func newPolicyHarness(t *testing.T, policy *ElevationPolicy) *policyHarness {
	t.Helper()
	store, db := openTestStore(t)
	h := &policyHarness{t: t, store: store, db: db, clock: time.Now().UTC(), ctx: context.Background()}
	store.now = func() time.Time { return h.clock }
	if policy != nil {
		store.SetElevationPolicy(*policy)
	}
	gate, err := NewGate(store)
	if err != nil {
		t.Fatal(err)
	}
	gate.ConfigureOperatorToken(fixtureOperatorToken, nil)
	h.gate = gate
	return h
}

// elevate mints a session and elevates it now; it returns the elevated
// session and its cookie value.
func (h *policyHarness) elevate() (Session, string) {
	h.t.Helper()
	minted, _, err := h.store.Mint(h.ctx, DefaultTemplate)
	if err != nil {
		h.t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	elevated, err := h.gate.Elevate(h.ctx, recorder, minted, fixtureOperatorToken)
	if err != nil {
		h.t.Fatalf("Elevate: %v", err)
	}
	return elevated, recorder.Result().Cookies()[0].Value
}

func (h *policyHarness) lookup(cookie string) (Session, error) { return h.store.Lookup(h.ctx, cookie) }

func TestEveryPolicyLapsesAtItsWindowOrWithTheSession(t *testing.T) {
	t.Parallel()
	day := 24 * time.Hour
	cases := []struct {
		name   string
		policy ElevationPolicy
		// elevatedAt are offsets from the elevation at which the session reads
		// elevated; lapsedAt are offsets at which it is live but reads minted;
		// goneAt are offsets at which the session itself no longer exists.
		elevatedAt, lapsedAt, goneAt []time.Duration
	}{
		{"always", ElevationAlways(),
			[]time.Duration{time.Second, 12 * time.Hour, 13 * time.Hour, 7 * day, 29*day + 23*time.Hour}, nil,
			[]time.Duration{30 * day, 31 * day}},
		{"never", ElevationNever(),
			[]time.Duration{time.Second, NeverElevationWindow - time.Second},
			[]time.Duration{NeverElevationWindow, NeverElevationWindow + time.Second, time.Hour, 20 * day}, []time.Duration{30 * day}},
		{"7d", ElevationDays(7),
			[]time.Duration{time.Hour, 6*day + 23*time.Hour + 59*time.Minute},
			[]time.Duration{7 * day, 7*day + time.Minute, 29 * day}, []time.Duration{30 * day}},
		// A window longer than the session is clipped by the session: the cap
		// is not reversed by the setting.
		{"45d", ElevationDays(45),
			[]time.Duration{29*day + 23*time.Hour}, nil, []time.Duration{30 * day, 44 * day}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := tc.policy
			h := newPolicyHarness(t, &policy)
			_, cookie := h.elevate()
			start := h.clock
			for _, off := range tc.elevatedAt {
				h.clock = start.Add(off)
				s, err := h.lookup(cookie)
				if err != nil || !s.Elevated() || !s.Holds(authz.Resolve) {
					t.Errorf("+%s: want elevated with resolve, got %+v, %v", off, s, err)
				}
			}
			for _, off := range tc.lapsedAt {
				h.clock = start.Add(off)
				s, err := h.lookup(cookie)
				if err != nil {
					t.Errorf("+%s: a lapsed elevation must not invalidate the session: %v", off, err)
					continue
				}
				if s.Elevated() || s.Holds(authz.Resolve) {
					t.Errorf("+%s: want minted, got %+v", off, s)
				}
				if authErr := h.gate.Authorize(s, LocalSurfaces(), authz.Resolve); !errors.Is(authErr, ErrAssuranceInsufficient) {
					t.Errorf("+%s: lapsed resolve = %v, want ErrAssuranceInsufficient", off, authErr)
				}
			}
			for _, off := range tc.goneAt {
				h.clock = start.Add(off)
				if _, err := h.lookup(cookie); !errors.Is(err, ErrSessionInvalid) {
					t.Errorf("+%s: want ErrSessionInvalid once the session expires, got %v", off, err)
				}
			}
		})
	}
}

func TestUnsetPolicyIsAlways(t *testing.T) {
	t.Parallel()
	h := newPolicyHarness(t, nil) // no SetElevationPolicy
	if got := h.store.ElevationPolicy().String(); got != "always" {
		t.Fatalf("default policy = %q", got)
	}
	_, cookie := h.elevate()
	h.clock = h.clock.Add(20 * 24 * time.Hour)
	if s, err := h.lookup(cookie); err != nil || !s.Elevated() {
		t.Fatalf("default must not lapse by time: %+v, %v", s, err)
	}
}

// Archie's ruling on #139: an elevation's end is stamped when it is granted. A
// lapsed elevation never revives when the lifetime is lengthened; lengthening
// applies to new elevations only; shortening is immediate.
func TestLengtheningTheLifetimeNeverRevivesALapsedElevation(t *testing.T) {
	t.Parallel()
	never := ElevationNever()
	h := newPolicyHarness(t, &never)
	_, cookie := h.elevate()
	if s, err := h.lookup(cookie); err != nil || !s.Elevated() {
		t.Fatalf("setup: not elevated inside the one minute window: %+v, %v", s, err)
	}
	h.clock = h.clock.Add(2 * time.Minute)
	if s, err := h.lookup(cookie); err != nil || s.Elevated() {
		t.Fatalf("setup: still elevated after the window: %+v, %v", s, err)
	}
	lapsedAt := h.clock
	for _, longer := range []ElevationPolicy{ElevationAlways(), ElevationDays(30), ElevationFor(48 * time.Hour)} {
		h.store.SetElevationPolicy(longer)
		for _, later := range []time.Duration{0, time.Hour, 20 * 24 * time.Hour} {
			h.clock = lapsedAt.Add(later)
			if s, err := h.lookup(cookie); err != nil || s.Elevated() || s.Holds(authz.Resolve) {
				t.Fatalf("policy %s after the lapse: a lapsed elevation revived: %+v, %v", longer, s, err)
			}
		}
	}
}

func TestLengtheningAppliesToNewElevationsOnly(t *testing.T) {
	t.Parallel()
	never := ElevationNever()
	h := newPolicyHarness(t, &never)
	_, old := h.elevate()
	h.store.SetElevationPolicy(ElevationDays(7))
	_, fresh := h.elevate() // granted under the longer lifetime
	h.clock = h.clock.Add(2 * time.Hour)
	if s, _ := h.lookup(old); s.Elevated() {
		t.Fatal("the elevation granted under 'never' was extended by a later, longer lifetime")
	}
	if s, err := h.lookup(fresh); err != nil || !s.Elevated() {
		t.Fatalf("the new elevation should last under the longer lifetime: %+v, %v", s, err)
	}
	h.clock = h.clock.Add(7 * 24 * time.Hour)
	if s, _ := h.lookup(fresh); s.Elevated() {
		t.Fatal("the new elevation outlived its 7 day lifetime")
	}
}

func TestShorteningTheLifetimeIsImmediate(t *testing.T) {
	t.Parallel()
	always := ElevationAlways()
	h := newPolicyHarness(t, &always)
	_, cookie := h.elevate()
	h.clock = h.clock.Add(23 * time.Hour)
	h.store.SetElevationPolicy(ElevationDays(1))
	if s, _ := h.lookup(cookie); !s.Elevated() {
		t.Fatal("23h into a 1 day window should still be elevated")
	}
	h.clock = h.clock.Add(2 * time.Hour)
	if s, _ := h.lookup(cookie); s.Elevated() {
		t.Fatal("the shortened lifetime did not end the elevation at once")
	}
}

// A shorter lifetime written at startup caps live elevations on disk, so a
// later restart with a longer one cannot bring one back, even when nothing read
// the session in between.
func TestShorteningIsPersistedAcrossRestarts(t *testing.T) {
	t.Parallel()
	always := ElevationAlways()
	h := newPolicyHarness(t, &always)
	_, cookie := h.elevate()
	db := h.db

	// Restart 1 with 'never': persists the cap.
	restart := func(policy ElevationPolicy) *Store {
		store, err := NewStore(db)
		if err != nil {
			t.Fatal(err)
		}
		store.now = func() time.Time { return h.clock }
		if err := store.ApplyElevationPolicy(h.ctx, policy); err != nil {
			t.Fatalf("ApplyElevationPolicy(%s): %v", policy, err)
		}
		return store
	}
	restart(ElevationNever())
	h.clock = h.clock.Add(2 * time.Minute) // lapses; nobody reads the session

	// Restart 2 with 'always'.
	store := restart(ElevationAlways())
	if s, err := store.Lookup(h.ctx, cookie); err != nil || s.Elevated() {
		t.Fatalf("an elevation ended by the shorter lifetime revived after a restart with a longer one: %+v, %v", s, err)
	}
}

// An elevation granted before stamping existed keeps the 12 hours it was
// granted under, whatever the lifetime is now.
func TestAnElevationGrantedBeforeStampingKeepsItsTwelveHours(t *testing.T) {
	t.Parallel()
	always := ElevationAlways()
	h := newPolicyHarness(t, &always)
	legacy := OperatorTemplate
	legacy.ID = "legacy-elevation"
	legacy.CreatedAt, legacy.LastSeenAt = h.clock.Add(-11*time.Hour), h.clock.Add(-11*time.Hour)
	legacy.ExpiresAt = h.clock.Add(10 * 24 * time.Hour)
	// No ElevatedUntil: the row is what a pre-stamping database holds.
	if err := h.store.insert(h.ctx, h.store.db, legacy, HashCookie("legacy-cookie")); err != nil {
		t.Fatal(err)
	}
	if s, err := h.lookup("legacy-cookie"); err != nil || !s.Elevated() {
		t.Fatalf("11h into a legacy elevation: %+v, %v", s, err)
	}
	h.clock = h.clock.Add(2 * time.Hour)
	if s, err := h.lookup("legacy-cookie"); err != nil || s.Elevated() {
		t.Fatalf("a legacy elevation outlived its 12 hours under 'always': %+v, %v", s, err)
	}
	// ApplyElevationPolicy stamps it so the bound is on disk.
	h.clock = h.clock.Add(-2 * time.Hour)
	if err := h.store.ApplyElevationPolicy(h.ctx, ElevationDays(30)); err != nil {
		t.Fatal(err)
	}
	var stamped any
	if err := h.db.QueryRow(`SELECT elevated_until FROM participant_sessions WHERE id = 'legacy-elevation'`).Scan(&stamped); err != nil || stamped == nil {
		t.Fatalf("the legacy elevation was not stamped: %v, %v", stamped, err)
	}
}

func TestDeElevateEndsOperatorAuthority(t *testing.T) {
	t.Parallel()
	always := ElevationAlways()
	h := newPolicyHarness(t, &always)
	elevated, oldCookie := h.elevate()
	h.clock = h.clock.Add(48 * time.Hour)

	recorder := httptest.NewRecorder()
	minted, err := h.gate.DeElevate(h.ctx, recorder, elevated)
	if err != nil {
		t.Fatalf("DeElevate: %v", err)
	}
	if minted.Elevated() || minted.Holds(authz.Resolve) || minted.Assurance != AssuranceLoopbackUnverified {
		t.Fatalf("after DeElevate: %+v", minted)
	}
	if minted.ID == elevated.ID || !minted.ExpiresAt.Equal(elevated.ExpiresAt) {
		t.Fatalf("DeElevate must rotate and keep the absolute expiry: %+v vs %+v", minted, elevated)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" || cookies[0].Value == oldCookie {
		t.Fatalf("expected one new session cookie, got %+v", cookies)
	}
	if _, lookupErr := h.lookup(oldCookie); !errors.Is(lookupErr, ErrSessionInvalid) {
		t.Fatalf("the elevated cookie must stop working: %v", lookupErr)
	}
	current, err := h.lookup(cookies[0].Value)
	if err != nil || current.Elevated() {
		t.Fatalf("new cookie: %+v, %v", current, err)
	}
	if authErr := h.gate.Authorize(current, LocalSurfaces(), authz.Resolve); !errors.Is(authErr, ErrAssuranceInsufficient) {
		t.Fatalf("resolve after DeElevate = %v, want ErrAssuranceInsufficient", authErr)
	}
	// It can be elevated again with the token.
	if again, err := h.gate.Elevate(h.ctx, httptest.NewRecorder(), current, fixtureOperatorToken); err != nil || !again.Elevated() {
		t.Fatalf("re-elevation = %+v, %v", again, err)
	}
}

func TestDeElevateOfALapsedSessionStopsALongerPolicyReviving(t *testing.T) {
	t.Parallel()
	never := ElevationNever()
	h := newPolicyHarness(t, &never)
	_, cookie := h.elevate()
	h.clock = h.clock.Add(time.Hour) // lapsed; the row still says operator-token
	lapsed, err := h.lookup(cookie)
	if err != nil || lapsed.Elevated() {
		t.Fatalf("setup: %+v, %v", lapsed, err)
	}
	recorder := httptest.NewRecorder()
	if _, err := h.gate.DeElevate(h.ctx, recorder, lapsed); err != nil {
		t.Fatalf("DeElevate of a lapsed session: %v", err)
	}
	h.store.SetElevationPolicy(ElevationAlways())
	if s, err := h.lookup(recorder.Result().Cookies()[0].Value); err != nil || s.Elevated() {
		t.Fatalf("a revoked elevation was revived by a longer policy: %+v, %v", s, err)
	}
}

func TestDeElevateRefusesWithoutASession(t *testing.T) {
	t.Parallel()
	h := newPolicyHarness(t, nil)
	if _, err := h.gate.DeElevate(h.ctx, httptest.NewRecorder(), Session{}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("DeElevate(no session) = %v", err)
	}
}
