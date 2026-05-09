package room_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	envelopes "github.com/hollis-labs/go-envelopes"
	"go.uber.org/goleak"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
	"github.com/hollis-labs/tangent/internal/room"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newTestServer(t *testing.T) (*room.Room, *websocket.Conn, *sql.DB, func()) {
	t.Helper()

	db := newTestDB(t)
	rm := newAnonRoom(t, db)

	connCh := make(chan *websocket.Conn, 1)
	stopCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			t.Errorf("server: ws accept: %v", err)
			return
		}
		connCh <- c
		select {
		case <-stopCh:
		case <-r.Context().Done():
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{})
	if err != nil {
		srv.Close()
		t.Fatalf("client dial: %v", err)
	}

	serverConn := <-connCh
	rm.AttachConn(context.Background(), serverConn)

	readDone := make(chan struct{})
	readCtx, readCancel := context.WithCancel(context.Background())
	go runRoomReadLoop(readCtx, rm, serverConn, readDone)

	cleanup := func() {
		close(stopCh)
		readCancel()
		_ = clientConn.Close(websocket.StatusNormalClosure, "test done")
		_ = serverConn.Close(websocket.StatusNormalClosure, "test done")
		srv.Close()
		<-readDone
		_ = tangentdb.Close(db)
	}
	return rm, clientConn, db, cleanup
}

