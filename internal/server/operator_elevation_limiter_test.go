package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestElevationLimiterChecksAndRecordsAtomically: concurrent attempts from one
// session cannot all pass the check before any is counted.
func TestElevationLimiterChecksAndRecordsAtomically(t *testing.T) {
	t.Parallel()
	limiter := newElevationLimiter()
	var admitted atomic.Int32
	var wait sync.WaitGroup
	for range 50 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, ok, _ := limiter.admit("session-a"); ok {
				admitted.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := admitted.Load(); got != elevationFailuresPerWindow {
		t.Fatalf("admitted %d concurrent attempts, want exactly %d", got, elevationFailuresPerWindow)
	}
}

func TestElevationLimiterFloodAlertsOncePerWindowAndRefusesNobody(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	limiter := newElevationLimiter()
	limiter.now = func() time.Time { return now }
	floods := 0
	for i := range 40 {
		_, ok, flood := limiter.admit(string(rune('a'+i%26)) + "-fresh-session-" + string(rune('A'+i/26)))
		if !ok {
			t.Fatalf("attempt %d from a fresh session was refused", i)
		}
		if flood {
			floods++
		}
	}
	if floods != 1 {
		t.Fatalf("flood alerts in one window = %d, want 1", floods)
	}
	now = now.Add(elevationWindow + time.Second)
	if _, ok, _ := limiter.admit("operator"); !ok {
		t.Fatal("a fresh session was refused after the window")
	}
}

func TestElevationLimiterSuccessDoesNotCountAsAFailure(t *testing.T) {
	t.Parallel()
	limiter := newElevationLimiter()
	for range 20 {
		attempt, ok, _ := limiter.admit("session-a")
		if !ok {
			t.Fatal("successful attempts were counted as failures")
		}
		limiter.succeed(attempt)
	}
}
