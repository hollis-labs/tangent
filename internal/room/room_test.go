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
	if _, err := rm.AttachConn(context.Background(), serverConn, room.AttachOptions{ClientID: "test-tab"}); err != nil {
		t.Fatalf("attach test connection: %v", err)
	}

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

func TestRoom_SaveSpreadsheetReviewSnapshotPersistsAndReloads(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "spreadsheet"})
	if err := rm.SaveSpreadsheetReviewSnapshot(room.SpreadsheetReviewSnapshot{
		TableID: "table-1",
		Columns: []map[string]any{
			{"id": "name", "label": "Name"},
			{"id": "status", "label": "Status"},
		},
		Rows: []map[string]any{
			{"id": "row-1", "name": "Alpha", "status": "open"},
			{"id": "row-2", "name": "Beta", "status": "closed"},
		},
		QueryState: map[string]any{
			"sort": []map[string]any{{"column_id": "status", "direction": "asc"}},
			"filters": []map[string]any{
				{"column_id": "status", "op": "eq", "value": "open"},
			},
			"search":          "Alpha  ",
			"visible_columns": []string{"name", "status"},
			"page":            map[string]any{"index": 2, "size": 25},
			"ignored":         true,
		},
		Notes:     "  shortlist only  ",
		UpdatedAt: "2026-05-09T20:05:00Z",
		SavedViews: []room.SpreadsheetReviewSavedView{
			{
				Name: "Open items",
				QueryState: map[string]any{
					"filters": []map[string]any{
						{"column_id": "status", "op": "eq", "value": "open"},
					},
					"ignored": "drop-me",
				},
			},
		},
	}); err != nil {
		t.Fatalf("SaveSpreadsheetReviewSnapshot: %v", err)
	}

	state := rm.PhaseState()
	review := room.ProjectSpreadsheetReviewState(state)
	if review == nil {
		t.Fatal("ProjectSpreadsheetReviewState returned nil")
	}
	if got := review.TableID; got != "table-1" {
		t.Fatalf("table_id = %q, want table-1", got)
	}
	if got := review.Notes; got != "shortlist only" {
		t.Fatalf("notes = %q, want shortlist only", got)
	}
	if got := len(review.QueryState); got != 5 {
		t.Fatalf("query_state key count = %d, want 5", got)
	}
	if _, exists := review.QueryState["ignored"]; exists {
		t.Fatalf("query_state unexpectedly retained ignored key: %+v", review.QueryState)
	}
	if got := len(review.SavedViews); got != 1 {
		t.Fatalf("saved_views len = %d, want 1", got)
	}

	reloaded, found, err := mgr.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState reload: %v", err)
	}
	if !found {
		t.Fatalf("GetPhaseState found = false, want true")
	}
	reloadedReview := room.ProjectSpreadsheetReviewState(reloaded)
	if reloadedReview == nil {
		t.Fatal("reloaded ProjectSpreadsheetReviewState returned nil")
	}
	if got := len(reloadedReview.Rows); got != 2 {
		t.Fatalf("reloaded rows len = %d, want 2", got)
	}
	if got := reloadedReview.SavedViews[0].Name; got != "Open items" {
		t.Fatalf("reloaded saved view name = %q, want Open items", got)
	}
	if _, exists := reloadedReview.SavedViews[0].QueryState["ignored"]; exists {
		t.Fatalf("saved view query_state unexpectedly retained ignored key: %+v", reloadedReview.SavedViews[0].QueryState)
	}
}

func TestRoom_SaveFilePickerSnapshotPersistsAndReloads(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "file-picker"})
	if err := rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID: "picker-1",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Label: "Workspace", Path: "/tmp/workspace"},
			{RootID: "docs", Label: "Docs", Path: "/tmp/docs"},
		},
		SelectedRefs: []room.FilePickerArtifactRef{
			{
				ArtifactID:   "artifact-1",
				Name:         "spec.md",
				URI:          "artifact://artifact-1",
				MIMEType:     "text/markdown",
				RootID:       "workspace",
				RelativePath: "docs/spec.md",
			},
		},
		QueryState: map[string]any{
			"search":      "spec",
			"current_dir": "docs",
			"sort":        "name:asc",
		},
		SelectionRevisions: []room.FilePickerSelectionRevision{
			{
				SubmittedAt:   "2026-05-09T20:30:00Z",
				SelectedCount: 1,
			},
		},
		SubmissionSummary: &room.FilePickerSubmissionSummary{
			SelectionRevisionID: "picker-1-rev-001",
			SelectedNames:       []string{"spec.md"},
			SelectedCount:       1,
			SubmittedAt:         "2026-05-09T20:30:00Z",
		},
		Handoff: &room.FilePickerHandoff{
			SelectionRevisionID: "picker-1-rev-001",
			ArtifactRefs: []room.FilePickerArtifactRef{
				{
					ArtifactID:   "artifact-1",
					Name:         "spec.md",
					URI:          "artifact://artifact-1",
					MIMEType:     "text/markdown",
					RootID:       "workspace",
					RelativePath: "docs/spec.md",
				},
			},
			Summary: &room.FilePickerSubmissionSummary{
				SelectionRevisionID: "picker-1-rev-001",
				SelectedNames:       []string{"spec.md"},
				SelectedCount:       1,
				SubmittedAt:         "2026-05-09T20:30:00Z",
			},
		},
		UpdatedAt: "2026-05-09T20:30:00Z",
	}); err != nil {
		t.Fatalf("SaveFilePickerSnapshot: %v", err)
	}

	view := room.ProjectFilePickerState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectFilePickerState returned nil")
	}
	if got := view.PickerID; got != "picker-1" {
		t.Fatalf("picker_id = %q, want picker-1", got)
	}
	if got := len(view.BrowseRoots); got != 2 {
		t.Fatalf("browse_roots len = %d, want 2", got)
	}
	if got := view.SelectedRefs[0].ArtifactID; got != "artifact-1" {
		t.Fatalf("selected_refs[0].artifact_id = %q, want artifact-1", got)
	}
	if got := view.SelectedRefs[0].RelativePath; got != "docs/spec.md" {
		t.Fatalf("selected_refs[0].relative_path = %q, want docs/spec.md", got)
	}
	if got := view.QueryState["search"]; got != "spec" {
		t.Fatalf("query_state.search = %v, want spec", got)
	}
	if got := view.SelectionRevisions[0].SelectedCount; got != 1 {
		t.Fatalf("selection_revisions[0].selected_count = %d, want 1", got)
	}
	if view.SubmissionSummary == nil || view.SubmissionSummary.SelectionRevisionID != "picker-1-rev-001" {
		t.Fatalf("submission_summary = %#v, want revision picker-1-rev-001", view.SubmissionSummary)
	}
	if view.Handoff == nil || len(view.Handoff.ArtifactRefs) != 1 {
		t.Fatalf("handoff = %#v, want one artifact ref", view.Handoff)
	}

	reloaded, found, err := mgr.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState reload: %v", err)
	}
	if !found {
		t.Fatal("GetPhaseState found = false, want true")
	}
	reloadedView := room.ProjectFilePickerState(reloaded)
	if reloadedView == nil {
		t.Fatal("reloaded ProjectFilePickerState returned nil")
	}
	if got := reloadedView.SelectedRefs[0].URI; got != "artifact://artifact-1" {
		t.Fatalf("reloaded selected_refs[0].uri = %q, want artifact://artifact-1", got)
	}
	if got := reloadedView.BrowseRoots[0].Kind; got != "directory" {
		t.Fatalf("reloaded browse_roots[0].kind = %q, want directory", got)
	}
	if reloadedView.Handoff == nil || reloadedView.Handoff.SelectionRevisionID != "picker-1-rev-001" {
		t.Fatalf("reloaded handoff = %#v, want revision picker-1-rev-001", reloadedView.Handoff)
	}
}