func runRoomReadLoop(ctx context.Context, rm *room.Room, conn *websocket.Conn, done chan<- struct{}) {
	defer close(done)
	for {
		mt, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if mt != websocket.MessageText {
			continue
		}
		var msg struct {
			Type       string                 `json:"type"`
			EnvelopeID string                 `json:"envelopeId"`
			Response   map[string]interface{} `json:"response"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "response":
			if msg.EnvelopeID == "" {
				continue
			}
			raw, _ := json.Marshal(msg.Response)
			var resp envelopes.Response
			_ = json.Unmarshal(raw, &resp)
			rm.HandleResponse(msg.EnvelopeID, &resp)
		case "cancel":
			rm.HandleCancel(msg.EnvelopeID)
		}
	}
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := tangentdb.Open(t.TempDir() + "/tangent.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := tangentdb.RunMigrations(db); err != nil {
		_ = tangentdb.Close(db)
		t.Fatalf("db.RunMigrations: %v", err)
	}
	return db
}

func newAnonRoom(t *testing.T, db *sql.DB) *room.Room {
	t.Helper()
	mgr := room.NewManager(db)
	return mgr.Create(map[string]string{"test": t.Name()})
}

func TestManager_CreateConcurrent(t *testing.T) {
	mgr := room.NewManager(nil)
	const n = 64

	var wg sync.WaitGroup
	wg.Add(n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			r := mgr.Create(nil)
			ids[i] = r.ID
		}(i)
	}
	wg.Wait()

	if mgr.Len() != n {
		t.Fatalf("expected %d rooms, got %d", n, mgr.Len())
	}
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if id == "" {
			t.Error("empty room id")
			continue
		}
		if seen[id] {
			t.Errorf("duplicate room id: %s", id)
		}
		seen[id] = true
	}
}

func TestManager_Get(t *testing.T) {
	mgr := room.NewManager(nil)
	if _, ok := mgr.Get("nope"); ok {
		t.Error("expected miss for unknown id")
	}
	r := mgr.Create(nil)
	got, ok := mgr.Get(r.ID)
	if !ok || got != r {
		t.Errorf("expected hit for created room, got ok=%v got==r=%v", ok, got == r)
	}
}

func TestManager_ListInMemoryActiveOnlySkipsClosedRooms(t *testing.T) {
	mgr := room.NewManager(nil)
	openRoom := mgr.Create(map[string]string{"title": "open"})
	closedRoom := mgr.Create(map[string]string{"title": "closed"})
	closedRoom.Close("closed for test")

	activeOnly, err := mgr.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List(activeOnly=true): %v", err)
	}
	if len(activeOnly) != 1 {
		t.Fatalf("List(activeOnly=true) len=%d, want 1", len(activeOnly))
	}
	if activeOnly[0].ID != openRoom.ID {
		t.Fatalf("active room id=%q, want %q", activeOnly[0].ID, openRoom.ID)
	}

	allRooms, err := mgr.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List(activeOnly=false): %v", err)
	}
	if len(allRooms) != 2 {
		t.Fatalf("List(activeOnly=false) len=%d, want 2", len(allRooms))
	}
	foundClosed := false
	for _, summary := range allRooms {
		if summary.ID == closedRoom.ID {
			foundClosed = true
			break
		}
	}
	if !foundClosed {
		t.Fatalf("closed room %q missing from full list", closedRoom.ID)
	}
}

func TestRoom_PushResponseRoundTrip(t *testing.T) {
	rm, clientConn, db, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-1", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	frame := readClientFrame(t, clientConn, 2*time.Second)
	if frame["type"] != "envelope" {
		t.Fatalf("expected envelope frame, got %+v", frame)
	}
	if frame["envelopeId"] != "e-1" {
		t.Fatalf("envelopeId mismatch: %v", frame["envelopeId"])
	}

	writeClientFrame(t, clientConn, map[string]any{
		"type":       "response",
		"envelopeId": "e-1",
		"response": map[string]any{
			"v":          1,
			"envelopeId": "e-1",
			"kind":       "data",
			"status":     "submitted",
			"payload":    map[string]any{"accepted": true},
		},
	})

	res := <-pushDone
	if res.err != nil {
		t.Fatalf("Push returned error: %v", res.err)
	}
	if res.resp == nil {
		t.Fatal("Push returned nil response")
	}
	if res.resp.Kind != envelopes.ResponseKindData {
		t.Errorf("response kind = %q, want data", res.resp.Kind)
	}

	assertEnvelopeStatus(t, db, rm.ID, env.ID, "submitted", "", true)
}

func TestRoom_PushCancel(t *testing.T) {
	rm, clientConn, db, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-cancel", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)
	writeClientFrame(t, clientConn, map[string]any{
		"type":       "cancel",
		"envelopeId": "e-cancel",
	})

	res := <-pushDone
	if res.err == nil {
		t.Fatalf("expected error from cancel, got resp=%+v", res.resp)
	}
	if !errors.Is(res.err, room.ErrUserCancelled) {
		t.Errorf("expected ErrUserCancelled, got %v", res.err)
	}

	assertEnvelopeStatus(t, db, rm.ID, env.ID, "cancelled", envelopes.ErrorCodeUserCancelled, true)
}

func TestRoom_PushDisconnect(t *testing.T) {
	rm, clientConn, db, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-drop", Type: "triage"}

	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)
	_ = clientConn.Close(websocket.StatusGoingAway, "closing")
	rm.Close("client disconnected")

	select {
	case res := <-pushDone:
		if res.err == nil {
			t.Fatal("expected error from disconnect, got nil")
		}
		if !errors.Is(res.err, room.ErrRoomDisconnected) {
			t.Errorf("expected ErrRoomDisconnected, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push hung after disconnect")
	}

	assertEnvelopeStatus(t, db, rm.ID, env.ID, "error", "ROOM_DISCONNECTED", true)
}

func TestRoom_PushCtxCancel(t *testing.T) {
	rm, clientConn, db, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-ctx", Type: "triage"}

	ctx, cancel := context.WithCancel(context.Background())
	pushDone := make(chan pushResult, 1)
	go func() {
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	_ = readClientFrame(t, clientConn, 2*time.Second)
	cancel()

	select {
	case res := <-pushDone:
		if !errors.Is(res.err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push did not honor ctx cancel")
	}

	assertEnvelopeStatus(t, db, rm.ID, env.ID, "error", envelopes.ErrorCodeUserCancelled, true)
}

func TestManager_Hydrate_StaleRoomsTimeout(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.Exec(`
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES (?, ?, ?, ?)`,
		"room-stale",
		`{"test":"hydrate"}`,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert room: %v", err)
	}
	_, err = db.Exec(`
INSERT INTO envelopes (room_id, envelope_id, type, request_payload, status, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		"room-stale",
		"env-stale",
		"triage",
		`{"prompt":"hi"}`,
		"pending",
		now,
	)
	if err != nil {
		t.Fatalf("insert envelope: %v", err)
	}

	mgr := room.NewManager(db)
	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	if _, ok := mgr.Get("room-stale"); !ok {
		t.Fatal("expected hydrated room to be present")
	}
	assertEnvelopeStatus(t, db, "room-stale", "env-stale", "timeout", "SERVER_RESTART", true)
}

func TestRoom_PhaseStatePersistsAndHydrates(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "phaseful"})

	if err := rm.AdvancePhase("intake", "start"); err != nil {
		t.Fatalf("AdvancePhase intake: %v", err)
	}
	if err := rm.SetPhaseOutput("intake", "notes", map[string]any{"count": 2, "items": []any{"a", "b"}}); err != nil {
		t.Fatalf("SetPhaseOutput intake: %v", err)
	}
	if err := rm.AdvancePhase("draft", "forward"); err != nil {
		t.Fatalf("AdvancePhase draft: %v", err)
	}
	if err := rm.AdvancePhase("intake", "jump back"); err != nil {
		t.Fatalf("AdvancePhase intake again: %v", err)
	}

	state := rm.PhaseState()
	if state.CurrentPhase != "intake" {
		t.Fatalf("current_phase = %q, want intake", state.CurrentPhase)
	}
	if got, want := state.PhasesVisited, []string{"intake", "draft", "intake"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("phases_visited = %#v, want %#v", got, want)
	}
	if blob := state.PhaseOutputs["intake"]; blob.Version != 1 {
		t.Fatalf("phase output version = %d, want 1", blob.Version)
	}

	hydrated := room.NewManager(db)
	if err := hydrated.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloaded, ok := hydrated.Get(rm.ID)
	if !ok {
		t.Fatalf("room %q missing after hydrate", rm.ID)
	}
	reloadedState := reloaded.PhaseState()
	if reloadedState.CurrentPhase != "intake" {
		t.Fatalf("reloaded current_phase = %q, want intake", reloadedState.CurrentPhase)
	}
	if got, want := reloadedState.PhasesVisited, []string{"intake", "draft", "intake"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("reloaded phases_visited = %#v, want %#v", got, want)
	}
	blob := reloadedState.PhaseOutputs["intake"]
	if blob.Version != 1 {
		t.Fatalf("reloaded phase output version = %d, want 1", blob.Version)
	}
	notes, ok := blob.Data["notes"].(map[string]any)
	if !ok {
		t.Fatalf("reloaded phase output notes has type %T, want map[string]any", blob.Data["notes"])
	}
	if notes["count"] != float64(2) {
		t.Fatalf("reloaded notes count = %#v, want 2", notes["count"])
	}
}

