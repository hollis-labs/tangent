package interaction

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/tangent/internal/authz"
)

// InboxEntry indexes one canonical interaction in the global arrival order.
type InboxEntry struct {
	Sequence    int64             `json:"sequence"`
	Interaction InteractionRecord `json:"interaction"`
	Resolution  *ResolutionRecord `json:"resolution,omitempty"`
}

// BrowserInbox is a pure operator read. It never claims a presentation lease,
// retrieves a caller outcome, or acknowledges delivery. Like the browser room
// listing, it is authority-wide within the admitted local caller authority.
func (s *Service) BrowserInbox(ctx context.Context) ([]InboxEntry, error) {
	entries, err := s.store.listInbox(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]InboxEntry, 0, len(entries))
	for _, entry := range entries {
		if s.browserMayView(entry) {
			visible = append(visible, entry)
		}
	}
	return visible, nil
}

// browserMayView decides whether the operator's browser lists one entry.
//
// An entry from the operator's own authority is always listed. An entry from
// the verified gateway is listed too: the gateway is the operator's own front
// door for the agents of the fabric, its authority is assigned by the host from
// an authenticated token and can never be asserted by a local caller, and a
// room workflow an agent sends through it (triage, diff review, forms) is
// addressed to the operator by definition. An entry from any other authority is
// listed only when it sits on a surface reserved for the operator inbox (docs,
// artifacts, turns, approvals): only the host's own enqueue tools can write
// there. A producer's own surfaces and any generic surface stay private to that
// producer, as before.
func (s *Service) browserMayView(entry InboxEntry) bool {
	if authz.Authorize(authz.Request{
		Kind:       authz.KindCallerApplication,
		Scope:      authz.CallerScope(""),
		OwnerScope: entry.Interaction.CallerScope,
		Capability: authz.View,
		Isolation:  authz.AuthorityWide,
	}) == nil {
		return true
	}
	if authz.ParseScope(entry.Interaction.CallerScope).Authority == authz.AuthorityGateway {
		return true
	}
	return s.surfaces != nil && !s.surfaces.Authorize(entry.Interaction.SurfaceID, "")
}

func (s *Store) listInbox(ctx context.Context) ([]InboxEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin inbox read: %w", err)
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, interactionSelect+`
JOIN inbox_order o ON o.interaction_id = i.id
WHERE NOT EXISTS (SELECT 1 FROM inbox_hidden_items h WHERE h.interaction_id = i.id)
ORDER BY o.sequence`)
	if err != nil {
		return nil, fmt.Errorf("list inbox interactions: %w", err)
	}
	entries := make([]InboxEntry, 0)
	for rows.Next() {
		record, scanErr := scanInteractionWithDefinition(rows)
		if scanErr != nil {
			closeRows(rows)
			return nil, scanErr
		}
		entries = append(entries, InboxEntry{Interaction: record})
	}
	rowsErr := rows.Err()
	closeRows(rows)
	if rowsErr != nil {
		return nil, rowsErr
	}
	// The transaction pins arrival order, request state and response evidence
	// to one snapshot. Bulk reads keep polling independent of queue length.
	positions := make(map[string]int, len(entries))
	for idx := range entries {
		positions[entries[idx].Interaction.ID] = idx
	}
	orderRows, err := tx.QueryContext(ctx, `SELECT interaction_id, sequence FROM inbox_order`)
	if err != nil {
		return nil, err
	}
	for orderRows.Next() {
		var id string
		var sequence int64
		if scanErr := orderRows.Scan(&id, &sequence); scanErr != nil {
			closeRows(orderRows)
			return nil, scanErr
		}
		if idx, ok := positions[id]; ok {
			entries[idx].Sequence = sequence
		}
	}
	orderErr := orderRows.Err()
	closeRows(orderRows)
	if orderErr != nil {
		return nil, orderErr
	}
	resolutionRows, err := tx.QueryContext(ctx, `SELECT id, interaction_id, expected_interaction_revision,
		presented_projection_revision, participant_scope, participant_ref, participant_authority,
		participant_assurance, response_kind, response_payload, source_draft_revision,
		integrity_digest, submitted_at, validated_at, recorded_at FROM resolutions`)
	if err != nil {
		return nil, err
	}
	for resolutionRows.Next() {
		resolution, err := scanResolution(resolutionRows)
		if err != nil {
			closeRows(resolutionRows)
			return nil, err
		}
		if idx, ok := positions[resolution.InteractionID]; ok {
			entries[idx].Resolution = &resolution
		}
	}
	resolutionErr := resolutionRows.Err()
	closeRows(resolutionRows)
	if resolutionErr != nil {
		return nil, resolutionErr
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return entries, nil
}

