package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/health"
)

// ReadOnlyProbeTool is the single tool this check calls.
//
// It is `tangent.health_report` and the choice is deliberate. Every candidate
// proves the transport carried a call; only this one also answers the question
// the check exists to ask. `tangent.list_workflows` proves dispatch and
// nothing about whether the host can serve. `definition_registry_list` proves
// the registry materialized but not that the database behind it answers.
// `tangent.retention_status` reads custody posture, which is a different
// question and a larger surface than a smoke check needs.
//
// `tangent.health_report` writes nothing, creates no room, touches no
// participant state, and returns the same readiness and capability vocabulary
// `/readyz` and `cerberus resource doctor` already print — so a finding
// derived from it reads in the words the operator is already holding. It is
// also the only probe that reaches an agent behind the Tether gateway, which
// speaks JSON-RPC and holds no HTTP client for `/readyz`.
const ReadOnlyProbeTool = "tangent.health_report"

// probeTimeout bounds one HTTP probe. A smoke check that can hang is a smoke
// check an operator stops running.
const probeTimeout = 15 * time.Second

// Endpoint is one Tangent HTTP base URL under check.
type Endpoint struct {
	BaseURL string
	Client  *http.Client
}

// NewEndpoint builds an endpoint with the standard probe timeout.
func NewEndpoint(baseURL string) Endpoint {
	return Endpoint{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Client:  &http.Client{Timeout: probeTimeout},
	}
}

// Listening reports whether anything answers liveness at this address.
//
// It is the probe half of [ClassifySupervisor]: a supervisor's `running` and
// this function's false are the discrepancy that check exists to surface.
func (e Endpoint) Listening(ctx context.Context) bool {
	_, err := e.get(ctx, "/healthz")
	return err == nil
}

