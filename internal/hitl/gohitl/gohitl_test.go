package gohitl

import "testing"

func TestEnabledIsOffByDefaultAndRefusesTypos(t *testing.T) {
	for value, want := range map[string]bool{"": false, "native": false, " Native ": false, "gohitl": true, "GoHITL": true} {
		got, err := Enabled(value)
		if err != nil || got != want {
			t.Errorf("Enabled(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, typo := range []string{"go-hitl", "on", "true", "native2"} {
		if _, err := Enabled(typo); err == nil {
			t.Errorf("Enabled(%q) must be an error so a typo does not silently leave the flag off", typo)
		}
	}
}
