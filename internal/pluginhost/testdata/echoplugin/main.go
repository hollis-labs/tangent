// Command echoplugin is a minimal subprocess plugin, used by
// internal/pluginhost's tests to exercise the real wire against a real child
// process (CW-20260910-0034).
//
// It lives under testdata so `go build ./...` does not build it: it is a main
// package that only a test has a use for, and the test compiles it by explicit
// path. That mirrors internal/plugintemplate/example — compiled and exercised
// here, shipped in no build.
//
// It is deliberately the smallest thing that proves the path end to end rather
// than a demonstration of what a plugin should look like. `docs/writing-a-plugin.md`
// is where an author should start.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

type echoPlugin struct {
	// initParams is kept so the test can assert what the host sent — in
	// particular that Config arrived EMPTY, which is Tangent's secret boundary
	// expressed on the wire rather than in a policy document.
	initParams subprocess.InitParams
}

func (p *echoPlugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	p.initParams = params
	return subprocess.InitResult{
		ID:          "tangent.plugin.echo",
		Name:        "Echo",
		Version:     "0.1.0",
		Description: "Echoes its input back, for testing the subprocess wire.",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *echoPlugin) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}

func (p *echoPlugin) Unload(context.Context) error { return nil }

// Health answers plugin/health, controllable via ECHO_PLUGIN_HEALTH so a test
// can make this plugin report unhealthy without a second test fixture. Empty
// or "ok" reports healthy; any other value is reported as the message on an
// unhealthy answer.
func (p *echoPlugin) Health(context.Context) (subprocess.HealthStatus, error) {
	if message := os.Getenv("ECHO_PLUGIN_HEALTH"); message != "" && message != "ok" {
		return subprocess.HealthStatus{OK: false, Message: message}, nil
	}
	return subprocess.HealthStatus{OK: true}, nil
}

// MCPCallTool answers the two tools the test registers.
func (p *echoPlugin) MCPCallTool(
	_ context.Context,
	request subprocess.MCPCallRequest,
) (subprocess.MCPCallResult, error) {
	switch request.ToolName {
	case "tangent.echo":
		body, err := json.Marshal(map[string]any{
			"tool":        request.ToolName,
			"arguments":   request.Arguments,
			"config_size": len(p.initParams.Config),
			"data_dir":    p.initParams.DataDir,
			"host":        p.initParams.HostInfo.Version,
			// Proves the child reads its OWN environment rather than being
			// handed config by the host.
			"secret_from_env": os.Getenv("ECHO_PLUGIN_SECRET"),
		})
		if err != nil {
			return subprocess.MCPCallResult{}, err
		}
		return subprocess.MCPCallResult{Content: body}, nil
	case "tangent.echo_slow":
		// Blocks until the host gives up on it. The host's call must time out
		// without killing the connection, which is the defect this wire exists
		// not to reproduce.
		<-make(chan struct{})
		return subprocess.MCPCallResult{}, nil
	default:
		return subprocess.MCPCallResult{}, fmt.Errorf("echoplugin: no handler for %q", request.ToolName)
	}
}

// HTTPHandle answers the one route the test registers.
func (p *echoPlugin) HTTPHandle(
	_ context.Context,
	request subprocess.HTTPRequest,
) (subprocess.HTTPResponse, error) {
	body, err := json.Marshal(map[string]any{"path": request.Path, "body": string(request.Body)})
	if err != nil {
		return subprocess.HTTPResponse{}, err
	}
	return subprocess.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}, nil
}

func main() {
	if err := subprocess.Serve(&echoPlugin{}); err != nil {
		fmt.Fprintf(os.Stderr, "echoplugin: %v\n", err)
		os.Exit(1)
	}
}
