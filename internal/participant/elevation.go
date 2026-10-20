package participant

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Operator elevation lifetime (CW-20261020-0033, CW-20261019-0043).
//
// How long an elevation lasts used to be a constant (12 hours). It is now an
// operator setting, TANGENT_ELEVATION_TTL, read once at startup:
//
//	always   elevation does not lapse by time; it ends when the operator
//	         revokes it (POST /api/session/de-elevate, or `tangent session
//	         revoke`), or when the browser session itself ends. Default.
//	never    elevation is not remembered: it lasts NeverElevationWindow, long
//	         enough to make the decision it was requested for and no more.
//	<N>d     elevation lasts N days (a whole number, 1 or more) from the
//	         moment of elevation.
//
// Every mode is bounded by the browser session's own absolute expiry
// (MaximumLifetime, 30 days). An elevation never extends it: Rotate hands the
// successor its predecessor's expiry (CW-20261016-0174), so "always" means
// "until revoked or until the session ends, whichever comes first", and an
// elevation made on day 29 has one day left whatever the setting.
//
// The policy is evaluated when a session is read, not stamped on the row.
// Shortening it takes effect on the next request; lengthening it also extends
// elevations that were made earlier and have not been revoked, as long as the
// session behind them is still live.
const (
	// EnvElevationTTL names the environment variable the policy is read from.
	EnvElevationTTL = "TANGENT_ELEVATION_TTL"

	// NeverElevationWindow is how long an elevation lasts in "never" mode. It
	// is deliberately not zero: the browser elevates and then resolves in two
	// requests, and a zero window would make the second one race the first.
	NeverElevationWindow = time.Minute

	// maxElevationDays keeps N*24h far from overflowing a Duration. Anything
	// above MaximumLifetime is already clipped by the session itself.
	maxElevationDays = 3650

	elevationModeAlways = "always"
	elevationModeNever  = "never"
)

// ElevationPolicy is how long an elevation lasts. The zero value is "always".
type ElevationPolicy struct {
	label  string
	window time.Duration // 0 with forever
	finite bool
}

// ElevationAlways is the default policy: elevation does not lapse by time.
func ElevationAlways() ElevationPolicy { return ElevationPolicy{label: elevationModeAlways} }

// ElevationNever is the "not remembered" policy.
func ElevationNever() ElevationPolicy {
	return ElevationPolicy{label: elevationModeNever, window: NeverElevationWindow, finite: true}
}

// ElevationDays lasts n days from the elevation (n >= 1).
func ElevationDays(n int) ElevationPolicy {
	return ElevationPolicy{label: strconv.Itoa(n) + "d", window: time.Duration(n) * 24 * time.Hour, finite: true}
}

// ElevationFor lasts exactly d from the elevation. It exists for callers that
// need a window the operator setting cannot express (tests, the 12 hour lapse
// this package used to hard-code).
func ElevationFor(d time.Duration) ElevationPolicy {
	return ElevationPolicy{label: d.String(), window: d, finite: true}
}

// ParseElevationTTL reads the TANGENT_ELEVATION_TTL value. Empty means the
// default, "always". Anything else that is not exactly always, never or <N>d
// is an error naming the variable: a typo must not silently pick a different
// lifetime.
func ParseElevationTTL(raw string) (ElevationPolicy, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", elevationModeAlways:
		return ElevationAlways(), nil
	case elevationModeNever:
		return ElevationNever(), nil
	}
	digits, ok := strings.CutSuffix(value, "d")
	if ok && digits != "" && strings.Trim(digits, "0123456789") == "" {
		n, err := strconv.Atoi(digits)
		if err == nil && n >= 1 && n <= maxElevationDays {
			return ElevationDays(n), nil
		}
	}
	return ElevationPolicy{}, fmt.Errorf("%s: %q is not valid; use always, never, or <N>d with N a whole number of days from 1 to %d",
		EnvElevationTTL, raw, maxElevationDays)
}

// String is the policy as the operator wrote it: always, never or <N>d.
func (p ElevationPolicy) String() string {
	if p.label == "" {
		return elevationModeAlways
	}
	return p.label
}

// Window is how long an elevation lasts, and false when it does not lapse by
// time.
func (p ElevationPolicy) Window() (time.Duration, bool) { return p.window, p.finite }

// ExceedsSessionCap reports whether the window is longer than the browser
// session itself can live, so the cap, not the policy, will end it.
func (p ElevationPolicy) ExceedsSessionCap() bool { return p.finite && p.window > MaximumLifetime }
