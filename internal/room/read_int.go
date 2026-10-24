package room

// readInt reads a numeric field from a decoded JSON record, tolerating the
// integer and float forms a JSON round-trip can produce. Missing or
// non-numeric values read as zero.
func readInt(record map[string]any, key string) int {
	if record == nil {
		return 0
	}
	switch value := record[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