// PortOpen reports whether a TCP connection to the endpoint's host:port
// succeeds, independent of whether HTTP answers.
//
// It separates "the process is gone" from "the process is up but not serving
// what we asked for", which is the difference between restarting Tangent and
// reading its logs.
func (e Endpoint) PortOpen(ctx context.Context) bool {
	parsed, err := url.Parse(e.BaseURL)
	if err != nil {
		return false
	}
	host := parsed.Host
	if parsed.Port() == "" {
		host = net.JoinHostPort(parsed.Hostname(), map[string]string{"http": "80", "https": "443"}[parsed.Scheme])
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Liveness checks `/healthz`, the probe that touches nothing.
func (e Endpoint) Liveness(ctx context.Context) *Finding {
	body, err := e.get(ctx, "/healthz")
	if err != nil {
		return Unreachable("liveness /healthz", e.BaseURL, err)
	}
	var report health.LivenessReport
	if err := json.Unmarshal(body, &report); err != nil {
		return Unreachable("liveness /healthz", e.BaseURL, fmt.Errorf("undecodable body: %w", err))
	}
	if report.Status != "ok" {
		return &Finding{
			Mode:   ModeProcessDown,
			Check:  "liveness /healthz",
			Detail: fmt.Sprintf("liveness answered status=%q at %s", report.Status, e.BaseURL),
			Action: "Restart the Tangent process; a liveness answer that is not `ok` means the process " +
				"cannot vouch for itself.",
		}
	}
	return nil
}

// Readiness reads `/readyz`.
//
// A readiness failure answers 503 by design, so a non-200 here is a report to
// read rather than a transport error — the body is decoded either way.
func (e Endpoint) Readiness(ctx context.Context) (health.ReadinessReport, *Finding) {
	body, err := e.getAllowingStatus(ctx, "/readyz", http.StatusOK, http.StatusServiceUnavailable)
	if err != nil {
		return health.ReadinessReport{}, Unreachable("readiness /readyz", e.BaseURL, err)
	}
	var report health.ReadinessReport
	if err := json.Unmarshal(body, &report); err != nil {
		return report, Unreachable("readiness /readyz", e.BaseURL, fmt.Errorf("undecodable body: %w", err))
	}
	return report, ClassifyReadiness(report, e.BaseURL)
}

// CapabilitySummary reads `/healthz/capability`, the bounded per-kind answer.
func (e Endpoint) CapabilitySummary(ctx context.Context) (health.CapabilitySummaryReport, *Finding) {
	body, err := e.getAllowingStatus(ctx, "/healthz/capability", http.StatusOK, http.StatusServiceUnavailable)
	if err != nil {
		return health.CapabilitySummaryReport{}, Unreachable("capability /healthz/capability", e.BaseURL, err)
	}
	var report health.CapabilitySummaryReport
	if err := json.Unmarshal(body, &report); err != nil {
		return report, Unreachable("capability /healthz/capability", e.BaseURL, fmt.Errorf("undecodable body: %w", err))
	}
	return report, ClassifyCapabilitySummary(report, e.BaseURL)
}

// StreamableSurface lists tools over direct `/mcp`.
//
// It sends the same single stateless POST the documented curl probe does,
// rather than driving an SDK client, so this check and the recipe in
// docs/mcp-smoketest.md exercise the same request.
func (e Endpoint) StreamableSurface(ctx context.Context) (Surface, *Finding) {
	body, err := e.rpc(ctx, "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if err != nil {
		return Surface{}, Unreachable("direct /mcp tools/list", e.BaseURL+"/mcp", err)
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Surface{}, Unreachable("direct /mcp tools/list", e.BaseURL+"/mcp", fmt.Errorf("undecodable body: %w", err))
	}
	if len(response.Error) > 0 {
		return Surface{}, Unreachable("direct /mcp tools/list", e.BaseURL+"/mcp",
			fmt.Errorf("JSON-RPC error: %s", string(response.Error)))
	}
	names := make([]string, 0, len(response.Result.Tools))
	for _, tool := range response.Result.Tools {
		names = append(names, tool.Name)
	}
	return NewSurface(names), nil
}

// CallHealthReport invokes [ReadOnlyProbeTool] over direct `/mcp` and returns
// the readiness half of what it reported.
func (e Endpoint) CallHealthReport(ctx context.Context) (HealthReport, *Finding) {
	body, err := e.rpc(ctx, "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": ReadOnlyProbeTool, "arguments": map[string]any{}},
	})
	if err != nil {
		return HealthReport{}, Unreachable("read-only tool call "+ReadOnlyProbeTool, e.BaseURL+"/mcp", err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return HealthReport{}, Unreachable("read-only tool call "+ReadOnlyProbeTool, e.BaseURL+"/mcp",
			fmt.Errorf("undecodable body: %w", err))
	}
	if len(response.Error) > 0 {
		return HealthReport{}, Unreachable("read-only tool call "+ReadOnlyProbeTool, e.BaseURL+"/mcp",
			fmt.Errorf("JSON-RPC error: %s", string(response.Error)))
	}
	return decodeHealthReport("direct /mcp", response.Result)
}

// HealthReport mirrors the fields of tangent.health_report's result this check
// reads. It is a separate declaration rather than an import because the tool's
// result type is unexported; keeping the mirror narrow means a field added
// there does not silently change what the smoke check asserts.
type HealthReport struct {
	HostVersion       string                          `json:"host_version"`
	Liveness          health.LivenessReport           `json:"liveness"`
	Readiness         health.ReadinessReport          `json:"readiness"`
	CapabilitySummary *health.CapabilitySummaryReport `json:"capability_summary,omitempty"`
	Capability        *health.CapabilityReport        `json:"capability,omitempty"`
}

func decodeHealthReport(where string, rawResult json.RawMessage) (HealthReport, *Finding) {
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return HealthReport{}, &Finding{
			Mode:   ModeCapabilityUnhealthy,
			Check:  where + " " + ReadOnlyProbeTool,
			Detail: fmt.Sprintf("undecodable tool result: %v", err),
			Action: "Re-run against a build that serves the documented health tool result shape.",
		}
	}
	if result.IsError {
		detail := "tool returned an error result"
		if len(result.Content) > 0 {
			detail = result.Content[0].Text
		}
		return HealthReport{}, &Finding{
			Mode:   ModeCapabilityUnhealthy,
			Check:  where + " " + ReadOnlyProbeTool,
			Detail: bound(detail),
			Action: "The host answered but declined to report health; read its logs. A build with no " +
				"health reporter wired answers `health_unavailable`.",
		}
	}
	payload := result.StructuredContent
	if len(payload) == 0 && len(result.Content) > 0 {
		payload = json.RawMessage(result.Content[0].Text)
	}
	var report HealthReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return report, &Finding{
			Mode:   ModeCapabilityUnhealthy,
			Check:  where + " " + ReadOnlyProbeTool,
			Detail: fmt.Sprintf("undecodable health report: %v", err),
			Action: "Re-run against a build that serves the documented health tool result shape.",
		}
	}
	return report, ClassifyReadiness(report.Readiness, where)
}