func TestRoom_SaveFilePickerSnapshotEmptyPicker(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID:           "picker-empty",
		QueryState:         map[string]any{},
		BrowseRoots:        []room.FilePickerBrowseRoot{},
		SelectedRefs:       []room.FilePickerArtifactRef{},
		SelectionRevisions: []room.FilePickerSelectionRevision{},
	}); err != nil {
		t.Fatalf("SaveFilePickerSnapshot empty: %v", err)
	}

	view := room.ProjectFilePickerState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectFilePickerState returned nil")
	}
	if got := len(view.BrowseRoots); got != 0 {
		t.Fatalf("browse_roots len = %d, want 0", got)
	}
	if got := len(view.SelectedRefs); got != 0 {
		t.Fatalf("selected_refs len = %d, want 0", got)
	}
	if got := len(view.SelectionRevisions); got != 0 {
		t.Fatalf("selection_revisions len = %d, want 0", got)
	}
}

func TestRoom_SaveFilePickerSnapshotRejectsInvalidSelectionRef(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID: "picker-invalid",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Path: "/tmp/workspace"},
		},
		SelectedRefs: []room.FilePickerArtifactRef{
			{
				Name:         "unsafe",
				URI:          "blob:local-file",
				RootID:       "workspace",
				RelativePath: "unsafe.txt",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidFilePickerSelectionRef) {
		t.Fatalf("SaveFilePickerSnapshot invalid ref err = %v, want ErrInvalidFilePickerSelectionRef", err)
	}

	err = rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID: "picker-out-of-root",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Path: "/tmp/workspace"},
		},
		SelectedRefs: []room.FilePickerArtifactRef{
			{
				ArtifactID:   "artifact-2",
				RootID:       "workspace",
				RelativePath: "../escape.txt",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidFilePickerSelectionRef) {
		t.Fatalf("SaveFilePickerSnapshot out-of-root err = %v, want ErrInvalidFilePickerSelectionRef", err)
	}
}

func TestRoom_SaveFilePickerSnapshotRejectsDuplicateRevisionIDs(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID: "picker-dup-revision",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Path: "/tmp/workspace"},
		},
		SelectionRevisions: []room.FilePickerSelectionRevision{
			{SelectionRevisionID: "picker-dup-revision-001", SelectedCount: 1},
			{SelectionRevisionID: "picker-dup-revision-002", SelectedCount: 1},
			{SelectionRevisionID: "picker-dup-revision-001", SelectedCount: 2},
		},
	})
	if !errors.Is(err, room.ErrInvalidFilePickerSelectionRevision) {
		t.Fatalf("SaveFilePickerSnapshot duplicate revision err = %v, want ErrInvalidFilePickerSelectionRevision", err)
	}
}

