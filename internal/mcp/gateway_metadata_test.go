package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/hitl"
	"github.com/hollis-labs/tangent/internal/interaction"
	tangentmcp "github.com/hollis-labs/tangent/internal/mcp"
	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/telemetry"
)

// These tests reproduce CW-20260907-0022 at the MCP boundary: Tether's
// proxy forwards tool calls with the caller's W3C trace context written into
// the arguments as `_traceparent` (and `_tracestate`), and the strict v1 HITL
// schemas rejected the whole call. The argument shapes below are exactly what
// the proxy sends (github.com/hollis-labs/go-otel/propagation.InjectMCP).

const (
	gatewayTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	gatewayTraceHex    = "4bf92f3577b34da6a3ce929d0e0e4736"
	gatewaySpanHex     = "00f067aa0ba902b7"
)

func newGatewayTestServer(t *testing.T) *tangentmcp.Server {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrationErr := tangentdb.RunMigrations(database); migrationErr != nil {
		t.Fatalf("db.RunMigrations: %v", migrationErr)
	}
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if registrationErr := extensions.RegisterHITLItem(envelopeService); registrationErr != nil {
		t.Fatalf("RegisterHITLItem: %v", registrationErr)
	}
	interactions, err := interaction.NewService(
		interaction.NewStore(database),
		interaction.NewEnvelopeDefinitionCatalog(envelopeService, tangentmcp.HostVersion),
		interaction.WithAwaitPollInterval(time.Millisecond),
		interaction.WithSurfaceAccessPolicy(hitl.SurfaceAccessPolicy{}),
	)
	if err != nil {
		t.Fatalf("interaction.NewService: %v", err)
	}
	hitlService, err := hitl.NewService(interactions)
	if err != nil {
		t.Fatalf("hitl.NewService: %v", err)
	}
	server, err := tangentmcp.New(
		envelopeService, envelope.NewDispatcher(envelopeService), room.NewManager(nil), "",
		tangentmcp.WithInteractionService(interactions), tangentmcp.WithHITLService(hitlService),
	)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	return server
}

// gatewayEnqueueRequest is the contract's own minimal attention request, as a
// gateway would forward it: the caller's fields plus the injected trace keys.
func gatewayEnqueueRequest(extra map[string]any) map[string]any {
	request := map[string]any{
		"contract_version": "1.0",
		"kind":             "attention",
		"idempotency_key":  "gateway-test:attention-1",
		"title":            "Forwarded through a tracing gateway",
		"summary":          "The proxy injected trace context into the arguments.",
		"request":          "Acknowledge this item.",
		"source": map[string]any{
			"application_id": "gateway-test", "agent_id": "gateway-worker",
		},
	}
	for key, value := range extra {
		request[key] = value
	}
	return request
}

func callRaw(t *testing.T, client *mcpsdk.ClientSession, name string, arguments map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s) transport error: %v", name, err)
	}
	return result
}