// bound keeps a remote-supplied string from turning one finding into a page.
func bound(text string) string {
	const limit = 240
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// LegacySSESurface lists tools over the legacy `/sse` mount, using a real MCP
// client session.
//
// `/sse` cannot be probed with a single POST the way `/mcp` can: the client
// opens a long-lived stream, receives an `endpoint` event naming a
// session-scoped POST URL, and reads replies back off the stream. Driving it
// with the SDK client is what a legacy consumer actually does, and it is the
// transport the Tether gateway dials — so a `/sse` that lists a different
// surface than `/mcp` is the deployment defect this check is here to find.
func (e Endpoint) LegacySSESurface(ctx context.Context) (Surface, *Finding) {
	address := e.BaseURL + "/sse"
	transport := &mcpsdk.SSEClientTransport{Endpoint: address, HTTPClient: e.Client}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-smoke", Version: "1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return Surface{}, Unreachable("legacy /sse connect", address, err)
	}
	defer func() { _ = session.Close() }()

	var names []string
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return Surface{}, Unreachable("legacy /sse tools/list", address, err)
	}
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return NewSurface(names), nil
}

// CallHealthReportOverSSE invokes [ReadOnlyProbeTool] over the legacy stream.
func (e Endpoint) CallHealthReportOverSSE(ctx context.Context) (HealthReport, *Finding) {
	address := e.BaseURL + "/sse"
	transport := &mcpsdk.SSEClientTransport{Endpoint: address, HTTPClient: e.Client}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-smoke", Version: "1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return HealthReport{}, Unreachable("legacy /sse connect", address, err)
	}
	defer func() { _ = session.Close() }()

	called, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: ReadOnlyProbeTool, Arguments: map[string]any{},
	})
	if err != nil {
		return HealthReport{}, Unreachable("legacy /sse "+ReadOnlyProbeTool, address, err)
	}
	encoded, err := json.Marshal(called)
	if err != nil {
		return HealthReport{}, Unreachable("legacy /sse "+ReadOnlyProbeTool, address, err)
	}
	return decodeHealthReport("legacy /sse", encoded)
}

func (e Endpoint) get(ctx context.Context, path string) ([]byte, error) {
	return e.getAllowingStatus(ctx, path, http.StatusOK)
}

func (e Endpoint) getAllowingStatus(ctx context.Context, path string, allowed ...int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return e.do(request, allowed)
}

func (e Endpoint) rpc(ctx context.Context, path string, message map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	return e.do(request, []int{http.StatusOK})
}

func (e Endpoint) do(request *http.Request, allowed []int) ([]byte, error) {
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	// #nosec G704 -- the URL is the operator-supplied deployment address this
	// check exists to probe; there is no untrusted input on this path.
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	for _, status := range allowed {
		if response.StatusCode == status {
			return body, nil
		}
	}
	return nil, fmt.Errorf("HTTP %d from %s", response.StatusCode, request.URL.Path)
}
