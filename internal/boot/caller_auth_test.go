package boot

import (
	"strings"
	"testing"
)

func TestMalformedCallerAuthFailsBeforeOpeningDatabase(t *testing.T) {
	const invalid = "must-not-appear-in-startup-errors"
	services, server, closer, err := Boot(Config{MCPCallerTokens: invalid, DBPath: t.TempDir() + "/unused.db"})
	if err == nil || services != nil || server != nil || closer != nil {
		t.Fatalf("invalid caller configuration did not fail boot: %v", err)
	}
	if !strings.Contains(err.Error(), "TANGENT_MCP_TOKENS") || strings.Contains(err.Error(), invalid) {
		t.Fatalf("unsafe configuration diagnostic: %v", err)
	}
}

func TestUnknownTrustedForwarderFailsBeforeOpeningDatabase(t *testing.T) {
	services, server, closer, err := Boot(Config{MCPTrustedForwarders: "unknown-forwarder", DBPath: t.TempDir() + "/unused.db"})
	if err == nil || services != nil || server != nil || closer != nil {
		t.Fatalf("misconfigured forwarder did not fail boot: %v", err)
	}
	if strings.Contains(err.Error(), "unknown-forwarder") {
		t.Fatal("configuration value leaked")
	}
}

func TestInvalidElevationTTLFailsBeforeOpeningDatabase(t *testing.T) {
	for _, bad := range []string{"12h", "0d", "forever", "7", "-1d"} {
		services, server, closer, err := Boot(Config{ElevationTTL: bad, DBPath: t.TempDir() + "/unused.db"})
		if err == nil || services != nil || server != nil || closer != nil {
			t.Fatalf("ElevationTTL %q did not fail boot: %v", bad, err)
		}
		if !strings.Contains(err.Error(), "TANGENT_ELEVATION_TTL") {
			t.Fatalf("ElevationTTL %q: the error does not name the variable: %v", bad, err)
		}
	}
}
