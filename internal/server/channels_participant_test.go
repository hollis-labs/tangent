package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/channel"
)

// TestChannelSendWorksForARealParticipantSession is the regression guard for
// the bug this test was written to catch: POST /api/channels/{id}/messages
// was gated on authz.Submit, a capability the ADR 0004 §7 matrix's
// KindParticipant row never holds ({view, draft, resolve, cancel} only) —
// participant.Gate.Authorize consults that row unconditionally, so no real
// browser participant session could ever have sent a message through this
// pane. Every other channels test used a fake ChannelService with no
// Participants gate configured at all, so none of them could have caught
// this; only a real, guarded harness with a minted session can.
func TestChannelSendWorksForARealParticipantSession(t *testing.T) {
	t.Parallel()
	app := startGuardedApp(t)
	ctx := context.Background()

	ch, operator, err := app.channels.OpenChannel(ctx, channel.CreateChannelParams{OwnerScope: "standalone-local:anonymous"})
	if err != nil {
		t.Fatalf("OpenChannel: %v", err)
	}
	agent, err := app.channels.UpsertParticipant(ctx, channel.UpsertParticipantParams{
		Kind: channel.ParticipantAgent, ExternalAuthority: "claude-code", ExternalRef: "regression-agent",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}
	_, err = app.channels.AddParticipant(ctx, ch.ID, agent.ID)
	if err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	cookie := app.openDocument(t, "/")
	if cookie == nil {
		t.Fatal("no participant session was minted")
	}

	request, err := http.NewRequest(http.MethodPost, app.baseURL+"/api/channels/"+ch.ID+"/messages",
		strings.NewReader(`{"body":"a real operator, sending through a real session"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST messages: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/channels/%s/messages with a minted participant session = %d, want 200 (not 403 — "+
			"a browser participant can never hold authz.Submit)", ch.ID, response.StatusCode)
	}

	exchanges, err := app.relay.ListForChannel(ctx, ch.ID, 0)
	if err != nil {
		t.Fatalf("ListForChannel: %v", err)
	}
	if len(exchanges) != 1 || exchanges[0].SenderParticipantID != operator.ID {
		t.Fatalf("ListForChannel = %+v, want one exchange sent by the operator", exchanges)
	}

	// Mark-read must also work for a real participant session, for the same
	// reason: it is gated on authz.Draft, which this test pins alongside
	// the send fix so the two routes cannot drift apart again silently.
	readRequest, err := http.NewRequest(http.MethodPost, app.baseURL+"/api/channels/"+ch.ID+"/read", nil)
	if err != nil {
		t.Fatalf("build read request: %v", err)
	}
	readRequest.AddCookie(cookie)
	readResponse, err := http.DefaultClient.Do(readRequest)
	if err != nil {
		t.Fatalf("POST read: %v", err)
	}
	defer func() { _ = readResponse.Body.Close() }()
	if readResponse.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/channels/%s/read with a minted participant session = %d, want 200", ch.ID, readResponse.StatusCode)
	}
}
