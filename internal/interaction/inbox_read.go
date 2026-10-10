package interaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// InboxReadSurfacePolicy is an optional host policy for pure inbox reads.
// It does not participate in the capability gate of any lifecycle command.
// The caller must independently pass the per-record View decision.
type InboxReadSurfacePolicy interface {
	AuthorizeInboxRead(surfaceID string) bool
}

// InboxReadInput uses the adapter-established caller scope, not a wire identity.
// CreatedFrom is inclusive and CreatedBefore is exclusive. Query is a literal,
// case-insensitive substring across JSON string values in retained content.
type InboxReadInput struct {
	RequesterScope string
	Kind           string
	Status         InteractionState
	CreatedFrom    *time.Time
	CreatedBefore  *time.Time
	Query          string
	Limit          int
	Cursor         string
}

type InboxResponse struct {
	ResolutionID string          `json:"resolution_id"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload"`
	Redacted     bool            `json:"redacted"`
	RecordedAt   time.Time       `json:"recorded_at"`
}

// InboxReadItem deliberately omits policy, external refs, participant bindings,
// drafts, transport credentials, delivery destinations, and internal audit data.
// List/search return metadata; Get additionally returns retained content.
type InboxReadItem struct {
	ItemID            string           `json:"item_id"`
	SurfaceID         string           `json:"surface_id"`
	Kind              string           `json:"kind"`
	DefinitionVersion string           `json:"definition_version"`
	Status            InteractionState `json:"status"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	RequestRedacted   bool             `json:"request_redacted"`
	Request           json.RawMessage  `json:"request,omitempty"`
	ResponseHistory   []InboxResponse  `json:"response_history,omitempty"`
}

type InboxReadPage struct {
	Items      []InboxReadItem `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type inboxReadRow struct {
	item        InboxReadItem
	sequence    int64
	callerScope string
	ownerScope  string
}

const inboxReadSelect = `
SELECT i.id, i.surface_id, d.kind, d.version, i.lifecycle_state,
 i.created_at, i.updated_at, i.request_snapshot, o.sequence,
 i.caller_scope, s.owner_scope, r.id, r.response_kind, r.response_payload, r.recorded_at
FROM interactions i JOIN definition_bindings d ON d.interaction_id=i.id
JOIN surfaces s ON s.id=i.surface_id JOIN inbox_order o ON o.interaction_id=i.id
LEFT JOIN resolutions r ON r.interaction_id=i.id
WHERE NOT EXISTS (SELECT 1 FROM inbox_hidden_items h WHERE h.interaction_id=i.id)
`

type inboxReadCursor struct {
	Version   int    `json:"version"`
	AfterItem string `json:"after_item"`
	Filter    string `json:"filter"`
}

// ReadInbox is a pure caller read. Authorization precedes text matching and
// pagination, and no foreign rows/counts/sequence positions enter the result.
// Each page is one SQLite snapshot; subsequent pages recheck current retention
// and access. It does not claim presentation, delivery, retrieval, or an ACK.
func (s *Service) ReadInbox(ctx context.Context, input InboxReadInput) (InboxReadPage, error) {
	input, err := validateInboxRead(input)
	if err != nil {
		return InboxReadPage{}, err
	}
	tx, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return InboxReadPage{}, err
	}
	defer rollback(tx)
	after, err := s.inboxCursorPosition(ctx, tx, input)
	if err != nil {
		return InboxReadPage{}, err
	}
	scopes, err := json.Marshal(equivalentScopes(input.RequesterScope))
	if err != nil {
		return InboxReadPage{}, err
	}
	// Scope-keyed admission narrows candidates in SQL. The shared authz decision
	// and host surface policy still run before any candidate becomes observable.
	rows, err := tx.QueryContext(ctx, inboxReadSelect+`
 AND o.sequence > ? AND (? = '' OR d.kind = ?) AND (? = '' OR i.lifecycle_state = ?)
 AND (i.caller_scope IN (SELECT value FROM json_each(?))
 OR s.owner_scope IN (SELECT value FROM json_each(?))) ORDER BY o.sequence`,
		after, input.Kind, input.Kind, input.Status, input.Status, string(scopes), string(scopes))
	if err != nil {
		return InboxReadPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := InboxReadPage{Items: make([]InboxReadItem, 0, input.Limit)}
	for rows.Next() {
		row, scanErr := scanInboxRead(rows)
		if scanErr != nil {
			return InboxReadPage{}, scanErr
		}
		if s.authorizeInboxRead(input.RequesterScope, row) != nil || !matchesInboxRead(input, row.item) {
			continue
		}
		if len(page.Items) == input.Limit {
			cursor, cursorErr := json.Marshal(inboxReadCursor{Version: 1, AfterItem: page.Items[len(page.Items)-1].ItemID, Filter: inboxReadFilter(input)})
			if cursorErr != nil {
				return InboxReadPage{}, cursorErr
			}
			page.NextCursor = base64.RawURLEncoding.EncodeToString(cursor)
			break
		}
		row.item.Request = nil
		row.item.ResponseHistory = nil
		page.Items = append(page.Items, row.item)
	}
	if err := rows.Err(); err != nil {
		return InboxReadPage{}, err
	}
	if err := rows.Close(); err != nil {
		return InboxReadPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxReadPage{}, err
	}
	return page, nil
}

// GetInboxItem inspects the retained caller request and confirmed response
// history atomically. Hidden and purged items are not found; redacted payloads
// remain explicit tombstones. Unsubmitted drafts are not responses.
func (s *Service) GetInboxItem(ctx context.Context, requester, itemID string) (InboxReadItem, error) {
	if strings.TrimSpace(requester) == "" {
		return InboxReadItem{}, ErrUnauthorized
	}
	if itemID == "" || len(itemID) > 256 {
		return InboxReadItem{}, fmt.Errorf("%w: item_id is required and bounded", ErrInvalidRecord)
	}
	tx, err := s.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return InboxReadItem{}, err
	}
	defer rollback(tx)
	row, err := scanInboxRead(tx.QueryRowContext(ctx, inboxReadSelect+` AND i.id=?`, itemID))
	if err != nil {
		return InboxReadItem{}, err
	}
	if err = s.authorizeInboxRead(requester, row); err != nil {
		return InboxReadItem{}, err
	}
	if err = tx.Commit(); err != nil {
		return InboxReadItem{}, err
	}
	return row.item, nil
}