func TestGatewayTraceMetadataIsStrippedBeforeStrictValidation(t *testing.T) {
	server := newGatewayTestServer(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()

	// The exact failing shape from the spike: a valid v1 request plus the
	// proxy's `_traceparent`. Before the fix this returned
	//   validating "arguments": validating root: unexpected additional properties ["_traceparent"]
	enqueue := callRaw(t, client, "tangent.hitl_enqueue", gatewayEnqueueRequest(map[string]any{
		"_traceparent": gatewayTraceparent,
		"_tracestate":  "congo=t61rcWkgMzE",
	}))
	if enqueue.IsError {
		t.Fatalf("hitl_enqueue with gateway metadata was refused: %s", extractText(t, enqueue))
	}
	var handle hitl.ItemHandle
	if err := json.Unmarshal([]byte(extractText(t, enqueue)), &handle); err != nil || handle.ItemID == "" {
		t.Fatalf("hitl_enqueue result = %s (%v)", extractText(t, enqueue), err)
	}

	// The remaining three strict tools take the same metadata.
	caller := map[string]any{"application_id": "gateway-test"}
	for _, name := range []string{"tangent.hitl_get", "tangent.hitl_await"} {
		arguments := map[string]any{
			"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
			"_traceparent": gatewayTraceparent,
		}
		if name == "tangent.hitl_await" {
			arguments["wait_ms"] = 1
		}
		result := callRaw(t, client, name, arguments)
		if result.IsError {
			t.Fatalf("%s with gateway metadata was refused: %s", name, extractText(t, result))
		}
	}
	withdraw := callRaw(t, client, "tangent.hitl_withdraw", map[string]any{
		"contract_version": "1.0", "item_id": handle.ItemID, "caller": caller,
		"reason": "test complete", "_traceparent": gatewayTraceparent,
	})
	if withdraw.IsError {
		t.Fatalf("hitl_withdraw with gateway metadata was refused: %s", extractText(t, withdraw))
	}
}

func TestUnknownUnderscoreKeysAreStillRejectedByTheStrictSchema(t *testing.T) {
	server := newGatewayTestServer(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()

	for name, extra := range map[string]map[string]any{
		"unknown key alone":            {"_bogus": "x"},
		"unknown key beside accepted":  {"_traceparent": gatewayTraceparent, "_bogus": "x"},
		"traceparent with wrong case":  {"_TraceParent": gatewayTraceparent},
		"meta-shaped key in arguments": {"_meta": map[string]any{"traceparent": gatewayTraceparent}},
	} {
		t.Run(name, func(t *testing.T) {
			result := callRaw(t, client, "tangent.hitl_enqueue", gatewayEnqueueRequest(extra))
			if !result.IsError {
				t.Fatalf("%v must still be rejected; the schema stays strict", extra)
			}
			text := extractText(t, result)
			if !strings.Contains(text, "unexpected additional properties") || strings.Contains(text, `"_traceparent"`) {
				t.Fatalf("rejection must name only the unknown key, got: %s", text)
			}
		})
	}
}

func TestMalformedGatewayTraceparentIsStrippedButNotRecorded(t *testing.T) {
	server := newGatewayTestServer(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	sink := &gatewaySink{}
	recorder := telemetry.New(telemetry.WithSink(sink))
	registerUpstreamProbe(t, server, recorder)

	for name, value := range map[string]any{
		"garbage string": "not-a-traceparent",
		"wrong type":     12345,
		"object":         map[string]any{"traceparent": gatewayTraceparent},
	} {
		t.Run(name, func(t *testing.T) {
			sink.records = nil
			result := callRaw(t, client, "probe.upstream", map[string]any{"_traceparent": value})
			if result.IsError {
				t.Fatalf("a malformed traceparent is the gateway's bug and must not fail the call: %s", extractText(t, result))
			}
			if len(sink.records) != 1 {
				t.Fatalf("probe recorded %d observations, want 1", len(sink.records))
			}
			if _, present := sink.records[0].Attributes[telemetry.AttrUpstreamTraceID]; present {
				t.Fatalf("a malformed traceparent must not produce an upstream link, got %v", sink.records[0].Attributes)
			}
		})
	}
}

func TestGatewayTraceparentReachesTelemetryAsUpstreamContext(t *testing.T) {
	server := newGatewayTestServer(t)
	client, closeClient := connectInteractionClient(t, server)
	defer closeClient()
	sink := &gatewaySink{}
	recorder := telemetry.New(telemetry.WithSink(sink))
	registerUpstreamProbe(t, server, recorder)

	result := callRaw(t, client, "probe.upstream", map[string]any{"_traceparent": gatewayTraceparent})
	if result.IsError {
		t.Fatalf("probe refused: %s", extractText(t, result))
	}
	if len(sink.records) != 1 {
		t.Fatalf("probe recorded %d observations, want 1", len(sink.records))
	}
	record := sink.records[0]
	if record.TraceID != telemetry.TraceForCheck("gateway-probe").String() {
		t.Fatalf("trace_id = %s; the derived identity must not be replaced by the upstream trace", record.TraceID)
	}
	if record.Attributes[telemetry.AttrUpstreamTraceID] != gatewayTraceHex ||
		record.Attributes[telemetry.AttrUpstreamSpanID] != gatewaySpanHex {
		t.Fatalf("upstream link not recorded, attributes = %v", record.Attributes)
	}

	// The same probe without gateway metadata records no upstream link.
	sink.records = nil
	if result := callRaw(t, client, "probe.upstream", map[string]any{}); result.IsError {
		t.Fatalf("probe refused: %s", extractText(t, result))
	}
	if _, present := sink.records[0].Attributes[telemetry.AttrUpstreamTraceID]; present {
		t.Fatal("no gateway metadata was sent, so no upstream link may be recorded")
	}
}

func TestGatewayMetadataKeySetIsTinyAndDefinedOnce(t *testing.T) {
	want := []string{"_traceparent", "_tracestate"}
	if got := tangentmcp.GatewayMetadataKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("accepted gateway metadata = %v, want exactly %v; adding a key needs the injector's source as evidence", got, want)
	}
}

// registerUpstreamProbe adds a permissive tool whose handler observes the
// request context the middleware produced, by emitting one event through the
// given recorder. It stands in for any Tangent code path that emits telemetry
// while serving a forwarded call.
func registerUpstreamProbe(t *testing.T, server *tangentmcp.Server, recorder *telemetry.Recorder) {
	t.Helper()
	mcpsdk.AddTool(server.MCP(), &mcpsdk.Tool{Name: "probe.upstream", Description: "test probe"},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ map[string]any) (*mcpsdk.CallToolResult, any, error) {
			recorder.Emit(ctx, telemetry.Event{
				Name:        telemetry.EventCapabilityDenied,
				Outcome:     telemetry.OutcomeRefused,
				Correlation: telemetry.Correlation{Trace: telemetry.TraceForCheck("gateway-probe")},
			})
			return nil, map[string]any{"ok": true}, nil
		})
}

type gatewaySink struct{ records []telemetry.Record }

func (s *gatewaySink) Append(_ context.Context, record telemetry.Record) error {
	s.records = append(s.records, record)
	return nil
}