func TestRoom_PhaseStateRejectsInvalidPhaseID(t *testing.T) {
	rm := room.NewManager(nil).Create(nil)

	if err := rm.AdvancePhase("   ", "bad"); !errors.Is(err, room.ErrInvalidPhaseID) {
		t.Fatalf("AdvancePhase invalid err = %v, want ErrInvalidPhaseID", err)
	}
	if err := rm.SetPhaseOutput("", "key", "value"); !errors.Is(err, room.ErrInvalidPhaseID) {
		t.Fatalf("SetPhaseOutput invalid phase err = %v, want ErrInvalidPhaseID", err)
	}
	if err := rm.SetPhaseOutput("draft", "   ", "value"); !errors.Is(err, room.ErrInvalidPhaseKey) {
		t.Fatalf("SetPhaseOutput invalid key err = %v, want ErrInvalidPhaseKey", err)
	}
}

func TestRoom_AcceptedDraftBlocksPersistAndHydrate(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "draftful"})

	if err := rm.AppendAcceptedDraftBlock(room.DraftBlock{
		BlockID:    "intro",
		EnvelopeID: "draft-1",
		Label:      "Intro",
		Mode:       "section",
		Content:    "First accepted draft block.",
		Decision:   "accept",
	}); err != nil {
		t.Fatalf("AppendAcceptedDraftBlock intro: %v", err)
	}
	if err := rm.AppendAcceptedDraftBlock(room.DraftBlock{
		BlockID:    "body",
		EnvelopeID: "draft-2",
		Label:      "Body",
		Mode:       "paragraph",
		Content:    "Second accepted draft block.",
		Decision:   "inline_edit",
		Feedback:   "Tightened the middle.",
	}); err != nil {
		t.Fatalf("AppendAcceptedDraftBlock body: %v", err)
	}
	if err := rm.AppendAcceptedDraftBlock(room.DraftBlock{
		BlockID:    "intro",
		EnvelopeID: "draft-3",
		Label:      "Intro",
		Mode:       "section",
		Content:    "First accepted draft block, revised.",
		Decision:   "inline_edit",
	}); err != nil {
		t.Fatalf("AppendAcceptedDraftBlock intro revision: %v", err)
	}

	state := rm.PhaseState()
	accepted := room.ProjectAcceptedDraftBlocks(state)
	if len(accepted) != 3 {
		t.Fatalf("accepted blocks len = %d, want 3", len(accepted))
	}
	current := room.ProjectCurrentDraft(state)
	if current == nil || current.BlockCount != 2 {
		t.Fatalf("current draft = %#v, want 2 blocks", current)
	}
	if got := current.Blocks[0].Content; got != "First accepted draft block, revised." {
		t.Fatalf("intro content = %q, want revised content", got)
	}

	hydrated := room.NewManager(db)
	if err := hydrated.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloaded, ok := hydrated.Get(rm.ID)
	if !ok {
		t.Fatalf("room %q missing after hydrate", rm.ID)
	}
	reloadedState := reloaded.PhaseState()
	reloadedAccepted := room.ProjectAcceptedDraftBlocks(reloadedState)
	if len(reloadedAccepted) != 3 {
		t.Fatalf("reloaded accepted blocks len = %d, want 3", len(reloadedAccepted))
	}
	reloadedCurrent := room.ProjectCurrentDraft(reloadedState)
	if reloadedCurrent == nil || reloadedCurrent.BlockCount != 2 {
		t.Fatalf("reloaded current draft = %#v, want 2 blocks", reloadedCurrent)
	}
	if got := reloadedCurrent.Markdown; !strings.Contains(got, "First accepted draft block, revised.") || !strings.Contains(got, "Second accepted draft block.") {
		t.Fatalf("reloaded markdown = %q, want both accepted blocks", got)
	}
}