func (s *Service) authorizeInboxRead(requester string, row inboxReadRow) error {
	if !authz.SameAuthority(requester, row.callerScope) {
		return ErrNotFound
	}
	decision := func(owner string) error {
		return authz.Authorize(authz.Request{
			Kind: authz.KindCallerApplication, Scope: requester, OwnerScope: owner,
			Capability: authz.View, Isolation: authz.PartitionScoped,
		})
	}
	callerErr := decision(row.callerScope)
	ownerErr := decision(row.ownerScope)
	if callerErr != nil && ownerErr != nil {
		if errors.Is(callerErr, authz.ErrNotFound) && errors.Is(ownerErr, authz.ErrNotFound) {
			return ErrNotFound
		}
		return ErrUnauthorized
	}
	allowed := s.surfaces.Authorize(row.item.SurfaceID, "")
	if policy, ok := s.surfaces.(InboxReadSurfacePolicy); ok {
		allowed = policy.AuthorizeInboxRead(row.item.SurfaceID)
	}
	if !allowed {
		return ErrUnauthorized
	}
	return nil
}

func validateInboxRead(input InboxReadInput) (InboxReadInput, error) {
	if strings.TrimSpace(input.RequesterScope) == "" {
		return input, ErrUnauthorized
	}
	input.RequesterScope = authz.Normalize(input.RequesterScope)
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 100 || len(input.Cursor) > 2048 || len(input.Kind) > 256 || !utf8.ValidString(input.Kind) {
		return input, fmt.Errorf("%w: invalid inbox bounds", ErrInvalidRecord)
	}
	switch input.Status {
	case "", InteractionStateSubmitted, InteractionStateValidated, InteractionStateStaged, InteractionStatePresented, InteractionStateInProgress, InteractionStateResolved, InteractionStateCanceled, InteractionStateExpired, InteractionStateFailed, InteractionStateSuperseded:
	default:
		return input, fmt.Errorf("%w: unknown inbox status", ErrInvalidRecord)
	}
	input.Query = strings.TrimSpace(input.Query)
	if !utf8.ValidString(input.Query) || len(input.Query) > 1024 {
		return input, fmt.Errorf("%w: query must be at most 1024 UTF-8 bytes", ErrInvalidRecord)
	}
	if input.CreatedFrom != nil && input.CreatedBefore != nil && !input.CreatedFrom.Before(*input.CreatedBefore) {
		return input, fmt.Errorf("%w: invalid creation window", ErrInvalidRecord)
	}
	return input, nil
}

