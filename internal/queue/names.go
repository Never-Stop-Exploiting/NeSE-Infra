package queue

import (
	"fmt"
	"regexp"
	"strings"
)

var runnerIDCleaner = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// RunnerQueueName builds a deterministic stream name for a particular runner ID.
func RunnerQueueName(prefix string, runnerID string) string {
	base := strings.TrimSpace(prefix)
	if base == "" {
		base = "runner"
	}
	id := strings.TrimSpace(runnerID)
	if id == "" {
		id = "anonymous"
	}
	id = runnerIDCleaner.ReplaceAllString(strings.ToLower(id), "-")
	return fmt.Sprintf("%s:%s", strings.TrimSuffix(base, ":"), id)
}