func TestRoom_SaveFilePickerSnapshotRejectsInvalidHandoffRef(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveFilePickerSnapshot(room.FilePickerSnapshot{
		PickerID: "picker-invalid-handoff",
		BrowseRoots: []room.FilePickerBrowseRoot{
			{RootID: "workspace", Path: "/tmp/workspace"},
		},
		Handoff: &room.FilePickerHandoff{
			SelectionRevisionID: "picker-invalid-handoff-001",
			ArtifactRefs: []room.FilePickerArtifactRef{
				{
					ArtifactID:   "artifact-bad",
					URI:          "file:///tmp/unsafe.txt",
					RootID:       "workspace",
					RelativePath: "unsafe.txt",
				},
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidFilePickerSelectionRef) {
		t.Fatalf("SaveFilePickerSnapshot invalid handoff err = %v, want ErrInvalidFilePickerSelectionRef", err)
	}
}

func TestRoom_SaveProgressPanelSnapshotEmptyPanel(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveProgressPanelSnapshot(room.ProgressPanelSnapshot{
		PanelID: "panel-empty",
		Items:   []room.ProgressPanelItem{},
		Updates: []room.ProgressPanelUpdate{},
	}); err != nil {
		t.Fatalf("SaveProgressPanelSnapshot empty: %v", err)
	}

	view := room.ProjectProgressPanelState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectProgressPanelState returned nil")
	}
	if got := len(view.Items); got != 0 {
		t.Fatalf("items len = %d, want 0", got)
	}
	if got := len(view.Updates); got != 0 {
		t.Fatalf("updates len = %d, want 0", got)
	}
	if got := len(view.Checkpoints); got != 0 {
		t.Fatalf("checkpoints len = %d, want 0", got)
	}
}

func TestRoom_SaveProgressPanelSnapshotRoundTrip(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "progress-panel"})

	if err := rm.SaveProgressPanelSnapshot(room.ProgressPanelSnapshot{
		PanelID: "panel-1",
		Items: []room.ProgressPanelItem{
			{
				ItemID:    "item-1",
				Label:     "Ingest repository",
				Status:    "running",
				Detail:    "Enumerating source files",
				CreatedAt: "2026-05-09T21:00:00Z",
				UpdatedAt: "2026-05-09T21:04:00Z",
				Metadata: map[string]any{
					"owner": "codex",
					"step":  1,
				},
			},
			{
				ItemID:    "item-2",
				Label:     "Write summary",
				Status:    "queued",
				CreatedAt: "2026-05-09T21:00:00Z",
				Metadata:  map[string]any{},
			},
		},
		Updates: []room.ProgressPanelUpdate{
			{
				UpdateID:  "upd-001",
				Kind:      "status",
				ItemID:    "item-1",
				Status:    "running",
				Summary:   "Work started",
				CreatedAt: "2026-05-09T21:01:00Z",
				Metadata:  map[string]any{"source": "agent"},
			},
			{
				UpdateID:        "upd-002",
				Kind:            "checkpoint",
				ItemID:          "item-1",
				Summary:         "Workspace scan complete",
				CreatedAt:       "2026-05-09T21:03:00Z",
				CheckpointID:    "cp-001",
				CheckpointLabel: "Scan complete",
				Metadata:        map[string]any{"files": 24},
			},
			{
				UpdateID:  "upd-003",
				Kind:      "summary",
				Summary:   "Repository scan is underway",
				CreatedAt: "2026-05-09T21:04:00Z",
				Metadata:  map[string]any{"scope": "boot"},
			},
		},
		Summary: &room.ProgressPanelSummary{
			CurrentStatus:       "running",
			Headline:            "1 active item",
			Detail:              "Repository scan is underway",
			LastUpdateID:        "upd-003",
			LastCheckpointID:    "cp-001",
			LastCheckpointLabel: "Scan complete",
		},
		UpdatedAt: "2026-05-09T21:04:00Z",
	}); err != nil {
		t.Fatalf("SaveProgressPanelSnapshot: %v", err)
	}

	view := room.ProjectProgressPanelState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectProgressPanelState returned nil")
	}
	if got := view.PanelID; got != "panel-1" {
		t.Fatalf("panel_id = %q, want panel-1", got)
	}
	if got := len(view.Items); got != 2 {
		t.Fatalf("items len = %d, want 2", got)
	}
	if got := view.Items[0].Metadata["owner"]; got != "codex" {
		t.Fatalf("items[0].metadata.owner = %v, want codex", got)
	}
	if got := len(view.Updates); got != 3 {
		t.Fatalf("updates len = %d, want 3", got)
	}
	if got := view.Updates[1].CheckpointID; got != "cp-001" {
		t.Fatalf("updates[1].checkpoint_id = %q, want cp-001", got)
	}
	if got := len(view.Checkpoints); got != 1 {
		t.Fatalf("checkpoints len = %d, want 1", got)
	}
	if got := view.Checkpoints[0].Label; got != "Scan complete" {
		t.Fatalf("checkpoints[0].label = %q, want Scan complete", got)
	}
	if view.Summary == nil || view.Summary.LastUpdateID != "upd-003" {
		t.Fatalf("summary = %#v, want last_update_id upd-003", view.Summary)
	}
	if got := view.Summary.LastCheckpointLabel; got != "Scan complete" {
		t.Fatalf("summary.last_checkpoint_label = %q, want Scan complete", got)
	}

	reloaded, found, err := mgr.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState reload: %v", err)
	}
	if !found {
		t.Fatal("GetPhaseState found = false, want true")
	}
	reloadedView := room.ProjectProgressPanelState(reloaded)
	if reloadedView == nil {
		t.Fatal("reloaded ProjectProgressPanelState returned nil")
	}
	if got := reloadedView.Items[0].Status; got != "running" {
		t.Fatalf("reloaded items[0].status = %q, want running", got)
	}
	if got := reloadedView.Checkpoints[0].Summary; got != "Workspace scan complete" {
		t.Fatalf("reloaded checkpoints[0].summary = %q, want Workspace scan complete", got)
	}
	if got := reloadedView.Summary.LastCheckpointLabel; got != "Scan complete" {
		t.Fatalf("reloaded summary.last_checkpoint_label = %q, want Scan complete", got)
	}
}

