package events

import (
	"encoding/json"
	"time"
)

const (
	RunnerEventTypeStatus   = "runner-status"
	RunnerEventTypeInstance = "runner-instance"
)

// RunnerEvent denotes changes in runner lifecycle that downstream components can react to.
type RunnerEvent struct {
	Type       string    `json:"type"`
	RunnerID   string    `json:"runnerId"`
	InstanceID string    `json:"instanceId,omitempty"`
	Status     string    `json:"status,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ForceDrain bool      `json:"forceDrain,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// Encode serializes a runner event into JSON.
func Encode(event RunnerEvent) ([]byte, error) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return json.Marshal(event)
}

// Decode parses a runner event from JSON bytes.
func Decode(data []byte) (RunnerEvent, error) {
	var evt RunnerEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return RunnerEvent{}, err
	}
	return evt, nil
}
