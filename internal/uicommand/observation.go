package uicommand

import (
	"context"
	"encoding/json"
	"errors"
)

// Observation obtains a fresh authorized view and projects client_context v1.
// This is data, never a grant or an installed tool definition. Consumers must
// call again for every turn/retry and omit context on absence; they must not
// persist or silently reuse this return value. The host's attachment cache and
// a consumer's immutable per-turn snapshot have different lifetimes.
func (b *Broker) Observation(ctx context.Context) (json.RawMessage, error) {
	snapshot, err := b.Get(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(snapshot.Descriptor)
	if err != nil {
		return nil, err
	}
	var view map[string]json.RawMessage
	if decodeErr := json.Unmarshal(raw, &view); decodeErr != nil {
		return nil, decodeErr
	}
	delete(view, "version")
	projection, err := json.Marshal(struct {
		Version int                        `json:"version"`
		View    map[string]json.RawMessage `json:"view"`
	}{Version: 1, View: view})
	if err != nil {
		return nil, err
	}
	if len(projection) > MaxDescriptorBytes {
		return nil, errors.New("normalized client_context too large")
	}
	// The envelope adds two levels/nodes to the view. Refuse overflow rather
	// than truncate observations that were valid at the attachment boundary.
	if err := checkJSON(projection); err != nil {
		return nil, err
	}
	return projection, nil
}
