package jobs

import "time"

// HintsFromMetadata extracts optional flag regex/file and timeout hints from exploit metadata.
func HintsFromMetadata(meta map[string]any) (string, string, time.Duration) {
	if meta == nil {
		return "", "", 0
	}

	flagRegex := ""
	flagFile := ""
	timeout := time.Duration(0)

	if value, ok := meta["flag_regex"].(string); ok {
		flagRegex = value
	} else if value, ok := meta["flagRegex"].(string); ok {
		flagRegex = value
	}

	if value, ok := meta["flag_file"].(string); ok {
		flagFile = value
	} else if value, ok := meta["flagFile"].(string); ok {
		flagFile = value
	}

	timeoutKeys := []string{"timeout_seconds", "timeoutSeconds"}
	for _, key := range timeoutKeys {
		if timeout > 0 {
			break
		}
		switch value := meta[key].(type) {
		case float64:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int32:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int64:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		}
	}

	return flagRegex, flagFile, timeout
}
