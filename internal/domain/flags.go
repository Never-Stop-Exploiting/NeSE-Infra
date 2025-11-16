package domain

import "strings"

// DefaultFlagRegex is the fallback pattern when no custom regex is configured.
const DefaultFlagRegex = `flag\{[a-zA-Z0-9_\-]+\}`

// ResolveFlagRegex picks the first non-empty flag regex, falling back to the defaults.
func ResolveFlagRegex(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return DefaultFlagRegex
}