func inboxReadFilter(input InboxReadInput) string {
	encoded, _ := json.Marshal(struct {
		Scope  string
		Kind   string
		Status InteractionState
		From   *time.Time
		Before *time.Time
		Query  string
	}{input.RequesterScope, input.Kind, input.Status, input.CreatedFrom, input.CreatedBefore, input.Query})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Service) inboxCursorPosition(ctx context.Context, tx *sql.Tx, input InboxReadInput) (int64, error) {
	if input.Cursor == "" {
		return 0, nil
	}
	invalid := fmt.Errorf("%w: invalid inbox cursor", ErrInvalidRecord)
	encoded, err := base64.RawURLEncoding.DecodeString(input.Cursor)
	if err != nil {
		return 0, invalid
	}
	var cursor inboxReadCursor
	if json.Unmarshal(encoded, &cursor) != nil || cursor.Version != 1 || cursor.Filter != inboxReadFilter(input) || cursor.AfterItem == "" {
		return 0, invalid
	}
	row, err := scanInboxRead(tx.QueryRowContext(ctx, inboxReadSelect+` AND i.id=?`, cursor.AfterItem))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, invalid
		}
		return 0, err
	}
	if s.authorizeInboxRead(input.RequesterScope, row) != nil {
		return 0, invalid
	}
	return row.sequence, nil
}

func scanInboxRead(row scanner) (inboxReadRow, error) {
	var result inboxReadRow
	var created, updated, recorded any
	var request string
	var resolutionID, responseKind, response sql.NullString
	err := row.Scan(&result.item.ItemID, &result.item.SurfaceID, &result.item.Kind, &result.item.DefinitionVersion, &result.item.Status,
		&created, &updated, &request, &result.sequence, &result.callerScope, &result.ownerScope, &resolutionID, &responseKind, &response, &recorded)
	if err != nil {
		return result, scanError("inbox item", err)
	}
	result.item.CreatedAt, err = parseTime(created)
	if err != nil {
		return result, err
	}
	result.item.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return result, err
	}
	result.item.Request = json.RawMessage(request)
	result.item.RequestRedacted = tangentdb.IsRedacted(request)
	if resolutionID.Valid {
		at, timeErr := parseTime(recorded)
		if timeErr != nil {
			return result, timeErr
		}
		result.item.ResponseHistory = []InboxResponse{{ResolutionID: resolutionID.String, Kind: responseKind.String, Payload: json.RawMessage(response.String), Redacted: tangentdb.IsRedacted(response.String), RecordedAt: at}}
	}
	return result, nil
}

func matchesInboxRead(input InboxReadInput, item InboxReadItem) bool {
	if input.Kind != "" && input.Kind != item.Kind || input.Status != "" && input.Status != item.Status {
		return false
	}
	if input.CreatedFrom != nil && item.CreatedAt.Before(*input.CreatedFrom) || input.CreatedBefore != nil && !item.CreatedAt.Before(*input.CreatedBefore) {
		return false
	}
	if input.Query == "" {
		return true
	}
	query := strings.ToLower(input.Query)
	if !item.RequestRedacted && inboxTextContains(item.Request, query) {
		return true
	}
	for _, response := range item.ResponseHistory {
		if !response.Redacted && inboxTextContains(response.Payload, query) {
			return true
		}
	}
	return false
}

// Match values rather than serialized JSON keys, escapes or tombstone metadata.
func inboxTextContains(payload json.RawMessage, query string) bool {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	var contains func(any) bool
	contains = func(value any) bool {
		switch typed := value.(type) {
		case string:
			return strings.Contains(strings.ToLower(typed), query)
		case []any:
			for _, element := range typed {
				if contains(element) {
					return true
				}
			}
		case map[string]any:
			for _, element := range typed {
				if contains(element) {
					return true
				}
			}
		}
		return false
	}
	return contains(value)
}
