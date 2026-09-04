package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const regressionWait = 5 * time.Second

// browserEvent is emitted by scripts/room-lifecycle-e2e.ts. That driver imports
// the same ws-client and room-lifecycle modules used by Room.tsx, while this Go
// test owns the real MCP session, Room manager, and WebSocket handler.
type browserEvent struct {
	Event        string `json:"event"`
	Browser      string `json:"browser"`
	RoomID       string `json:"roomID"`
	Status       string `json:"status"`
	EnvelopeID   string `json:"envelopeID"`
	EnvelopeType string `json:"envelopeType"`
	Revision     int64  `json:"revision"`
	Message      string `json:"message"`
	Reason       string `json:"reason"`
	Switched     bool   `json:"switched"`
	Submitted    bool   `json:"submitted"`
	Cancelled    bool   `json:"cancelled"`
}

type productionBrowserDriver struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	events  chan browserEvent
	done    chan error
	stderr  lockedBuffer
	writeMu sync.Mutex
	backlog []browserEvent
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func startProductionBrowserDriver(t *testing.T) *productionBrowserDriver {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	viteNode := filepath.Join(repoRoot, "ui", "node_modules", "vite-node", "vite-node.mjs")
	if _, statErr := os.Stat(viteNode); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			t.Skip("production SPA lifecycle regression requires ui/node_modules; run through make test after npm install")
		}
		t.Fatalf("stat vite-node: %v", statErr)
	}

	script := filepath.Join(repoRoot, "scripts", "room-lifecycle-e2e.ts")
	// Paths are derived from the checked-out repository, not user input.
	//nolint:gosec
	cmd := exec.Command("node", viteNode, script)
	cmd.Dir = repoRoot
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("SPA driver stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("SPA driver stdout: %v", err)
	}
	driver := &productionBrowserDriver{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan browserEvent, 64),
		done:   make(chan error, 1),
	}
	cmd.Stderr = &driver.stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skip("production SPA lifecycle regression requires supported Node on PATH")
		}
		t.Fatalf("start SPA driver: %v", err)
	}

	go driver.readEvents(stdout)
	go func() {
		driver.done <- cmd.Wait()
		close(driver.done)
	}()
	driver.await(t, func(event browserEvent) bool { return event.Event == "ready" }, "SPA driver ready")
	t.Cleanup(driver.stop)
	return driver
}

func (d *productionBrowserDriver) readEvents(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event browserEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			d.events <- browserEvent{Event: "driver-error", Message: fmt.Sprintf("decode %q: %v", scanner.Text(), err)}
			continue
		}
		d.events <- event
	}
	if err := scanner.Err(); err != nil {
		d.events <- browserEvent{Event: "driver-error", Message: "read driver stdout: " + err.Error()}
	}
	close(d.events)
}

func (d *productionBrowserDriver) stop() {
	_ = d.send(map[string]any{"command": "shutdown"})
	_ = d.stdin.Close()
	select {
	case <-d.done:
	case <-time.After(2 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.done
	}
}

func (d *productionBrowserDriver) send(command map[string]any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = d.stdin.Write(append(raw, '\n'))
	return err
}

func (d *productionBrowserDriver) command(t *testing.T, command map[string]any) {
	t.Helper()
	if err := d.send(command); err != nil {
		t.Fatalf("send SPA driver command %v: %v", command, err)
	}
}

func (d *productionBrowserDriver) await(t *testing.T, match func(browserEvent) bool, description string) browserEvent {
	t.Helper()
	for i, event := range d.backlog {
		if match(event) {
			d.backlog = append(d.backlog[:i], d.backlog[i+1:]...)
			return event
		}
	}

	timer := time.NewTimer(regressionWait)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-d.events:
			if !ok {
				t.Fatalf("SPA driver exited while waiting for %s; stderr: %s", description, d.stderr.String())
			}
			if event.Event == "driver-error" || event.Event == "browser-error" {
				t.Fatalf("SPA driver error while waiting for %s: %s; stderr: %s", description, event.Message, d.stderr.String())
			}
			if match(event) {
				return event
			}
			d.backlog = append(d.backlog, event)
		case err := <-d.done:
			t.Fatalf("SPA driver exited while waiting for %s: %v; stderr: %s", description, err, d.stderr.String())
		case <-timer.C:
			t.Fatalf("timed out waiting for %s; backlog=%+v stderr=%s", description, d.backlog, d.stderr.String())
		}
	}
}