func TestRoom_AcceptedDraftBlocksRejectInvalidInput(t *testing.T) {
	rm := room.NewManager(nil).Create(nil)

	if err := rm.AppendAcceptedDraftBlock(room.DraftBlock{BlockID: "   ", Content: "Valid text"}); !errors.Is(err, room.ErrInvalidDraftBlockID) {
		t.Fatalf("AppendAcceptedDraftBlock invalid id err = %v, want ErrInvalidDraftBlockID", err)
	}
	if err := rm.AppendAcceptedDraftBlock(room.DraftBlock{BlockID: "intro", Content: "   "}); !errors.Is(err, room.ErrInvalidDraftBlockContent) {
		t.Fatalf("AppendAcceptedDraftBlock invalid content err = %v, want ErrInvalidDraftBlockContent", err)
	}
}

func TestRoom_ProseRevisionOutcomesPersistAndHydrate(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "revisionful"})

	if err := rm.AppendProseRevisionOutcome(room.ProseRevisionOutcome{
		RevisionID: "opening-pass",
		EnvelopeID: "rev-1",
		Lens:       "review",
		BlockID:    "intro",
		SourceText: "Original opening paragraph.",
		Suggestions: []room.ProseRevisionSuggestion{
			{ID: "s1", SuggestedText: "Lead with the claim."},
			{ID: "s2", SuggestedText: "Cut the repeated example."},
		},
		Outcomes: []room.ProseRevisionSuggestionOutcome{
			{SuggestionID: "s1", Decision: "accept"},
			{SuggestionID: "s2", Decision: "comment", Comment: "Keep one example, just shorten it."},
		},
		GeneralComment: "Prefer structural fixes over more examples.",
	}); err != nil {
		t.Fatalf("AppendProseRevisionOutcome: %v", err)
	}

	state := rm.PhaseState()
	outcomes := room.ProjectProseRevisionOutcomes(state)
	if len(outcomes) != 1 {
		t.Fatalf("outcomes len = %d, want 1", len(outcomes))
	}
	if got := outcomes[0].Outcomes[0].Decision; got != "accept" {
		t.Fatalf("first decision = %q, want accept", got)
	}

	hydrated := room.NewManager(db)
	if err := hydrated.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloaded, ok := hydrated.Get(rm.ID)
	if !ok {
		t.Fatalf("room %q missing after hydrate", rm.ID)
	}
	reloadedOutcomes := room.ProjectProseRevisionOutcomes(reloaded.PhaseState())
	if len(reloadedOutcomes) != 1 {
		t.Fatalf("reloaded outcomes len = %d, want 1", len(reloadedOutcomes))
	}
	if got := reloadedOutcomes[0].Outcomes[1].Comment; got != "Keep one example, just shorten it." {
		t.Fatalf("reloaded comment = %q, want preserved comment", got)
	}
}

