package queue

import (
	"fmt"
	"regexp"
	"strings"
)

var runnerIDCleaner = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// RunnerQueueName builds a deterministic stream name for a specific runner instance.
func RunnerQueueName(prefix string, runnerID string, instanceID string) string {
	base := strings.TrimSpace(prefix)
	if base == "" {
		base = "runner"
	}
	id := strings.TrimSpace(runnerID)
	if id == "" {
		id = "anonymous"
	}
	id = runnerIDCleaner.ReplaceAllString(strings.ToLower(id), "-")
	instance := strings.TrimSpace(instanceID)
	if instance != "" {
		instance = runnerIDCleaner.ReplaceAllString(strings.ToLower(instance), "-")
		id = fmt.Sprintf("%s:%s", id, instance)
	}
	return fmt.Sprintf("%s:%s", strings.TrimSuffix(base, ":"), id)
}
