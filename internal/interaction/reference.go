package interaction

import (
	"context"
	"fmt"
)

// SurfaceReferenceSnapshot is the smallest coherent durable projection needed
// to display a Tangent surface or interaction as read-only evidence. It omits
// drafts, delivery records, and audit journals on purpose.
type SurfaceReferenceSnapshot struct {
	Surface     SurfaceRecord      `json:"surface"`
	Interaction *InteractionRecord `json:"interaction,omitempty"`
}

// InspectSurfaceReference reads a durable surface projection without recording
// a presentation, retrieval, or lifecycle event. The capability is supplied by
// an in-process adapter and must never be accepted from a wire request.
func (s *Service) InspectSurfaceReference(
	ctx context.Context,
	surfaceID string,
	interactionID string,
	capability string,
) (SurfaceReferenceSnapshot, error) {
	if surfaceID == "" {
		return SurfaceReferenceSnapshot{}, fmt.Errorf("%w: surface_id is required", ErrInvalidRecord)
	}
	if !s.surfaces.Authorize(surfaceID, capability) {
		return SurfaceReferenceSnapshot{}, ErrUnauthorized
	}
	snapshot, err := s.store.HydrateSurfaceInteractions(ctx, surfaceID)
	if err != nil {
		return SurfaceReferenceSnapshot{}, err
	}
	result := SurfaceReferenceSnapshot{Surface: snapshot.Surface}
	if interactionID == "" {
		return result, nil
	}
	for index := range snapshot.Interactions {
		if snapshot.Interactions[index].ID == interactionID {
			record := snapshot.Interactions[index]
			result.Interaction = &record
			return result, nil
		}
	}
	return SurfaceReferenceSnapshot{}, ErrNotFound
}