// authorizeOperatorInbox admits the operator's own Inbox actions on one
// interaction: anything the browser may list. Items from the verified gateway
// and from other producers on reserved surfaces are the operator's to manage;
// the old owner-scope check refused exactly the items agents send through the
// gateway, which is why a form or review could never be cleared.
func (s *Service) authorizeOperatorInbox(record InteractionRecord) error {
	if s.browserMayView(InboxEntry{Interaction: record}) {
		return nil
	}
	return ErrUnauthorized
}

// HideBrowserInboxItem hides a terminal request from the operator's Inbox;
// canonical evidence and caller delivery remain available.
func (s *Service) HideBrowserInboxItem(ctx context.Context, id string) error {
	record, err := s.store.GetInteraction(ctx, id)
	if err != nil {
		return err
	}
	if err = s.authorizeOperatorInbox(record); err != nil {
		return err
	}
	switch record.State {
	case InteractionStateResolved, InteractionStateCanceled, InteractionStateExpired, InteractionStateFailed, InteractionStateSuperseded:
	default:
		return fmt.Errorf("inbox: active interaction must be closed before deletion")
	}
	_, err = s.store.db.ExecContext(ctx, `INSERT INTO inbox_hidden_items (interaction_id) VALUES (?) ON CONFLICT(interaction_id) DO NOTHING`, id)
	return err
}

// HiddenInboxItems returns operator visibility metadata, not interaction state.
func (s *Service) HiddenInboxItems(ctx context.Context) (map[string]bool, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT interaction_id FROM inbox_hidden_items`)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	hidden := make(map[string]bool)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		hidden[id] = true
	}
	return hidden, rows.Err()
}

// BrowserInboxItem is the keyed read behind opening one Inbox row: the same entry
// BrowserInbox would list for id (same visibility and authority rules), fetched by key
// instead of by listing and filtering every interaction. It returns ErrNotFound for an
// unknown, hidden or not-visible id.
func (s *Service) BrowserInboxItem(ctx context.Context, id string) (InboxEntry, error) {
	entry, err := s.store.getInboxEntry(ctx, id)
	if err != nil {
		return InboxEntry{}, err
	}
	if !s.browserMayView(entry) {
		return InboxEntry{}, ErrNotFound
	}
	return entry, nil
}

func (s *Store) getInboxEntry(ctx context.Context, id string) (InboxEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InboxEntry{}, fmt.Errorf("begin inbox item read: %w", err)
	}
	defer rollback(tx)
	record, err := scanInteractionWithDefinition(tx.QueryRowContext(ctx, interactionSelect+`
JOIN inbox_order o ON o.interaction_id = i.id
WHERE i.id = ? AND NOT EXISTS (SELECT 1 FROM inbox_hidden_items h WHERE h.interaction_id = i.id)`, id))
	if err != nil {
		return InboxEntry{}, err
	}
	entry := InboxEntry{Interaction: record}
	if err = tx.QueryRowContext(ctx, `SELECT sequence FROM inbox_order WHERE interaction_id = ?`, id).Scan(&entry.Sequence); err != nil {
		return InboxEntry{}, err
	}
	resolution, err := scanResolution(tx.QueryRowContext(ctx, `
SELECT id, interaction_id, expected_interaction_revision,
       presented_projection_revision, participant_scope, participant_ref, participant_authority,
       participant_assurance, response_kind, response_payload,
       source_draft_revision, integrity_digest,
       submitted_at, validated_at, recorded_at
FROM resolutions WHERE interaction_id = ?`, id))
	switch {
	case err == nil:
		entry.Resolution = &resolution
	case errors.Is(err, ErrNotFound):
	default:
		return InboxEntry{}, err
	}
	if err = tx.Commit(); err != nil {
		return InboxEntry{}, err
	}
	return entry, nil
}
