package service

// DeepSeekRecoveryToolPayloadPresent distinguishes empty compatibility fields
// from a tool event. Any nonempty carrier remains a no-replay boundary, even
// when its name or arguments have not arrived yet.
func DeepSeekRecoveryToolPayloadPresent(kind string, value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case []any:
		return len(v) != 0
	case map[string]any:
		if len(v) == 0 {
			return false
		}
		if kind == "function_call" {
			for key, field := range v {
				if key != "name" && key != "arguments" {
					return true
				}
				if field == nil {
					continue
				}
				if text, ok := field.(string); !ok || text != "" {
					return true
				}
			}
			return false
		}
	}
	return true
}