func TestRoom_SaveProgressPanelSnapshotRejectsInvalidUpdate(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveProgressPanelSnapshot(room.ProgressPanelSnapshot{
		PanelID: "panel-invalid",
		Items: []room.ProgressPanelItem{
			{ItemID: "item-1", Label: "Item 1", Status: "running", Metadata: map[string]any{}},
		},
		Updates: []room.ProgressPanelUpdate{
			{
				UpdateID: "upd-001",
				Kind:     "status",
				ItemID:   "item-missing",
				Status:   "running",
				Metadata: map[string]any{},
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidProgressPanelUpdate) {
		t.Fatalf("SaveProgressPanelSnapshot unknown item update err = %v, want ErrInvalidProgressPanelUpdate", err)
	}

	err = rm.SaveProgressPanelSnapshot(room.ProgressPanelSnapshot{
		PanelID: "panel-invalid-checkpoint",
		Items: []room.ProgressPanelItem{
			{ItemID: "item-1", Label: "Item 1", Status: "running", Metadata: map[string]any{}},
		},
		Updates: []room.ProgressPanelUpdate{
			{
				UpdateID:     "upd-002",
				Kind:         "checkpoint",
				ItemID:       "item-1",
				CheckpointID: "cp-001",
				Metadata:     map[string]any{},
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidProgressPanelUpdate) {
		t.Fatalf("SaveProgressPanelSnapshot invalid checkpoint err = %v, want ErrInvalidProgressPanelUpdate", err)
	}
}

func TestRoom_SaveDashboardSnapshotEmptyDashboard(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveDashboardSnapshot(room.DashboardSnapshot{
		DashboardID:  "dashboard-empty",
		Tiles:        []room.DashboardTile{},
		Layout:       []room.DashboardTilePlacement{},
		SavedLayouts: []room.DashboardSavedLayout{},
		QueryState: &room.DashboardQueryState{
			Filters: []room.DashboardFilterState{},
			Sort:    []room.DashboardSortState{},
		},
		Summary: &room.DashboardSummary{},
	}); err != nil {
		t.Fatalf("SaveDashboardSnapshot empty: %v", err)
	}

	view := room.ProjectDashboardState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectDashboardState returned nil")
	}
	if got := len(view.Tiles); got != 0 {
		t.Fatalf("tiles len = %d, want 0", got)
	}
	if got := len(view.Layout); got != 0 {
		t.Fatalf("layout len = %d, want 0", got)
	}
	if got := len(view.SavedLayouts); got != 0 {
		t.Fatalf("saved_layouts len = %d, want 0", got)
	}
}

func TestRoom_SaveDashboardSnapshotPersistsAndReloads(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "dashboard"})

	if err := rm.SaveDashboardSnapshot(room.DashboardSnapshot{
		DashboardID: "dashboard-1",
		Title:       "Operations Dashboard",
		Tiles: []room.DashboardTile{
			{
				TileID:   "tile-open-rooms",
				Kind:     "room_count",
				Title:    "Open rooms",
				Status:   "healthy",
				Value:    "6",
				Summary:  "Rooms waiting for operator review",
				Workflow: "tangent.progress-panel",
				Metadata: map[string]any{"color": "amber"},
			},
			{
				TileID:      "tile-active-progress",
				Kind:        "workflow_summary",
				Title:       "Active progress panels",
				Subtitle:    "Running workflows",
				Status:      "busy",
				Value:       "2",
				RoomID:      "room-123",
				Workflow:    "tangent.progress-panel",
				ArtifactRef: "artifact://summary-123",
				Metadata:    map[string]any{"owner": "codex"},
			},
		},
		Layout: []room.DashboardTilePlacement{
			{TileID: "tile-open-rooms", X: 0, Y: 0, W: 2, H: 1},
			{TileID: "tile-active-progress", X: 2, Y: 0, W: 2, H: 2},
		},
		SavedLayouts: []room.DashboardSavedLayout{
			{
				LayoutID:    "layout-default",
				Name:        "Default",
				Description: "Operational starting point",
				Tiles: []room.DashboardTilePlacement{
					{TileID: "tile-open-rooms", X: 0, Y: 0, W: 2, H: 1},
					{TileID: "tile-active-progress", X: 2, Y: 0, W: 2, H: 2},
				},
				IsDefault: true,
				UpdatedAt: "2026-05-09T22:00:00Z",
			},
		},
		ActiveLayoutID: "layout-default",
		QueryState: &room.DashboardQueryState{
			Search:  "progress",
			Scope:   "active",
			GroupBy: "workflow",
			Filters: []room.DashboardFilterState{
				{FilterID: "status", Label: "Status", Operator: "in", Values: []string{"running", "blocked"}},
			},
			Range: &room.DashboardRangeState{
				Kind:     "relative",
				Preset:   "24h",
				Timezone: "America/Chicago",
			},
			Sort: []room.DashboardSortState{
				{Field: "updated_at", Direction: "desc"},
			},
		},
		Summary: &room.DashboardSummary{
			Headline:           "2 workflows need attention",
			Detail:             "One room is blocked and one is awaiting review",
			Status:             "attention",
			ActiveRoomCount:    6,
			LastRefreshAt:      "2026-05-09T22:01:00Z",
			AcceptedSnapshotID: "dashboard-1-snapshot-001",
			AcceptedSnapshotAt: "2026-05-09T22:01:00Z",
		},
		SnapshotHistory: []room.DashboardSnapshotMeta{
			{
				SnapshotID:     "dashboard-1-snapshot-001",
				Action:         "refresh",
				Note:           "First accepted refresh",
				CreatedAt:      "2026-05-09T22:01:00Z",
				TileCount:      2,
				ActiveLayoutID: "layout-default",
			},
		},
		ExportState: &room.DashboardExportState{
			ExportID:       "dashboard-1-export-dashboard-1-snapshot-001",
			SnapshotID:     "dashboard-1-snapshot-001",
			GeneratedAt:    "2026-05-09T22:01:00Z",
			ActiveLayoutID: "layout-default",
			TileCount:      2,
			RoomRefs:       []string{"room-123"},
			ArtifactRefs:   []string{"artifact://summary-123"},
		},
		UpdatedAt: "2026-05-09T22:01:00Z",
	}); err != nil {
		t.Fatalf("SaveDashboardSnapshot: %v", err)
	}

	view := room.ProjectDashboardState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectDashboardState returned nil")
	}
	if got := view.DashboardID; got != "dashboard-1" {
		t.Fatalf("dashboard_id = %q, want dashboard-1", got)
	}
	if got := view.Title; got != "Operations Dashboard" {
		t.Fatalf("title = %q, want Operations Dashboard", got)
	}
	if got := len(view.Tiles); got != 2 {
		t.Fatalf("tiles len = %d, want 2", got)
	}
	if got := view.Tiles[1].ArtifactRef; got != "artifact://summary-123" {
		t.Fatalf("tiles[1].artifact_ref = %q, want artifact://summary-123", got)
	}
	if got := len(view.Layout); got != 2 {
		t.Fatalf("layout len = %d, want 2", got)
	}
	if got := len(view.SavedLayouts); got != 1 {
		t.Fatalf("saved_layouts len = %d, want 1", got)
	}
	if got := view.ActiveLayoutID; got != "layout-default" {
		t.Fatalf("active_layout_id = %q, want layout-default", got)
	}
	if view.QueryState == nil || view.QueryState.Range == nil {
		t.Fatalf("query_state = %#v, want non-nil range", view.QueryState)
	}
	if got := view.QueryState.Sort[0].Direction; got != "desc" {
		t.Fatalf("sort[0].direction = %q, want desc", got)
	}
	if view.Summary == nil || view.Summary.TileCount != 2 {
		t.Fatalf("summary = %#v, want tile_count 2", view.Summary)
	}
	if got := len(view.SnapshotHistory); got != 1 {
		t.Fatalf("snapshot_history len = %d, want 1", got)
	}
	if view.ExportState == nil || view.ExportState.SnapshotID != "dashboard-1-snapshot-001" {
		t.Fatalf("export_state = %#v, want snapshot_id dashboard-1-snapshot-001", view.ExportState)
	}

	reloaded, found, err := mgr.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState reload: %v", err)
	}
	if !found {
		t.Fatal("GetPhaseState found = false, want true")
	}
	reloadedView := room.ProjectDashboardState(reloaded)
	if reloadedView == nil {
		t.Fatal("reloaded ProjectDashboardState returned nil")
	}
	if got := reloadedView.Tiles[0].Metadata["color"]; got != "amber" {
		t.Fatalf("reloaded tiles[0].metadata.color = %v, want amber", got)
	}
	if got := reloadedView.SavedLayouts[0].Tiles[1].TileID; got != "tile-active-progress" {
		t.Fatalf("reloaded saved_layouts[0].tiles[1].tile_id = %q, want tile-active-progress", got)
	}
	if got := reloadedView.Summary.AcceptedSnapshotID; got != "dashboard-1-snapshot-001" {
		t.Fatalf("reloaded summary.accepted_snapshot_id = %q, want dashboard-1-snapshot-001", got)
	}
	if got := reloadedView.SnapshotHistory[0].Action; got != "refresh" {
		t.Fatalf("reloaded snapshot_history[0].action = %q, want refresh", got)
	}
	if got := reloadedView.ExportState.ArtifactRefs[0]; got != "artifact://summary-123" {
		t.Fatalf("reloaded export_state.artifact_refs[0] = %q, want artifact://summary-123", got)
	}
}

func TestRoom_SaveDashboardSnapshotRejectsInvalidConfiguration(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveDashboardSnapshot(room.DashboardSnapshot{
		DashboardID: "dashboard-invalid-layout",
		Tiles: []room.DashboardTile{
			{TileID: "tile-1", Kind: "count", Title: "Tile 1", Metadata: map[string]any{}},
		},
		Layout: []room.DashboardTilePlacement{
			{TileID: "missing-tile", X: 0, Y: 0, W: 1, H: 1},
		},
	})
	if !errors.Is(err, room.ErrInvalidDashboardLayout) {
		t.Fatalf("SaveDashboardSnapshot invalid layout err = %v, want ErrInvalidDashboardLayout", err)
	}

	err = rm.SaveDashboardSnapshot(room.DashboardSnapshot{
		DashboardID: "dashboard-invalid-query",
		Tiles: []room.DashboardTile{
			{TileID: "tile-1", Kind: "count", Title: "Tile 1", Metadata: map[string]any{}},
		},
		QueryState: &room.DashboardQueryState{
			Sort: []room.DashboardSortState{
				{Field: "updated_at", Direction: "sideways"},
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidDashboardQueryState) {
		t.Fatalf("SaveDashboardSnapshot invalid query err = %v, want ErrInvalidDashboardQueryState", err)
	}

	err = rm.SaveDashboardSnapshot(room.DashboardSnapshot{
		DashboardID: "dashboard-invalid-summary",
		Tiles: []room.DashboardTile{
			{TileID: "tile-1", Kind: "count", Title: "Tile 1", Metadata: map[string]any{}},
		},
		Summary: &room.DashboardSummary{
			TileCount: -1,
		},
	})
	if !errors.Is(err, room.ErrInvalidDashboardSummary) {
		t.Fatalf("SaveDashboardSnapshot invalid summary err = %v, want ErrInvalidDashboardSummary", err)
	}
}

func TestRoom_SaveWizardSnapshotEmptyWizard(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveWizardSnapshot(room.WizardSnapshot{
		WizardID:         "wizard-empty",
		Steps:            []room.WizardStep{},
		Progress:         []room.WizardStepProgress{},
		BranchSelections: []room.WizardBranchSelection{},
		Summary: &room.WizardSummary{
			Status:         "not_started",
			TotalStepCount: 0,
		},
	}); err != nil {
		t.Fatalf("SaveWizardSnapshot empty: %v", err)
	}

	view := room.ProjectWizardState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectWizardState returned nil")
	}
	if got := len(view.Steps); got != 0 {
		t.Fatalf("steps len = %d, want 0", got)
	}
	if got := len(view.Progress); got != 0 {
		t.Fatalf("progress len = %d, want 0", got)
	}
	if got := len(view.BranchSelections); got != 0 {
		t.Fatalf("branch_selections len = %d, want 0", got)
	}
}

func TestRoom_SaveWizardSnapshotPersistsAndReloads(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "wizard"})

	if err := rm.SaveWizardSnapshot(room.WizardSnapshot{
		WizardID:      "wizard-1",
		Title:         "Shipping Checklist",
		Description:   "Collect the bounded inputs needed to prepare release notes.",
		CurrentStepID: "step-review",
		Steps: []room.WizardStep{
			{
				StepID:      "step-scope",
				Title:       "Confirm scope",
				Description: "Define the release scope.",
				Kind:        "form",
				Fields: map[string]any{
					"fields": []map[string]any{
						{"field_id": "scope", "label": "Scope", "kind": "textarea"},
					},
				},
				Branches: []room.WizardStepBranch{
					{BranchID: "requires-review", Label: "Needs review", TargetStepID: "step-review", Metadata: map[string]any{"priority": "high"}},
					{BranchID: "skip-review", Label: "Skip review", TargetStepID: "step-publish", Metadata: map[string]any{}},
				},
				Metadata: map[string]any{"order": float64(1)},
			},
			{
				StepID:      "step-review",
				Title:       "Review summary",
				Description: "Inspect the accepted result.",
				Kind:        "review",
				Metadata:    map[string]any{"order": float64(2)},
			},
			{
				StepID:   "step-publish",
				Title:    "Publish result",
				Optional: true,
				Kind:     "action",
				Metadata: map[string]any{"order": float64(3)},
			},
		},
		Progress: []room.WizardStepProgress{
			{
				StepID:     "step-scope",
				Status:     "completed",
				RevisionID: "rev-001",
				Response: map[string]any{
					"scope": "Wizard state substrate",
				},
				Summary:     "Scope confirmed",
				CompletedAt: "2026-05-10T04:10:00Z",
				UpdatedAt:   "2026-05-10T04:10:00Z",
			},
			{
				StepID:     "step-review",
				Status:     "in_progress",
				RevisionID: "rev-002",
				Response: map[string]any{
					"notes": "Awaiting final review",
				},
				Summary:   "Review in progress",
				UpdatedAt: "2026-05-10T04:12:00Z",
			},
		},
		BranchSelections: []room.WizardBranchSelection{
			{
				StepID:     "step-scope",
				OptionID:   "requires-review",
				SelectedAt: "2026-05-10T04:10:00Z",
			},
		},
		Summary: &room.WizardSummary{
			Status:              "in_progress",
			Headline:            "1 of 3 steps completed",
			Detail:              "Review remains before publish.",
			CompletedStepCount:  1,
			TotalStepCount:      3,
			LastCompletedStepID: "step-scope",
			CurrentStepID:       "step-review",
		},
		UpdatedAt: "2026-05-10T04:12:00Z",
	}); err != nil {
		t.Fatalf("SaveWizardSnapshot: %v", err)
	}

	view := room.ProjectWizardState(rm.PhaseState())
	if view == nil {
		t.Fatal("ProjectWizardState returned nil")
	}
	if got := view.WizardID; got != "wizard-1" {
		t.Fatalf("wizard_id = %q, want wizard-1", got)
	}
	if got := view.CurrentStepID; got != "step-review" {
		t.Fatalf("current_step_id = %q, want step-review", got)
	}
	if got := len(view.Steps); got != 3 {
		t.Fatalf("steps len = %d, want 3", got)
	}
	if got := view.Steps[0].Branches[0].TargetStepID; got != "step-review" {
		t.Fatalf("steps[0].branches[0].target_step_id = %q, want step-review", got)
	}
	if got := view.Progress[0].Response["scope"]; got != "Wizard state substrate" {
		t.Fatalf("progress[0].response.scope = %v, want Wizard state substrate", got)
	}
	if got := view.BranchSelections[0].TargetStepID; got != "step-review" {
		t.Fatalf("branch_selections[0].target_step_id = %q, want step-review", got)
	}
	if view.Summary == nil || view.Summary.CurrentStepID != "step-review" {
		t.Fatalf("summary = %#v, want current_step_id step-review", view.Summary)
	}

	reloaded, found, err := mgr.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState reload: %v", err)
	}
	if !found {
		t.Fatal("GetPhaseState found = false, want true")
	}
	reloadedView := room.ProjectWizardState(reloaded)
	if reloadedView == nil {
		t.Fatal("reloaded ProjectWizardState returned nil")
	}
	if got := reloadedView.Steps[0].Metadata["order"]; got != float64(1) {
		t.Fatalf("reloaded steps[0].metadata.order = %v, want 1", got)
	}
	if got := reloadedView.Progress[1].RevisionID; got != "rev-002" {
		t.Fatalf("reloaded progress[1].revision_id = %q, want rev-002", got)
	}
	if got := reloadedView.BranchSelections[0].OptionID; got != "requires-review" {
		t.Fatalf("reloaded branch_selections[0].option_id = %q, want requires-review", got)
	}
}

