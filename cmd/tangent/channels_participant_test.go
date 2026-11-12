package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/access"
)

// Sending through the channel pane works for the signed-in owner and for no one else. The route
// is gated on tangent.draft, which a user session holds and an API key may hold too; the guard
// that keeps an agent out is that the browser routes serve a user session, so a key holding the
// scope is still refused. A real session is what this needs: a fake service with no gate cannot
// see this class of bug.
func TestChannelSendWorksForTheSignedInOwner(t *testing.T) {
	h := newHost(t)
	agentKey := h.key("channel-agent", "tangent.mcp", access.View, access.Draft, access.Submit)
	session := h.mcpSession(agentKey)
	var opened struct {
		ChannelID string `json:"channel_id"`
	}
	callHITL(t, session, "tangent_relay_channel_open", map[string]any{"title": "regression"}, &opened)
	if opened.ChannelID == "" {
		t.Fatal("no channel was opened")
	}
	var attached map[string]any
	callHITL(t, session, "tangent_relay_attach", map[string]any{
		"channel_id": opened.ChannelID,
		"source":     map[string]any{"application_id": "claude-code", "agent_id": "regression-agent"},
		"runtime":    map[string]any{"authority": "claude-code"},
	}, &attached)

	send := "/api/channels/" + opened.ChannelID + "/messages"
	if got := h.call("", "POST", send); got != http.StatusUnauthorized {
		t.Errorf("an anonymous send = %d, want 401", got)
	}
	if got := h.call(agentKey, "POST", send); got != http.StatusForbidden {
		t.Errorf("a send with an agent key that holds tangent.draft = %d, want 403", got)
	}

	h.ownerDo("POST", send, map[string]any{"body": "a real operator, sending through a real session"})
	code, raw := h.ownerRaw("GET", "/api/channels/"+opened.ChannelID, nil)
	var detail struct {
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &detail) != nil || len(detail.Messages) != 1 ||
		!strings.Contains(detail.Messages[0].Body, "a real operator") {
		t.Fatalf("the channel after the send = %d %s", code, raw)
	}
	h.ownerDo("POST", "/api/channels/"+opened.ChannelID+"/read", nil)
}