func TestRoom_ProseRevisionOutcomesRejectInvalidInput(t *testing.T) {
	rm := room.NewManager(nil).Create(nil)

	err := rm.AppendProseRevisionOutcome(room.ProseRevisionOutcome{
		RevisionID: "   ",
		Lens:       "review",
		SourceText: "Valid source.",
		Suggestions: []room.ProseRevisionSuggestion{
			{ID: "s1", SuggestedText: "Suggested text."},
		},
		Outcomes: []room.ProseRevisionSuggestionOutcome{
			{SuggestionID: "s1", Decision: "accept"},
		},
	})
	if !errors.Is(err, room.ErrInvalidProseRevisionID) {
		t.Fatalf("AppendProseRevisionOutcome invalid id err = %v, want ErrInvalidProseRevisionID", err)
	}

	err = rm.AppendProseRevisionOutcome(room.ProseRevisionOutcome{
		RevisionID: "rev-1",
		Lens:       "copy",
		SourceText: "Valid source.",
		Suggestions: []room.ProseRevisionSuggestion{
			{ID: "s1", SuggestedText: "Suggested text."},
		},
		Outcomes: []room.ProseRevisionSuggestionOutcome{
			{SuggestionID: "s1", Decision: "comment"},
		},
	})
	if !errors.Is(err, room.ErrInvalidProseRevisionOutcome) {
		t.Fatalf("AppendProseRevisionOutcome invalid outcome err = %v, want ErrInvalidProseRevisionOutcome", err)
	}
}

