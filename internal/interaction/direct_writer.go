package interaction

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The helpers below let a sibling store that writes interaction rows directly
// (internal/hitl/sqlstore, which holds the go-hitl Store contract's own
// revision numbers and request bytes) write exactly what the native paths
// write, so Tangent's inbox, resolve and delivery machinery treat its rows like
// any other. Each takes the caller's open transaction.

// InsertDefinitionBinding pins the definition an interaction was cut from.
func InsertDefinitionBinding(ctx context.Context, tx *sql.Tx, interactionID string, d DefinitionBinding, at time.Time) error {
	return insertDefinitionBinding(ctx, tx, interactionID, d, at)
}

// QueueResolutionDelivery records the durable caller-pull delivery obligation
// of a resolution, as ResolveInteraction does. expectedRevision is the
// interaction revision the resolution was taken at.
func QueueResolutionDelivery(
	ctx context.Context, tx *sql.Tx, interactionID, resolutionID, callerScope string,
	expectedRevision int64, at time.Time,
) error {
	store := &Store{id: uuid.NewString}
	deliveryID := store.id()
	params := callerPullResolutionDelivery(InteractionRecord{ID: interactionID, CallerScope: callerScope, Revision: expectedRevision})
	if _, err := tx.ExecContext(ctx, `
INSERT INTO resolution_deliveries (
  id, resolution_id, destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'queued', 1, ?, ?)`,
		deliveryID, resolutionID, string(params.DestinationBinding), params.IdempotencyKey, string(params.Policy), at, at,
	); err != nil {
		return fmt.Errorf("insert resolution delivery: %w", err)
	}
	return store.insertDeliveryEvent(ctx, tx, DeliveryKindResolution, deliveryID, "delivery.queued",
		0, 1, nil, mustJSON(map[string]any{"resolution_id": resolutionID}), at)
}

// QueueTerminalNotification records the durable caller-pull notification of a
// non-resolution terminal outcome (cancel, supersede, expire), as
// TerminalizeInteraction does.
func QueueTerminalNotification(
	ctx context.Context, tx *sql.Tx, interactionID, callerScope, operation string,
	to InteractionState, cause TerminalCause, expectedRevision int64, at time.Time,
) error {
	store := &Store{id: uuid.NewString}
	id := store.id()
	params := callerPullTerminalNotification(InteractionRecord{ID: interactionID, CallerScope: callerScope, Revision: expectedRevision}, operation)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO terminal_notifications (
  id, interaction_id, terminal_state, terminal_cause,
  destination_binding, idempotency_key, policy,
  lifecycle_state, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', 1, ?, ?)`,
		id, interactionID, to, nullString(string(cause)),
		string(params.DestinationBinding), params.IdempotencyKey, string(params.Policy), at, at,
	); err != nil {
		return fmt.Errorf("insert terminal notification: %w", err)
	}
	return store.insertDeliveryEvent(ctx, tx, DeliveryKindTerminalNotification, id, "delivery.queued",
		0, 1, nil, mustJSON(map[string]any{"interaction_id": interactionID}), at)
}
