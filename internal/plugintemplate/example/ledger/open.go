package ledger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// Opening a board.
//
// The whole flow is four steps and none of them is a judgement call: query
// Ledger, shape the cards, create a room, present the envelope. It runs through
// Tangent's own tool surface (pluginhost/tools.go) rather than through a private
// host API, so the plugin is a caller of the same tools an agent calls, with the
// same authority and no more.
//
// The envelope goes out with completion mode "async". That is what keeps the
// board open: the receipt returns immediately, nothing is cancelled, and the
// interaction stays pending for as long as the participant works in it. A
// "wait" here would block this call across a human decision, which for a board
// means forever.

// metaFiltersKey is where the board's originating filters are recorded.
//
// They have to survive somewhere, because a sync re-queries Ledger with the
// SAME filters and nothing else knows what they were. The envelope's own request
// snapshot is the right place: immutable, already durable, and read back from
// the interaction record — so a sync cannot re-query with filters that drifted
// from the ones the board was opened with.
const metaFiltersKey = "ledger_board_filters"

// metaBoardKey records the board id on the envelope alongside the filters.
const metaBoardKey = "ledger_board_id"

// OpenInput is the open tool's arguments.
type OpenInput struct {
	Title    string   `json:"title,omitempty"`
	Statuses []string `json:"statuses,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Search   string   `json:"search,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	RoomID   string   `json:"room_id,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
}

// filters projects the tool input onto the Ledger query.
func (in OpenInput) filters() ListFilters {
	statuses := in.Statuses
	if len(statuses) == 0 {
		statuses = ActiveStatuses
	}
	return ListFilters{
		Statuses: statuses, Tags: in.Tags, Search: in.Search,
		Limit: clampCards(in.Limit),
	}
}

// OpenResult is what the open tool returns.
type OpenResult struct {
	RoomID string `json:"room_id"`
	URL    string `json:"url"`
	// BoardID is the handle a sync names alongside the room.
	BoardID string `json:"board_id"`
	// Cards is how many records were sent. It is the number the board's own
	// filter bar narrows.
	Cards int `json:"cards"`
	// Truncated says Ledger held more matches than the board could send. The
	// participant is told in the scope line; the agent is told here, because an
	// agent that opened a board over a cut set and does not know it will reason
	// about the cards as though they were the whole answer.
	Truncated bool `json:"truncated"`
	// Scope says which records were sent, in the same words the board shows.
	Scope string `json:"scope"`
	// Source is where the cards came from.
	Source string `json:"source"`
}

// Open queries Ledger, shapes a board, and presents it in a room.
func (p *Plugin) Open(ctx context.Context, input OpenInput) (OpenResult, error) {
	tools, err := p.tools()
	if err != nil {
		return OpenResult{}, err
	}

	filters := input.filters()
	page, err := p.client.List(ctx, filters)
	if err != nil {
		return OpenResult{}, err
	}

	roomID := input.RoomID
	url := ""
	if roomID == "" {
		// The room is created only after Ledger has answered, so an outage
		// leaves no empty room behind.
		roomID, url, err = createRoom(ctx, tools, p.boardTitle(input, filters))
		if err != nil {
			return OpenResult{}, err
		}
	}

	boardID := newBoardID()
	data := p.boardData(boardID, input, filters, page)
	envelope := boardEnvelope(newEnvelopeID(), p.boardTitle(input, filters), data, filters)

	if err := advance(ctx, tools, roomID, envelope); err != nil {
		return OpenResult{}, err
	}
	return OpenResult{
		RoomID: roomID, URL: url, BoardID: boardID,
		Cards: len(page.Records), Truncated: page.More,
		Scope: data.Sync.scopeOrEmpty(), Source: p.client.BaseURL(),
	}, nil
}

// boardData assembles the envelope's data block.
func (p *Plugin) boardData(
	boardID string,
	input OpenInput,
	filters ListFilters,
	page RecordPage,
) BoardData {
	sync := &Sync{
		Enabled:    !input.ReadOnly,
		Endpoint:   SyncPath,
		Label:      "Sync",
		StageLabel: "Move to",
		Scope:      ScopeSentence(filters, page),
	}
	if input.ReadOnly {
		sync.Label = ""
		sync.StageLabel = ""
	}
	return BuildBoard(
		boardID,
		p.boardTitle(input, filters),
		page.Records,
		filters.Statuses,
		Source{App: "ledger", Label: "Ledger · " + p.client.BaseURL()},
		sync,
		nowRFC3339(),
	)
}

func (p *Plugin) boardTitle(input OpenInput, filters ListFilters) string {
	if input.Title != "" {
		return input.Title
	}
	if len(filters.Tags) > 0 {
		return "Ledger — tagged " + strings.Join(filters.Tags, "/")
	}
	return "Ledger — active work"
}

// boardEnvelope wraps the board data in an envelope of the kind this plugin
// fills.
//
// The filters ride in `meta` so a later sync can read them back off the
// immutable request snapshot rather than guessing or being told again.
func boardEnvelope(
	id, title string,
	data BoardData,
	filters ListFilters,
) map[string]any {
	meta := map[string]any{
		metaBoardKey:   data.BoardID,
		metaFiltersKey: filters,
	}
	return map[string]any{
		"v":            1,
		"id":           id,
		"type":         EnvelopeType,
		"title":        title,
		"presentation": "fullscreen",
		"data":         data,
		"meta":         meta,
	}
}

// createRoom opens a browser room through Tangent's own tool.
func createRoom(
	ctx context.Context,
	tools pluginhost.ToolCaller,
	title string,
) (roomID string, url string, err error) {
	result, err := tools.CallTool(ctx, "tangent.session_create", map[string]any{
		"title": title,
		"meta":  map[string]any{"app": "ledger", "surface": "board"},
	})
	if err != nil {
		return "", "", err
	}
	var created struct {
		RoomID string `json:"roomID"`
		URL    string `json:"url"`
	}
	if err := result.Unmarshal(&created); err != nil {
		return "", "", fmt.Errorf("ledger: create room: %w", err)
	}
	if created.RoomID == "" {
		return "", "", fmt.Errorf("ledger: tangent.session_create returned no room id")
	}
	return created.RoomID, created.URL, nil
}

// advance presents the envelope on the room and returns as soon as the durable
// receipt exists.
func advance(
	ctx context.Context,
	tools pluginhost.ToolCaller,
	roomID string,
	envelope map[string]any,
) error {
	result, err := tools.CallTool(ctx, "tangent.session_advance", map[string]any{
		"roomID":     roomID,
		"envelope":   envelope,
		"completion": map[string]any{"mode": "async"},
	})
	if err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("ledger: present board: %s", string(result.Content))
	}
	return nil
}

// nowRFC3339 is the timestamp a fresh board carries. It is one function so the
// open and sync paths cannot stamp a board differently.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func newBoardID() string { return "ledger-board-" + randomSuffix() }

func newEnvelopeID() string { return "ledger-board-env-" + randomSuffix() }

// randomSuffix is a short unique tail. Envelope identity is what Tangent's
// idempotency is keyed on, so two boards opened in the same second must not
// collide — a timestamp alone would.
func randomSuffix() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
