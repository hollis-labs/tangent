package channelpane

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// assertNoNilSliceMarshalsToNull is a check over the response type's shape,
// not one case per field: it walks every slice-typed field on value via
// reflection and fails if that field's JSON came back null. A nil Go slice
// and an empty one both marshal correctly by Go's own rules — nil becomes
// JSON null, make([]T, 0) becomes [] — so this is purely about which one a
// handler actually constructed, and it catches the next field that gets a
// bare `var x []T` instead of make(...) the same way it caught hitlItems.
func assertNoNilSliceMarshalsToNull(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal into map: %v (body %s)", err, raw)
	}

	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.Type.Kind() != reflect.Slice {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if fieldRaw, ok := asMap[name]; ok && string(fieldRaw) == "null" {
			t.Errorf("%s.%s (json %q) marshaled as null, want [] — a nil slice reached a JSON response", rt.Name(), field.Name, name)
		}
	}
}

// TestGetChannelWithNoHITLItemsSerializesAnEmptyArrayNotNull is the exact
// bug the phase 4 acceptance run found live: Chrispian accepting Alpha's
// one pending HITL item emptied its list to zero, which turned hitl_items
// into JSON null (from `var hitlItems []HITLItem`) rather than [], and
// ChannelPane.tsx's unconditional `detail.hitl_items.length` threw,
// blanking the whole SPA — not only the channel pane, every room too,
// since the render crashed above any route-specific boundary. No test
// caught it because every seeded scenario and every existing fixture had
// at least one HITL item; the empty case was never exercised.
func TestGetChannelWithNoHITLItemsSerializesAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()
	channelID, _, _ := h.seedChannel(t, "claude-code", "agent-a")

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	detail, err := svc.GetChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if detail.HITLItems == nil {
		t.Fatal("GetChannel with zero HITL items returned a nil HITLItems slice in Go — the JSON check below would still catch this, but a nil check here pins the actual invariant, not just its JSON symptom")
	}
	assertNoNilSliceMarshalsToNull(t, detail)
}

// TestListChannelsWithNoChannelsSerializesAnEmptyArrayNotNull is the same
// property for the channel list response, exercised the same way: nothing
// seeded, so ListChannels' own slice starts from zero.
func TestListChannelsWithNoChannelsSerializesAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	h := openTestHarness(t)
	ctx := context.Background()

	svc, err := New(h.channels, h.relay, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	summaries, err := svc.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if summaries == nil {
		t.Fatal("ListChannels with zero channels returned a nil slice in Go")
	}
	raw, err := json.Marshal(map[string]any{"channels": summaries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"channels":[]}` {
		t.Fatalf(`ListChannels wrapper marshaled as %s, want {"channels":[]}`, raw)
	}
}