func (d *productionBrowserDriver) awaitBrowser(t *testing.T, browser, event string) browserEvent {
	t.Helper()
	return d.await(t, func(got browserEvent) bool {
		return got.Browser == browser && got.Event == event
	}, browser+" "+event)
}

func (d *productionBrowserDriver) open(t *testing.T, browser, roomID, wsURL string) browserEvent {
	t.Helper()
	d.command(t, map[string]any{
		"command": "open",
		"browser": browser,
		"roomID":  roomID,
		"wsURL":   wsURL,
	})
	_ = d.awaitBrowser(t, browser, "open-started")
	return d.awaitBrowser(t, browser, "opened")
}

func (d *productionBrowserDriver) switchRoom(t *testing.T, browser, roomID string) browserEvent {
	t.Helper()
	d.command(t, map[string]any{"command": "switch", "browser": browser, "roomID": roomID})
	switched := d.awaitBrowser(t, browser, "switched")
	if !switched.Switched {
		t.Fatalf("production lifecycle declined room switch to %s", roomID)
	}
	return d.awaitBrowser(t, browser, "opened")
}

func (d *productionBrowserDriver) receive(t *testing.T, browser, envelopeID string) browserEvent {
	t.Helper()
	event := d.awaitBrowser(t, browser, "envelope")
	if event.EnvelopeID != envelopeID {
		t.Fatalf("%s received envelope %q, want %q", browser, event.EnvelopeID, envelopeID)
	}
	if event.Revision <= 0 {
		t.Fatalf("%s received invalid presentation revision %d", browser, event.Revision)
	}
	return event
}

func (d *productionBrowserDriver) submit(t *testing.T, browser, marker string) browserEvent {
	t.Helper()
	d.command(t, map[string]any{"command": "submit", "browser": browser, "marker": marker})
	event := d.awaitBrowser(t, browser, "submitted")
	if !event.Submitted || event.EnvelopeID != "" || event.Status != "response submitted" {
		t.Fatalf("%s submit state = %+v", browser, event)
	}
	return event
}

func (d *productionBrowserDriver) submitRevision(t *testing.T, browser, marker string, revision int64) browserEvent {
	t.Helper()
	d.command(t, map[string]any{
		"command":  "submit-revision",
		"browser":  browser,
		"marker":   marker,
		"revision": revision,
	})
	return d.awaitBrowser(t, browser, "revision-submitted")
}

func (d *productionBrowserDriver) cancel(t *testing.T, browser string) browserEvent {
	t.Helper()
	d.command(t, map[string]any{"command": "cancel", "browser": browser})
	event := d.awaitBrowser(t, browser, "cancelled")
	if !event.Cancelled || event.EnvelopeID != "" || event.Status != "cancelled" {
		t.Fatalf("%s cancel state = %+v", browser, event)
	}
	return event
}

func (d *productionBrowserDriver) unload(t *testing.T, browser string) browserEvent {
	t.Helper()
	d.command(t, map[string]any{"command": "unload", "browser": browser})
	return d.awaitBrowser(t, browser, "unloaded")
}

func (d *productionBrowserDriver) abortProcess(t *testing.T) {
	t.Helper()
	d.command(t, map[string]any{"command": "abort-process"})
	select {
	case err := <-d.done:
		if err == nil {
			t.Fatal("abrupt SPA driver exit unexpectedly returned success")
		}
	case <-time.After(regressionWait):
		t.Fatal("timed out waiting for abrupt SPA driver exit")
	}
}

type regressionToolOutcome struct {
	result *mcpsdk.CallToolResult
	err    error
}

func startRegressionTriageCall(rg *rig, roomID, envelopeID string) <-chan regressionToolOutcome {
	done := make(chan regressionToolOutcome, 1)
	envelopeArg := triageEnvelopeArg(envelopeID)
	envelopeArg["meta"] = map[string]any{"roomID": roomID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "tangent.triage",
			Arguments: map[string]any{"envelope": envelopeArg},
		})
		done <- regressionToolOutcome{result: result, err: err}
	}()
	return done
}