func TestRoom_SaveWizardSnapshotRejectsInvalidProgress(t *testing.T) {
	rm := newAnonRoom(t, nil)
	err := rm.SaveWizardSnapshot(room.WizardSnapshot{
		WizardID: "wizard-invalid-progress",
		Steps: []room.WizardStep{
			{StepID: "step-1", Title: "Step 1", Metadata: map[string]any{}},
		},
		Progress: []room.WizardStepProgress{
			{StepID: "missing-step", Status: "completed", Response: map[string]any{}},
		},
	})
	if !errors.Is(err, room.ErrInvalidWizardProgress) {
		t.Fatalf("SaveWizardSnapshot invalid progress err = %v, want ErrInvalidWizardProgress", err)
	}

	err = rm.SaveWizardSnapshot(room.WizardSnapshot{
		WizardID: "wizard-invalid-branch",
		Steps: []room.WizardStep{
			{
				StepID:   "step-1",
				Title:    "Step 1",
				Branches: []room.WizardStepBranch{{BranchID: "next", Label: "Next", TargetStepID: "step-2", Metadata: map[string]any{}}},
				Metadata: map[string]any{},
			},
			{StepID: "step-2", Title: "Step 2", Metadata: map[string]any{}},
		},
		BranchSelections: []room.WizardBranchSelection{
			{StepID: "step-1", OptionID: "missing"},
		},
	})
	if !errors.Is(err, room.ErrInvalidWizardBranchSelection) {
		t.Fatalf("SaveWizardSnapshot invalid branch err = %v, want ErrInvalidWizardBranchSelection", err)
	}
}

