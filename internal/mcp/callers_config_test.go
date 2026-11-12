package mcp_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/mcp"
)

// Who counts as a gateway is configuration, and a wrong one is refused when the server is built,
// not discovered on the first request.
func TestCallerConfigurationIsValidatedWhenTheServerIsBuilt(t *testing.T) {
	for name, c := range map[string]mcp.Callers{
		"a forwarder that is not a gateway producer": {Gateway: []string{"tether-gateway"}, Forwarders: []string{"unknown-forwarder"}},
		"a forwarder listed twice":                   {Gateway: []string{"tether-gateway"}, Forwarders: []string{"tether-gateway", "tether-gateway"}},
		"a producer listed twice":                    {Gateway: []string{"tether-gateway", "tether-gateway"}},
		"a producer that is not a caller name":       {Gateway: []string{"Not A Name"}},
		"a producer taking the plugin-child prefix":  {Gateway: []string{"plugin-runner"}},
	} {
		if err := mcp.WithCallers(c)(&mcp.Server{}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := mcp.WithCallers(mcp.Callers{Gateway: []string{"tether-gateway", "macbook-1"}, Forwarders: []string{"tether-gateway"}})(&mcp.Server{}); err != nil {
		t.Fatalf("a valid configuration was refused: %v", err)
	}
	err := mcp.WithCallers(mcp.Callers{Gateway: []string{"tether-gateway"}, Forwarders: []string{"other"}})(&mcp.Server{})
	if err == nil || !strings.Contains(err.Error(), "forwarder") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
}