// TestRegression_ProductionRoomSwitchResumesOriginatingCall invokes the exact
// lifecycle.switchRoom and ws-client.switchRoom methods used by Room.tsx. Work
// in the first room survives the click-away and is replayed when revisited.
func TestRegression_ProductionRoomSwitchResumesOriginatingCall(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	roomA := rg.mgr.Create(map[string]string{"scenario": "switch-a"})
	roomB := rg.mgr.Create(map[string]string{"scenario": "switch-b"})
	opened := driver.open(t, "browser", roomA.ID, regressionWSURL(rg))
	assertBrowserState(t, opened, roomA.ID, "connected", "")

	callA := startRegressionTriageCall(rg, roomA.ID, "reg-switch-a")
	firstA := driver.receive(t, "browser", "reg-switch-a")
	callB := startRegressionTriageCall(rg, roomB.ID, "reg-switch-b")
	switched := driver.switchRoom(t, "browser", roomB.ID)
	assertBrowserState(t, switched, roomB.ID, "connected", "")
	driver.receive(t, "browser", "reg-switch-b")
	driver.submit(t, "browser", "browser-b")

	assertRegressionSubmitted(t, receiveRegressionOutcome(t, callB), "reg-switch-b", "browser-b")
	reopened := driver.switchRoom(t, "browser", roomA.ID)
	assertBrowserState(t, reopened, roomA.ID, "connected", "")
	replayedA := driver.receive(t, "browser", "reg-switch-a")
	if replayedA.Revision <= firstA.Revision {
		t.Fatalf("room A replay revision = %d, first = %d", replayedA.Revision, firstA.Revision)
	}
	driver.submit(t, "browser", "browser-a-resumed")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, callA), "reg-switch-a", "browser-a-resumed")
	if roomA.IsClosed() || roomB.IsClosed() {
		t.Fatalf("route switch closed durable rooms: A=%v B=%v", roomA.IsClosed(), roomB.IsClosed())
	}
}

// TestRegression_ProductionSameRoomRefreshReplacement exercises two production
// clients connecting to one room while work is pending. The replacement gets
// an immediate revisioned replay and resolves the original call.
func TestRegression_ProductionSameRoomRefreshReplacement(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	rm := rg.mgr.Create(map[string]string{"scenario": "refresh"})
	driver.open(t, "old-page", rm.ID, regressionWSURL(rg))
	call := startRegressionTriageCall(rg, rm.ID, "reg-refresh")
	first := driver.receive(t, "old-page", "reg-refresh")

	refreshed := driver.open(t, "refreshed-page", rm.ID, regressionWSURL(rg))
	assertBrowserState(t, refreshed, rm.ID, "connected", "")
	closedOld := driver.awaitBrowser(t, "old-page", "closed")
	assertBrowserState(t, closedOld, rm.ID, "disconnected: replaced", "")
	replayed := driver.receive(t, "refreshed-page", "reg-refresh")
	if replayed.Revision <= first.Revision {
		t.Fatalf("refresh replay revision = %d, first = %d", replayed.Revision, first.Revision)
	}
	oldUnloaded := driver.unload(t, "old-page")
	assertBrowserState(t, oldUnloaded, rm.ID, "unloading", "")
	if rm.IsClosed() || !rm.HasConn() || !rm.HasPending() {
		t.Fatalf("same-room replacement state: closed=%v connected=%v pending=%v", rm.IsClosed(), rm.HasConn(), rm.HasPending())
	}

	driver.submit(t, "refreshed-page", "refreshed-page")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, call), "reg-refresh", "refreshed-page")
}

// TestRegression_ProductionNormalUnloadResumes verifies that the production
// beforeunload/unmount path is connection-only and a reopened browser resumes.
func TestRegression_ProductionNormalUnloadResumes(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	rm := rg.mgr.Create(map[string]string{"scenario": "normal-unload"})
	driver.open(t, "browser", rm.ID, regressionWSURL(rg))
	call := startRegressionTriageCall(rg, rm.ID, "reg-normal-unload")
	first := driver.receive(t, "browser", "reg-normal-unload")
	unloaded := driver.unload(t, "browser")
	assertBrowserState(t, unloaded, rm.ID, "unloading", "")
	driver.open(t, "reopened-browser", rm.ID, regressionWSURL(rg))
	replayed := driver.receive(t, "reopened-browser", "reg-normal-unload")
	if replayed.Revision <= first.Revision {
		t.Fatalf("normal unload replay revision = %d, first = %d", replayed.Revision, first.Revision)
	}
	driver.submit(t, "reopened-browser", "normal-unload-resumed")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, call), "reg-normal-unload", "normal-unload-resumed")
}

