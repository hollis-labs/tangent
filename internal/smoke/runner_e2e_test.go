package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/smoke"
)

// TestInstalledRunnerCarriesATurnThroughTheInbox is the guard the runner's
// in-tree integration tests used to be (CW-20260930-0102): the REAL runner
// plugin, installed from its pinned source fixture, in front of the REAL turns tools
// of the shipped binary.
//
// The runner's own tests use a caller that accepts any tool name, so they would
// pass against a tool that no longer exists. This one launches the runner with
// `cat` as the agent — cat echoes what the runner writes to it, so an answer
// the runner delivers comes straight back out as the agent's next turn — and
// follows one turn all the way round: it reaches the inbox through
// tangent.turns_enqueue, the operator answers it through the browser API, the
// runner finds the answer through tangent.turn_await, writes it to the agent,
// and acknowledges it through tangent.turn_ack.
func TestInstalledRunnerCarriesATurnThroughTheInbox(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the shipped binary with its installed plugins; skipped under -short")
	}
	endpoint, _ := bootShippedBinaryWithPluginDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	session := mcpSession(ctx, t, endpoint)
	operator := newOperator(ctx, t, endpoint)

	const sessionID = "smoke-runner"
	launched := callTool(ctx, t, session, "tangent.runner_launch", map[string]any{
		"session_id": sessionID, "agent_id": "agent-smoke", "agent_label": "Smoke",
		"prompt":  "Please confirm you want to proceed with the deployment?",
		"command": "cat", "lifecycle": "streaming_stdio",
	})
	t.Logf("runner_launch: %s", launched)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_, _ = session.CallTool(stopCtx, &mcpsdk.CallToolParams{
			Name: "tangent.runner_stop", Arguments: map[string]any{"session_id": sessionID},
		})
	})

	// The runner's turn reaches the inbox through the real tool, carrying the
	// identity the runner stamps on it.
	first := operator.nextPresented(ctx, t, sessionID)
	if first.AgentID != "agent-smoke" || first.ApplicationID != "runner" {
		t.Errorf("runner turn identity = agent %q application %q", first.AgentID, first.ApplicationID)
	}
	if first.Kind != "approval" || !strings.Contains(first.Content, "confirm you want to proceed") {
		t.Errorf("runner turn = kind %q content %q", first.Kind, first.Content)
	}

	// The operator answers it in the inbox.
	operator.reply(ctx, t, first, "proceed")

	// The answer reached the agent if the agent's next turn is the answer.
	echoed := operator.nextPresented(ctx, t, sessionID, first.ItemID)
	if echoed.Content != "proceed" {
		t.Errorf("the agent received %q, want the operator's answer %q", echoed.Content, "proceed")
	}
	if echoed.TurnID == first.TurnID {
		t.Errorf("the agent's reply reused turn_id %q: the answer did not advance the turn", first.TurnID)
	}

	// And the runner acknowledged it.
	operator.waitFor(ctx, t, "the runner to acknowledge the delivered answer", func() bool {
		item, err := operator.item(ctx, first.ItemID)
		return err == nil && item.DeliveryState == "acknowledged"
	})
}

// turnItem is the slice of the turns inbox's item view this test reads.
type turnItem struct {
	ItemID        string `json:"item_id"`
	TurnID        string `json:"turn_id"`
	SessionID     string `json:"session_id"`
	AgentID       string `json:"agent_id"`
	ApplicationID string `json:"application_id"`
	Kind          string `json:"kind"`
	Content       string `json:"content"`
	State         string `json:"state"`
	Revision      int64  `json:"revision"`
	DeliveryState string `json:"delivery_state"`
}

func mcpSession(ctx context.Context, t *testing.T, endpoint smoke.Endpoint) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tangent-smoke", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: endpoint.BaseURL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect to %s/mcp: %v", endpoint.BaseURL, err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(ctx context.Context, t *testing.T, session *mcpsdk.ClientSession, name string, arguments map[string]any) string {
	t.Helper()
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcpsdk.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	if result.IsError {
		t.Fatalf("%s refused: %s", name, text.String())
	}
	return text.String()
}

// operator is the browser side of the turns inbox: a participant session minted
// by a same-origin document navigation, exactly as a browser gets one.
type operator struct {
	base   string
	client *http.Client
}

func newOperator(ctx context.Context, t *testing.T, endpoint smoke.Endpoint) *operator {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	o := &operator{base: endpoint.BaseURL, client: &http.Client{Jar: jar, Timeout: 15 * time.Second}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/turns", nil)
	if err != nil {
		t.Fatalf("build navigation: %v", err)
	}
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Sec-Fetch-Site", "none")
	request.Header.Set("Accept", "text/html")
	response, err := o.client.Do(request)
	if err != nil {
		t.Fatalf("document navigation: %v", err)
	}
	_ = response.Body.Close()
	if len(jar.Cookies(request.URL)) == 0 {
		t.Fatalf("a document navigation to %s minted no participant session", request.URL)
	}
	return o
}

func (o *operator) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, o.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		request.Header.Set("Origin", o.base)
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := o.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, response.Status, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (o *operator) item(ctx context.Context, itemID string) (turnItem, error) {
	var item turnItem
	err := o.do(ctx, http.MethodGet, "/api/turns/items/"+itemID, nil, &item)
	return item, err
}

// nextPresented waits for a turn from sessionID, other than any in seen, to be
// presented in the inbox. An item is listed while it is still being staged; the
// operator acts on it once it is presented.
func (o *operator) nextPresented(ctx context.Context, t *testing.T, sessionID string, seen ...string) turnItem {
	t.Helper()
	var found turnItem
	o.waitFor(ctx, t, "a turn from "+sessionID+" to reach the inbox", func() bool {
		var inbox struct {
			Pending []turnItem `json:"pending"`
		}
		if err := o.do(ctx, http.MethodGet, "/api/turns", nil, &inbox); err != nil {
			return false
		}
	candidates:
		for _, item := range inbox.Pending {
			if item.SessionID != sessionID || item.State != "presented" {
				continue
			}
			for _, id := range seen {
				if item.ItemID == id {
					continue candidates
				}
			}
			found = item
			return true
		}
		return false
	})
	return found
}

func (o *operator) reply(ctx context.Context, t *testing.T, item turnItem, text string) {
	t.Helper()
	body := map[string]any{"expected_revision": item.Revision, "action": "approve", "response_text": text}
	if err := o.do(ctx, http.MethodPost, "/api/turns/items/"+item.ItemID+"/reply", body, nil); err != nil {
		t.Fatalf("operator reply: %v", err)
	}
}

func (o *operator) waitFor(ctx context.Context, t *testing.T, what string, done func() bool) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if done() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}
