package domain

import (
	"time"
)

// Challenge models a single problem across the platform.
type Challenge struct {
	ID          string
	Name        string
	Category    string
	ServiceType string
	Description string
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Target represents an attackable service instance belonging to a remote team.
type Target struct {
	ID               string
	ChallengeID      string
	TeamName         string
	IP               string
	Port             int
	Status           string
	ConcurrencyLimit int
	Labels           map[string]string
	LastSeenAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Exploit captures an exploit artifact and scheduling preferences.
type Exploit struct {
	ID             string
	ChallengeID    string
	Name           string
	Description    string
	Enabled        bool
	RunnerTags     []string
	ScheduleWeight int
	Cooldown       time.Duration
	LastRunAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Metadata       map[string]any
}

// ExploitRevision references immutable build artifacts for reproducible runs.
type ExploitRevision struct {
	ID         string
	ExploitID  string
	Digest     string
	StorageURI string
	Command    []string
	Env        map[string]string
	CreatedAt  time.Time
}

// Runner represents an execution worker registered to the control plane.
type Runner struct {
	ID            string
	Type          string
	Hostname      string
	Labels        map[string]string
	MaxParallel   int
	LastHeartbeat time.Time
	Status        string
	RegisteredAt  time.Time
}

// Run links an exploit revision to a target execution attempt.
type Run struct {
	ID              string
	ExploitID       string
	ExploitRevision string
	TargetID        string
	RunnerID        string
	Status          RunStatus
	QueuedAt        time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	LogURI          string
	FlagID          string
	ErrorMessage    string
}

// RunWorkItem aggregates the ready-to-dispatch exploit, target, and packaged artifact bytes.
type RunWorkItem struct {
	RunID     string
	Exploit   Exploit
	Target    Target
	Artifact  []byte
	FlagRegex string
	FlagFile  string
	Timeout   time.Duration
}

// RunStatus enumerates lifecycle states for run execution.
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusTimeout   RunStatus = "timeout"
)

// Flag holds metadata about a captured flag awaiting submission.
type Flag struct {
	ID              string
	Value           string
	ChallengeID     string
	TargetID        string
	RunID           string
	Status          FlagStatus
	SubmissionCount int
	LastSubmittedAt time.Time
	CreatedAt       time.Time
}

type FlagStatus string

const (
	FlagStatusNew        FlagStatus = "new"
	FlagStatusSubmitting FlagStatus = "submitting"
	FlagStatusAccepted   FlagStatus = "accepted"
	FlagStatusRejected   FlagStatus = "rejected"
	FlagStatusRetry      FlagStatus = "retry"
	FlagStatusDropped    FlagStatus = "dropped"
)

// SubmissionAttempt tracks outbound flag submission requests.
type SubmissionAttempt struct {
	ID          string
	FlagID      string
	SubmitterID string
	StatusCode  int
	Error       string
	SubmittedAt time.Time
	NextRetryAt time.Time
	Payload     map[string]any
}

// LogEntry represents a structured log line stored for user inspection.
type LogEntry struct {
	ID        string
	Timestamp time.Time
	Level     string
	Source    string
	Message   string
	RunID     string
	FlagID    string
	Fields    map[string]any
}

// LogFilter captures query parameters for log retrieval.
type LogFilter struct {
	Level  string
	Source string
	RunID  string
	FlagID string
	Limit  int
}