func TestRoom_TwoRoomsParallel(t *testing.T) {
	rmA, clientA, _, cleanupA := newTestServer(t)
	defer cleanupA()
	rmB, clientB, _, cleanupB := newTestServer(t)
	defer cleanupB()

	envA := &envelopes.Envelope{V: 1, ID: "e-A", Type: "triage"}
	envB := &envelopes.Envelope{V: 1, ID: "e-B", Type: "triage"}

	doneA := make(chan pushResult, 1)
	doneB := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rmA.Push(ctx, envA)
		doneA <- pushResult{resp: resp, err: err}
	}()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := rmB.Push(ctx, envB)
		doneB <- pushResult{resp: resp, err: err}
	}()

	frameA := readClientFrame(t, clientA, 2*time.Second)
	if frameA["envelopeId"] != "e-A" {
		t.Errorf("client A got envelope %v, want e-A", frameA["envelopeId"])
	}
	frameB := readClientFrame(t, clientB, 2*time.Second)
	if frameB["envelopeId"] != "e-B" {
		t.Errorf("client B got envelope %v, want e-B", frameB["envelopeId"])
	}

	writeClientFrame(t, clientA, map[string]any{
		"type":       "response",
		"envelopeId": "e-A",
		"response":   responseShape("e-A", "data", "submitted"),
	})
	writeClientFrame(t, clientB, map[string]any{
		"type":       "response",
		"envelopeId": "e-B",
		"response":   responseShape("e-B", "data", "submitted"),
	})

	resA := <-doneA
	resB := <-doneB
	if resA.err != nil {
		t.Fatalf("A push: %v", resA.err)
	}
	if resB.err != nil {
		t.Fatalf("B push: %v", resB.err)
	}
	if resA.resp.EnvelopeID != "e-A" {
		t.Errorf("A response envelopeID = %q, want e-A (cross-talk?)", resA.resp.EnvelopeID)
	}
	if resB.resp.EnvelopeID != "e-B" {
		t.Errorf("B response envelopeID = %q, want e-B (cross-talk?)", resB.resp.EnvelopeID)
	}
}

func TestRoom_CloseIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()
	rm := newAnonRoom(t, db)
	rm.Close("first")
	rm.Close("second")
	rm.Close("third")
	if !rm.IsClosed() {
		t.Error("expected room to be closed")
	}
}

func TestRoom_ReplaceConn(t *testing.T) {
	rm, clientConn1, _, cleanup := newTestServer(t)
	defer cleanup()

	env := &envelopes.Envelope{V: 1, ID: "e-replace", Type: "triage"}
	pushDone := make(chan pushResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		resp, err := rm.Push(ctx, env)
		pushDone <- pushResult{resp: resp, err: err}
	}()

	frame := readClientFrame(t, clientConn1, 2*time.Second)
	if frame["envelopeId"] != "e-replace" {
		t.Fatalf("frame mismatch: %v", frame)
	}
	writeClientFrame(t, clientConn1, map[string]any{
		"type":       "response",
		"envelopeId": "e-replace",
		"response":   responseShape("e-replace", "data", "submitted"),
	})
	res := <-pushDone
	if res.err != nil {
		t.Fatalf("push: %v", res.err)
	}
}

type pushResult struct {
	resp *envelopes.Response
	err  error
}

func responseShape(id, kind, status string) map[string]any {
	return map[string]any{
		"v":          1,
		"envelopeId": id,
		"kind":       kind,
		"status":     status,
	}
}

func assertEnvelopeStatus(t *testing.T, db *sql.DB, roomID string, envelopeID string, wantStatus string, wantErrorCode string, expectResolved bool) {
	t.Helper()

	var status string
	var errorCode sql.NullString
	var resolvedAt sql.NullString
	err := db.QueryRow(`
SELECT status, error_code, resolved_at
FROM envelopes
WHERE room_id = ? AND envelope_id = ?`,
		roomID,
		envelopeID,
	).Scan(&status, &errorCode, &resolvedAt)
	if err != nil {
		t.Fatalf("query envelope row: %v", err)
	}
	if status != wantStatus {
		t.Fatalf("status = %q, want %q", status, wantStatus)
	}
	if got := nullableString(errorCode); got != wantErrorCode {
		t.Fatalf("error_code = %q, want %q", got, wantErrorCode)
	}
	if resolvedAt.Valid != expectResolved {
		t.Fatalf("resolved_at valid = %v, want %v", resolvedAt.Valid, expectResolved)
	}
}

func nullableString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
