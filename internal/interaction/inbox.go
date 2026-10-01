package interaction

import (
	"context"
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
		if authz.Authorize(authz.Request{
			Kind:       authz.KindCallerApplication,
			Scope:      authz.CallerScope(""),
			OwnerScope: entry.Interaction.CallerScope,
			Capability: authz.View,
			Isolation:  authz.AuthorityWide,
		}) == nil {
			visible = append(visible, entry)
		}
	}
	return visible, nil
}

func (s *Store) listInbox(ctx context.Context) ([]InboxEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin inbox read: %w", err)
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, interactionSelect+`
JOIN inbox_order o ON o.interaction_id = i.id
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