func TestRoom_SaveSpreadsheetReviewSnapshotEmptyTable(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveSpreadsheetReviewSnapshot(room.SpreadsheetReviewSnapshot{
		TableID:    "table-empty",
		QueryState: map[string]any{},
		SavedViews: []room.SpreadsheetReviewSavedView{},
	}); err != nil {
		t.Fatalf("SaveSpreadsheetReviewSnapshot empty: %v", err)
	}

	review := room.ProjectSpreadsheetReviewState(rm.PhaseState())
	if review == nil {
		t.Fatal("ProjectSpreadsheetReviewState returned nil")
	}
	if got := len(review.Columns); got != 0 {
		t.Fatalf("columns len = %d, want 0", got)
	}
	if got := len(review.Rows); got != 0 {
		t.Fatalf("rows len = %d, want 0", got)
	}
	if got := len(review.SavedViews); got != 0 {
		t.Fatalf("saved_views len = %d, want 0", got)
	}
}

func TestRoom_SaveSpreadsheetReviewSavedViewsReplacesViews(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveSpreadsheetReviewSnapshot(room.SpreadsheetReviewSnapshot{
		TableID: "table-views",
		Columns: []map[string]any{{"id": "status"}},
		Rows:    []map[string]any{{"id": "row-1", "status": "open"}},
		QueryState: map[string]any{
			"search": "open",
		},
	}); err != nil {
		t.Fatalf("seed SaveSpreadsheetReviewSnapshot: %v", err)
	}

	if err := rm.SaveSpreadsheetReviewSavedViews("table-views", []room.SpreadsheetReviewSavedView{
		{
			Name: "Only open",
			QueryState: map[string]any{
				"filters": []map[string]any{
					{"column_id": "status", "op": "eq", "value": "open"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("SaveSpreadsheetReviewSavedViews: %v", err)
	}

	review := room.ProjectSpreadsheetReviewState(rm.PhaseState())
	if review == nil {
		t.Fatal("ProjectSpreadsheetReviewState returned nil")
	}
	if got := len(review.SavedViews); got != 1 {
		t.Fatalf("saved_views len = %d, want 1", got)
	}
	if got := review.SavedViews[0].Name; got != "Only open" {
		t.Fatalf("saved_views[0].name = %q, want Only open", got)
	}
	if got := review.QueryState["search"]; got != "open" {
		t.Fatalf("query_state.search = %v, want open", got)
	}
}

func TestRoom_SaveSpreadsheetReviewSavedViewsRejectsInvalidView(t *testing.T) {
	rm := newAnonRoom(t, nil)
	if err := rm.SaveSpreadsheetReviewSnapshot(room.SpreadsheetReviewSnapshot{
		TableID: "table-invalid-view",
	}); err != nil {
		t.Fatalf("seed SaveSpreadsheetReviewSnapshot: %v", err)
	}

	err := rm.SaveSpreadsheetReviewSavedViews("table-invalid-view", []room.SpreadsheetReviewSavedView{
		{
			Name: "   ",
			QueryState: map[string]any{
				"ignored": "should-not-persist",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidSpreadsheetSavedView) {
		t.Fatalf("SaveSpreadsheetReviewSavedViews invalid err = %v, want ErrInvalidSpreadsheetSavedView", err)
	}
}

func TestRoom_SaveSpreadsheetReviewSnapshotRejectsUnsupportedBlobVersion(t *testing.T) {
	db := newTestDB(t)
	defer func() {
		_ = tangentdb.Close(db)
	}()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "spreadsheet-version"})

	if _, err := db.Exec(`
UPDATE rooms
SET phase_outputs = ?
WHERE id = ?`,
		`{"spreadsheet-review":{"version":99,"data":{"table_id":"table-old"}}}`,
		rm.ID,
	); err != nil {
		t.Fatalf("seed unsupported version: %v", err)
	}
	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloaded, ok := mgr.Get(rm.ID)
	if !ok {
		t.Fatalf("room %q missing after hydrate", rm.ID)
	}

	err := reloaded.SaveSpreadsheetReviewSnapshot(room.SpreadsheetReviewSnapshot{
		TableID: "table-new",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported phase output version 99") {
		t.Fatalf("SaveSpreadsheetReviewSnapshot unsupported version err = %v, want unsupported phase output version 99", err)
	}
}

func TestRoom_ExplicitCloseTerminatesAndAuditsPending(t *testing.T) {
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
	rm.Close("operator closed surface")

	select {
	case res := <-pushDone:
		if res.err == nil {
			t.Fatal("expected error from disconnect, got nil")
		}
		if !errors.Is(res.err, room.ErrRoomDisconnected) {
			t.Errorf("expected ErrRoomDisconnected, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Push hung after explicit surface close")
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

func TestManager_Hydrate_DoesNotTimeoutCanonicalPendingInteraction(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	now := time.Now().UTC()
	if _, err := db.Exec(`
INSERT INTO rooms (id, meta, created_at, updated_at)
VALUES ('room-canonical-recovery', '{"test":"canonical-recovery"}', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO envelopes (room_id, envelope_id, type, request_payload, status, created_at)
VALUES ('room-canonical-recovery', 'env-canonical-recovery', 'tangent.triage',
        '{"prompt":"recover durably"}', 'pending', ?)`, now); err != nil {
		t.Fatalf("insert pending envelope: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO surfaces (
  id, owner_scope, lifecycle_state, metadata, policy,
  next_interaction_sequence, revision, created_at, updated_at, legacy_room_id
) VALUES (
  'surface-canonical-recovery', 'operator:local', 'active', '{}', '{}',
  2, 1, ?, ?, 'room-canonical-recovery'
)`, now, now); err != nil {
		t.Fatalf("insert canonical surface: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO interactions (
  id, surface_id, caller_scope, caller_authority, caller_assurance,
  idempotency_key, surface_sequence, request_snapshot, external_refs, policy,
  lifecycle_state, revision, created_at, updated_at,
  legacy_room_id, legacy_envelope_id
) VALUES (
  'interaction-canonical-recovery', 'surface-canonical-recovery',
  'application:test', 'direct-mcp', 'asserted',
  'canonical-recovery', 1, '{"prompt":"recover durably"}', '{}', '{}',
  'staged', 3, ?, ?, 'room-canonical-recovery', 'env-canonical-recovery'
)`, now, now); err != nil {
		t.Fatalf("insert canonical interaction: %v", err)
	}

	mgr := room.NewManager(db)
	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	var status string
	var resolvedAt sql.NullTime
	if err := db.QueryRow(`
SELECT status, resolved_at FROM envelopes
WHERE room_id = 'room-canonical-recovery' AND envelope_id = 'env-canonical-recovery'`).Scan(
		&status,
		&resolvedAt,
	); err != nil {
		t.Fatalf("read compatibility envelope: %v", err)
	}
	if status != "pending" || resolvedAt.Valid {
		t.Fatalf("canonical pending compatibility row = status %q resolved=%v", status, resolvedAt.Valid)
	}
	var canonicalState string
	if err := db.QueryRow(`
SELECT lifecycle_state FROM interactions WHERE id = 'interaction-canonical-recovery'`).Scan(&canonicalState); err != nil {
		t.Fatalf("read canonical interaction: %v", err)
	}
	if canonicalState != "staged" {
		t.Fatalf("canonical interaction state = %q, want staged", canonicalState)
	}
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

func TestRoom_SetFinalOutputPersistsAndHydrates(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm, err := mgr.CreateWithError(map[string]string{"title": "Final output"})
	if err != nil {
		t.Fatalf("CreateWithError: %v", err)
	}

	err = rm.SetFinalOutput(room.FinalOutputView{
		Title:     "Final draft",
		Markdown:  "# Final draft\n\nTight final paragraph.",
		Filename:  "final-draft.md",
		Format:    "markdown",
		Summary:   "Polished final copy.",
		UpdatedAt: "2026-05-09T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("SetFinalOutput: %v", err)
	}

	state := rm.PhaseState()
	output := room.ProjectFinalOutput(state)
	if output == nil {
		t.Fatal("ProjectFinalOutput returned nil")
	}
	if output.Filename != "final-draft.md" {
		t.Fatalf("filename = %q, want final-draft.md", output.Filename)
	}
	if output.WordCount == 0 {
		t.Fatal("word_count should be populated")
	}

	reloaded := room.NewManager(db)
	err = reloaded.Hydrate(context.Background())
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloadedState, found, err := reloaded.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState: %v", err)
	}
	if !found {
		t.Fatalf("GetPhaseState found=false for room %q", rm.ID)
	}
	reloadedOutput := room.ProjectFinalOutput(reloadedState)
	if reloadedOutput == nil {
		t.Fatal("reloaded ProjectFinalOutput returned nil")
	}
	if reloadedOutput.Markdown != "# Final draft\n\nTight final paragraph." {
		t.Fatalf("markdown = %q", reloadedOutput.Markdown)
	}
	if reloadedOutput.UpdatedAt != "2026-05-09T12:00:00Z" {
		t.Fatalf("updated_at = %q", reloadedOutput.UpdatedAt)
	}
}

func TestRoom_SetFinalOutputRejectsInvalidData(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)

	if err := rm.SetFinalOutput(room.FinalOutputView{Markdown: "   "}); !errors.Is(err, room.ErrInvalidFinalOutputMarkdown) {
		t.Fatalf("SetFinalOutput invalid markdown err = %v, want ErrInvalidFinalOutputMarkdown", err)
	}
	if err := rm.SetFinalOutput(room.FinalOutputView{Markdown: "valid", Format: "html"}); !errors.Is(err, room.ErrInvalidFinalOutputFormat) {
		t.Fatalf("SetFinalOutput invalid format err = %v, want ErrInvalidFinalOutputFormat", err)
	}
}

func TestRoom_WhiteboardStatePersistsAndHydrates(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	mgr := room.NewManager(db)
	rm := mgr.Create(map[string]string{"title": "boardful"})

	if err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID: "board-1",
		SceneSnapshot: map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1", "name": "Page 1"},
				},
			},
		},
		Assets: []room.WhiteboardAssetRef{
			{
				AssetID:    "asset-1",
				ArtifactID: "artifact-1",
				Name:       "screenshot.png",
				MIMEType:   "image/png",
				Source:     "https://assets.example.test/screenshot.png",
				URI:        "artifact://artifact-1",
				Kind:       "reference_image",
				Width:      1200,
				Height:     800,
			},
		},
		ExportRefs: []room.WhiteboardExportRef{
			{
				Name:      "board-1-r1.png",
				MIMEType:  "image/png",
				Kind:      "png",
				CreatedAt: "2026-05-09T19:30:10Z",
				SizeBytes: 2048,
				Width:     1200,
				Height:    800,
			},
		},
		Notes:     "first board snapshot",
		UpdatedAt: "2026-05-09T19:30:00Z",
		Revision: &room.WhiteboardRevision{
			RevisionID: "rev-1",
			UpdatedAt:  "2026-05-09T19:30:00Z",
			Summary:    "initial snapshot",
			SceneSize:  1,
			AssetCount: 1,
		},
	}); err != nil {
		t.Fatalf("SaveWhiteboardSnapshot rev-1: %v", err)
	}
	if err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID: "board-1",
		SceneSnapshot: map[string]any{
			"document": map[string]any{
				"pages": []any{
					map[string]any{"id": "page:1", "name": "Page 1"},
					map[string]any{"id": "shape:1", "type": "geo"},
				},
			},
		},
		Assets: []room.WhiteboardAssetRef{
			{
				AssetID:    "asset-1",
				ArtifactID: "artifact-1",
				Name:       "screenshot.png",
				MIMEType:   "image/png",
				Source:     "https://assets.example.test/screenshot.png",
				URI:        "artifact://artifact-1",
				Kind:       "reference_image",
				Width:      1200,
				Height:     800,
			},
		},
		ExportRefs: []room.WhiteboardExportRef{
			{
				Name:      "board-1-r2.png",
				MIMEType:  "image/png",
				Kind:      "png",
				CreatedAt: "2026-05-09T19:35:10Z",
				SizeBytes: 3072,
				Width:     1280,
				Height:    900,
			},
		},
		Notes:     "second board snapshot",
		UpdatedAt: "2026-05-09T19:35:00Z",
		Revision: &room.WhiteboardRevision{
			RevisionID:              "rev-2",
			ContinuedFromRevisionID: "rev-1",
			UpdatedAt:               "2026-05-09T19:35:00Z",
			Summary:                 "added shape",
			SceneSize:               2,
			AssetCount:              1,
		},
	}); err != nil {
		t.Fatalf("SaveWhiteboardSnapshot rev-2: %v", err)
	}

	state := rm.PhaseState()
	board := room.ProjectWhiteboardState(state)
	if board == nil {
		t.Fatal("ProjectWhiteboardState returned nil")
	}
	if board.BoardID != "board-1" {
		t.Fatalf("board_id = %q, want board-1", board.BoardID)
	}
	if got := board.Notes; got != "second board snapshot" {
		t.Fatalf("notes = %q, want second board snapshot", got)
	}
	if got := len(board.RevisionHistory); got != 2 {
		t.Fatalf("revision_history len = %d, want 2", got)
	}
	if got := len(board.RevisionSnapshots); got != 2 {
		t.Fatalf("revision_snapshots len = %d, want 2", got)
	}
	if got := board.RevisionHistory[0].RevisionID; got != "rev-1" {
		t.Fatalf("revision_history[0].revision_id = %q, want rev-1", got)
	}
	if got := board.RevisionHistory[1].ContinuedFromRevisionID; got != "rev-1" {
		t.Fatalf("revision_history[1].continued_from_revision_id = %q, want rev-1", got)
	}
	if got := board.Assets[0].URI; got != "artifact://artifact-1" {
		t.Fatalf("asset uri = %q, want artifact://artifact-1", got)
	}
	if got := len(board.ExportRefs); got != 1 {
		t.Fatalf("export_refs len = %d, want 1", got)
	}

	reloaded := room.NewManager(db)
	if err := reloaded.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reloadedState, found, err := reloaded.GetPhaseState(context.Background(), rm.ID)
	if err != nil {
		t.Fatalf("GetPhaseState: %v", err)
	}
	if !found {
		t.Fatalf("GetPhaseState found=false for room %q", rm.ID)
	}
	reloadedBoard := room.ProjectWhiteboardState(reloadedState)
	if reloadedBoard == nil {
		t.Fatal("reloaded ProjectWhiteboardState returned nil")
	}
	if got := reloadedBoard.UpdatedAt; got != "2026-05-09T19:35:00Z" {
		t.Fatalf("updated_at = %q, want 2026-05-09T19:35:00Z", got)
	}
	if got := len(reloadedBoard.RevisionHistory); got != 2 {
		t.Fatalf("reloaded revision_history len = %d, want 2", got)
	}
	if got := reloadedBoard.RevisionHistory[1].RevisionID; got != "rev-2" {
		t.Fatalf("reloaded revision_history[1].revision_id = %q, want rev-2", got)
	}
	if got := reloadedBoard.RevisionHistory[1].ContinuedFromRevisionID; got != "rev-1" {
		t.Fatalf("reloaded revision_history[1].continued_from_revision_id = %q, want rev-1", got)
	}
	if got := len(reloadedBoard.RevisionSnapshots); got != 2 {
		t.Fatalf("reloaded revision_snapshots len = %d, want 2", got)
	}
	if got := reloadedBoard.RevisionSnapshots[1].RevisionID; got != "rev-2" {
		t.Fatalf("reloaded revision_snapshots[1].revision_id = %q, want rev-2", got)
	}
	if got := reloadedBoard.ExportRefs[0].Name; got != "board-1-r2.png" {
		t.Fatalf("reloaded export_refs[0].name = %q, want board-1-r2.png", got)
	}
}

func TestRoom_WhiteboardStateSupportsEmptyBoard(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	if err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID:       "board-empty",
		SceneSnapshot: map[string]any{},
		Assets:        []room.WhiteboardAssetRef{},
		Notes:         "",
	}); err != nil {
		t.Fatalf("SaveWhiteboardSnapshot empty: %v", err)
	}

	board := room.ProjectWhiteboardState(rm.PhaseState())
	if board == nil {
		t.Fatal("ProjectWhiteboardState returned nil")
	}
	if got := len(board.SceneSnapshot); got != 0 {
		t.Fatalf("scene_snapshot len = %d, want 0", got)
	}
	if got := len(board.Assets); got != 0 {
		t.Fatalf("assets len = %d, want 0", got)
	}
	if got := len(board.RevisionHistory); got != 0 {
		t.Fatalf("revision_history len = %d, want 0", got)
	}
}

func TestRoom_WhiteboardStateRejectsInlineAssetData(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID: "board-inline",
		Assets: []room.WhiteboardAssetRef{
			{
				AssetID: "asset-inline",
				Source:  "data:image/png;base64,AAA",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidWhiteboardAssetRef) {
		t.Fatalf("SaveWhiteboardSnapshot invalid asset err = %v, want ErrInvalidWhiteboardAssetRef", err)
	}
}

func TestRoom_WhiteboardStateRejectsBrowserOnlyAssetURI(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID: "board-inline-uri",
		Assets: []room.WhiteboardAssetRef{
			{
				AssetID: "asset-inline-uri",
				URI:     "blob:local-image",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidWhiteboardAssetRef) {
		t.Fatalf("SaveWhiteboardSnapshot invalid asset URI err = %v, want ErrInvalidWhiteboardAssetRef", err)
	}
}

func TestRoom_WhiteboardStateRejectsBrowserOnlyExportURI(t *testing.T) {
	db := newTestDB(t)
	defer func() { _ = tangentdb.Close(db) }()

	rm := newAnonRoom(t, db)
	err := rm.SaveWhiteboardSnapshot(room.WhiteboardSnapshot{
		BoardID: "board-inline-export-uri",
		ExportRefs: []room.WhiteboardExportRef{
			{
				Name: "board-inline-export.png",
				Kind: "png",
				URI:  "data:image/png;base64,AAA",
			},
		},
	})
	if !errors.Is(err, room.ErrInvalidWhiteboardExportRef) {
		t.Fatalf("SaveWhiteboardSnapshot invalid export URI err = %v, want ErrInvalidWhiteboardExportRef", err)
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
