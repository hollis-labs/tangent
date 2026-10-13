package interaction

import (
	"context"
	"database/sql"
	"fmt"
)

// SurfaceDigestRow is the per-interaction identity that change detection needs:
// enough to notice a new, transitioned or revised interaction without decoding
// its (potentially large) request snapshot or hydrating its resolutions.
type SurfaceDigestRow struct {
	ID              string
	SurfaceSequence int64
	State           InteractionState
	Revision        int64
}

// SurfaceDigest is a cheap summary of one surface for a single definition kind.
type SurfaceDigest struct {
	SurfaceID string
	Revision  int64
	Rows      []SurfaceDigestRow
}

// SurfaceDigest returns the surface revision and, in surface order, the identity
// of every interaction bound to definitionKind. It applies the same
// authorization as GetSurface but reads two narrow queries instead of hydrating
// the whole surface, so event streams can poll it without rebuilding views.
func (s *Service) SurfaceDigest(ctx context.Context, input GetSurfaceInput, definitionKind string) (SurfaceDigest, error) {
	if !s.surfaces.Authorize(input.SurfaceID, input.Capability) {
		return SurfaceDigest{}, ErrUnauthorized
	}
	surface, rows, err := s.store.surfaceDigest(ctx, input.SurfaceID, definitionKind)
	if err != nil {
		return SurfaceDigest{}, err
	}
	if !scopeMatches(input.RequesterScope, surface.OwnerScope) {
		openedByRequester, lookupErr := s.store.SurfaceWasOpenedByScope(ctx, input.SurfaceID, input.RequesterScope)
		if lookupErr != nil {
			return SurfaceDigest{}, lookupErr
		}
		if !openedByRequester {
			return SurfaceDigest{}, denial(input.RequesterScope, surface.OwnerScope)
		}
	}
	return SurfaceDigest{SurfaceID: surface.ID, Revision: surface.Revision, Rows: rows}, nil
}

func (s *Store) surfaceDigest(ctx context.Context, surfaceID, definitionKind string) (SurfaceRecord, []SurfaceDigestRow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SurfaceRecord{}, nil, fmt.Errorf("begin surface digest: %w", err)
	}
	defer rollback(tx)
	surface, err := getSurface(ctx, tx, surfaceID)
	if err != nil {
		return SurfaceRecord{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT i.id, i.surface_sequence, i.lifecycle_state, i.revision
FROM interactions i
JOIN definition_bindings d ON d.interaction_id = i.id
WHERE i.surface_id = ? AND d.kind = ?
ORDER BY i.surface_sequence`, surfaceID, definitionKind)
	if err != nil {
		return SurfaceRecord{}, nil, fmt.Errorf("list surface digest: %w", err)
	}
	defer closeRows(rows)
	var out []SurfaceDigestRow
	for rows.Next() {
		var r SurfaceDigestRow
		var state string
		if err := rows.Scan(&r.ID, &r.SurfaceSequence, &state, &r.Revision); err != nil {
			return SurfaceRecord{}, nil, fmt.Errorf("scan surface digest: %w", err)
		}
		r.State = InteractionState(state)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return SurfaceRecord{}, nil, fmt.Errorf("iterate surface digest: %w", err)
	}
	return surface, out, nil
}
