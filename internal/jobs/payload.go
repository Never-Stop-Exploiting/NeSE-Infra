package jobs

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// JobPayload represents the scheduler message runners consume.
type JobPayload struct {
	RunID          string    `json:"run_id"`
	ExploitID      string    `json:"exploit_id"`
	ExploitName    string    `json:"exploit_name"`
	TargetID       string    `json:"target_id"`
	TargetIP       string    `json:"target_ip"`
	TargetPort     int       `json:"target_port"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	ArtifactBase64 string    `json:"artifact_base64"`
	ArtifactURI    string    `json:"artifact_uri,omitempty"`
	ArtifactMD5    string    `json:"artifact_md5,omitempty"`
	FlagRegex      string    `json:"flag_regex"`
	FlagFile       string    `json:"flag_file"`
	RunScript      string    `json:"run_script,omitempty"`
	Entrypoint     string    `json:"entrypoint,omitempty"`
	RunnerTags     []string  `json:"runner_tags,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

// DecodeJobPayload parses a queue message body into JobPayload.
func DecodeJobPayload(raw []byte) (JobPayload, error) {
	var payload JobPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return JobPayload{}, fmt.Errorf("unmarshal job payload: %w", err)
	}
	if payload.RunID == "" || payload.TargetIP == "" {
		return JobPayload{}, fmt.Errorf("missing required job fields")
	}
	if payload.ArtifactBase64 == "" {
		if payload.ArtifactURI == "" || payload.ArtifactMD5 == "" {
			return JobPayload{}, fmt.Errorf("missing artifact data")
		}
	}
	return payload, nil
}

// MarshalJobPayload serializes the payload into JSON.
func MarshalJobPayload(payload JobPayload) ([]byte, error) {
	if payload.RunID == "" || payload.TargetIP == "" {
		return nil, fmt.Errorf("invalid job payload: incomplete fields")
	}
	if payload.ArtifactBase64 == "" {
		if payload.ArtifactURI == "" || payload.ArtifactMD5 == "" {
			return nil, fmt.Errorf("invalid job payload: incomplete fields")
		}
	}
	if payload.CreatedAt.IsZero() {
		payload.CreatedAt = time.Now().UTC()
	}
	return json.Marshal(payload)
}

// ArtifactBytes decodes base64 encoded ZIP bytes.
func (p JobPayload) ArtifactBytes() ([]byte, error) {
	if p.ArtifactBase64 == "" {
		return nil, fmt.Errorf("no inline artifact")
	}
	data, err := base64.StdEncoding.DecodeString(p.ArtifactBase64)
	if err != nil {
		return nil, fmt.Errorf("decode artifact: %w", err)
	}
	return data, nil
}

// Timeout derives the desired execution timeout.
func (p JobPayload) Timeout(fallback time.Duration) time.Duration {
	if p.TimeoutSeconds > 0 {
		return time.Duration(p.TimeoutSeconds) * time.Second
	}
	return fallback
}

// CreationTime returns when the job was enqueued, if present.
func (p JobPayload) CreationTime() time.Time {
	return p.CreatedAt
}

// FlagFileName returns the preferred flag file name.
func (p JobPayload) FlagFileName(defaultName string) string {
	if p.FlagFile != "" {
		return p.FlagFile
	}
	return defaultName
}
