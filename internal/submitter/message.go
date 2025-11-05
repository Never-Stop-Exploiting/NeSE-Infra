package submitter

import (
	"encoding/json"
	"time"
)

// QueueMessage represents a task handed off to the flag submitter via Redis.
type QueueMessage struct {
	FlagID      string            `json:"flag_id"`
	FlagValue   string            `json:"flag_value"`
	ChallengeID string            `json:"challenge_id"`
	TargetID    string            `json:"target_id"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

func EncodeMessage(msg QueueMessage) ([]byte, error) {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	return json.Marshal(msg)
}

func DecodeMessage(data []byte) (QueueMessage, error) {
	var msg QueueMessage
	err := json.Unmarshal(data, &msg)
	return msg, err
}