// TestRegression_ProductionAbruptTransportLoss does not dispatch beforeunload.
// It has the same durable reconnect semantics and remains distinct from an
// explicit cancellation.
func TestRegression_ProductionAbruptTransportLoss(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	firstDriver := startProductionBrowserDriver(t)

	rm := rg.mgr.Create(map[string]string{"scenario": "transport-loss"})
	firstDriver.open(t, "browser", rm.ID, regressionWSURL(rg))
	call := startRegressionTriageCall(rg, rm.ID, "reg-transport-loss")
	first := firstDriver.receive(t, "browser", "reg-transport-loss")
	firstDriver.abortProcess(t)
	awaitRegressionState(t, "abrupt transport detachment", func() bool { return !rm.HasConn() })
	if rm.IsClosed() || !rm.HasPending() {
		t.Fatalf("abrupt transport changed durable work: closed=%v pending=%v", rm.IsClosed(), rm.HasPending())
	}

	reconnectedDriver := startProductionBrowserDriver(t)
	reconnectedDriver.open(t, "reconnected-browser", rm.ID, regressionWSURL(rg))
	replayed := reconnectedDriver.receive(t, "reconnected-browser", "reg-transport-loss")
	if replayed.Revision <= first.Revision {
		t.Fatalf("transport-loss replay revision = %d, first = %d", replayed.Revision, first.Revision)
	}
	reconnectedDriver.submit(t, "reconnected-browser", "transport-resumed")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, call), "reg-transport-loss", "transport-resumed")
}

func TestRegression_ProductionExplicitCancelIsNotConnectionLoss(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	rm := rg.mgr.Create(map[string]string{"scenario": "explicit-cancel"})
	driver.open(t, "browser", rm.ID, regressionWSURL(rg))
	call := startRegressionTriageCall(rg, rm.ID, "reg-explicit-cancel")
	driver.receive(t, "browser", "reg-explicit-cancel")
	driver.cancel(t, "browser")
	assertRegressionCancelled(t, receiveRegressionOutcome(t, call), "reg-explicit-cancel")
	awaitRegressionState(t, "explicit cancel to clear pending", func() bool { return !rm.HasPending() })
	if rm.IsClosed() || !rm.HasConn() {
		t.Fatalf("explicit cancellation changed connection lifecycle: closed=%v connected=%v", rm.IsClosed(), rm.HasConn())
	}
}

func TestRegression_ProductionTwoBrowserTabsRejectStaleRevision(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	rm := rg.mgr.Create(map[string]string{"scenario": "two-tabs"})
	driver.open(t, "first-tab", rm.ID, regressionWSURL(rg))
	call := startRegressionTriageCall(rg, rm.ID, "reg-two-tabs")
	firstPresentation := driver.receive(t, "first-tab", "reg-two-tabs")
	second := driver.open(t, "second-tab", rm.ID, regressionWSURL(rg))
	assertBrowserState(t, second, rm.ID, "connected", "")
	firstClosed := driver.awaitBrowser(t, "first-tab", "closed")
	assertBrowserState(t, firstClosed, rm.ID, "disconnected: replaced", "")
	secondPresentation := driver.receive(t, "second-tab", "reg-two-tabs")
	if secondPresentation.Revision <= firstPresentation.Revision {
		t.Fatalf("second-tab revision = %d, first-tab = %d", secondPresentation.Revision, firstPresentation.Revision)
	}

	// Send a response carrying the replaced tab's revision through the real
	// production client. The server must reject it and issue a fresh resync.
	driver.submitRevision(t, "second-tab", "stale-first-tab", firstPresentation.Revision)
	resynchronized := driver.receive(t, "second-tab", "reg-two-tabs")
	if resynchronized.Revision <= secondPresentation.Revision {
		t.Fatalf("resync revision = %d, second presentation = %d", resynchronized.Revision, secondPresentation.Revision)
	}
	driver.submit(t, "second-tab", "second-tab")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, call), "reg-two-tabs", "second-tab")
}

