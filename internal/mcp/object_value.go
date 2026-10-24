package mcp

// readObjectValue returns a defensive copy of the nested object at key, or an
// empty map when the key is absent or not an object.
func readObjectValue(data map[string]any, key string) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	value, _ := data[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return cloneAnyMapForDispatch(value)
}
