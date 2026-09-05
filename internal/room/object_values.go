package room

import "fmt"

// Shared JSON-object helpers for the per-kind phase projections.
//
// They lived in form_state.go until CW-20260825-0074 moved tangent.form-collect
// out to its own interaction package, which made visible that six other
// projections had been depending on helpers parked in a seventh's file. They
// are workflow-neutral — nothing about them knows a kind — so they belong
// beside the phase substrate rather than inside whichever projection happened
// to define them first.

// normalizeObjectMap round-trips a map through JSON so what is persisted is
// exactly what will be read back, and returns invalid when the value does not
// survive as an object.
func normalizeObjectMap(input map[string]any, invalid error) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	normalized, err := normalizeJSONValue(cloneAnyMap(input))
	if err != nil {
		return nil, fmt.Errorf("room: normalize object map: %w", err)
	}
	record, ok := normalized.(map[string]any)
	if !ok {
		return nil, invalid
	}
	return record, nil
}

// readObjectMapValue reads a nested object out of a persisted blob, returning
// an empty map rather than nil so a projection never hands a caller a map it
// cannot range over.
func readObjectMapValue(raw any) map[string]any {
	record, _ := raw.(map[string]any)
	if record == nil {
		return map[string]any{}
	}
	return cloneAnyMap(record)
}