func TestRegression_ProductionTwoConcurrentCallersRemainIsolated(t *testing.T) {
	rg := newRig(t)
	defer rg.cleanup()
	driver := startProductionBrowserDriver(t)

	roomA := rg.mgr.Create(map[string]string{"scenario": "caller-a"})
	roomB := rg.mgr.Create(map[string]string{"scenario": "caller-b"})
	driver.open(t, "browser-a", roomA.ID, regressionWSURL(rg))
	driver.open(t, "browser-b", roomB.ID, regressionWSURL(rg))
	callA := startRegressionTriageCall(rg, roomA.ID, "reg-caller-a")
	callB := startRegressionTriageCall(rg, roomB.ID, "reg-caller-b")
	driver.receive(t, "browser-a", "reg-caller-a")
	driver.receive(t, "browser-b", "reg-caller-b")
	driver.submit(t, "browser-b", "response-b")
	driver.submit(t, "browser-a", "response-a")

	assertRegressionSubmitted(t, receiveRegressionOutcome(t, callA), "reg-caller-a", "response-a")
	assertRegressionSubmitted(t, receiveRegressionOutcome(t, callB), "reg-caller-b", "response-b")
	if roomA.IsClosed() || roomB.IsClosed() || !roomA.HasConn() || !roomB.HasConn() {
		t.Fatalf("concurrent room state: A(closed=%v connected=%v) B(closed=%v connected=%v)", roomA.IsClosed(), roomA.HasConn(), roomB.IsClosed(), roomB.HasConn())
	}
}

func assertBrowserState(t *testing.T, event browserEvent, roomID, status, envelopeID string) {
	t.Helper()
	if event.RoomID != roomID || event.Status != status || event.EnvelopeID != envelopeID {
		t.Fatalf("browser state = room %q status %q envelope %q, want room %q status %q envelope %q", event.RoomID, event.Status, event.EnvelopeID, roomID, status, envelopeID)
	}
}

func regressionWSURL(rg *rig) string {
	return "ws" + strings.TrimPrefix(rg.httpURL, "http")
}

func receiveRegressionOutcome(t *testing.T, done <-chan regressionToolOutcome) regressionToolOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(regressionWait):
		t.Fatal("originating MCP call did not finish")
		return regressionToolOutcome{}
	}
}

func assertRegressionCancelled(t *testing.T, outcome regressionToolOutcome, envelopeID string) {
	t.Helper()
	if outcome.err != nil {
		t.Fatalf("%s MCP transport error: %v", envelopeID, outcome.err)
	}
	if outcome.result == nil || outcome.result.IsError {
		t.Fatalf("%s cancel returned tool error: %q", envelopeID, textOf(outcome.result))
	}
	var response envelopes.Response
	if err := json.Unmarshal([]byte(textOf(outcome.result)), &response); err != nil {
		t.Fatalf("%s unmarshal cancel response: %v", envelopeID, err)
	}
	if response.EnvelopeID != envelopeID || response.Kind != envelopes.ResponseKindAck || response.Status != envelopes.ResponseStatusCancelled {
		t.Fatalf("%s cancel result = id %q kind %q status %q, want ack/cancelled", envelopeID, response.EnvelopeID, response.Kind, response.Status)
	}
}

func assertRegressionSubmitted(t *testing.T, outcome regressionToolOutcome, envelopeID, marker string) {
	t.Helper()
	if outcome.err != nil {
		t.Fatalf("%s MCP transport error: %v", envelopeID, outcome.err)
	}
	if outcome.result == nil || outcome.result.IsError {
		t.Fatalf("%s returned tool error: %q", envelopeID, textOf(outcome.result))
	}
	var response envelopes.Response
	if err := json.Unmarshal([]byte(textOf(outcome.result)), &response); err != nil {
		t.Fatalf("%s unmarshal MCP result: %v", envelopeID, err)
	}
	payload, ok := response.Payload.(map[string]any)
	if response.EnvelopeID != envelopeID || response.Status != envelopes.ResponseStatusSubmitted || !ok || payload["browser"] != marker {
		t.Fatalf("%s result = id %q status %q payload %v, want submitted payload browser=%q", envelopeID, response.EnvelopeID, response.Status, response.Payload, marker)
	}
}

func awaitRegressionState(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(regressionWait)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}
