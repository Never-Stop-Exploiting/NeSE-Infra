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
	RunScript      string
	Metadata       map[string]any
}

// ExploitRevision references immutable build artifacts for reproducible runs.
type ExploitRevision struct {
	ID           string
	ExploitID    string
	Digest       string
	StorageURI   string
	ArtifactMD5  string
	ArtifactName string
	Command      []string
	Env          map[string]string
	CreatedAt    time.Time
}

// Runner represents an execution worker registered to the control plane.
type Runner struct {
	ID            string
	Type          string
	Hostname      string
	LastIP        string
	Tags          []string
	ActiveJobs    int
	Labels        map[string]string
	Config        map[string]string
	MaxParallel   int
	LastHeartbeat time.Time
	Status        string
	RegisteredAt  time.Time
}

// Submitter represents a flag submitter worker reporting to the control plane.
type Submitter struct {
	ID               string
	Type             string
	Hostname         string
	Status           string
	Concurrency      int
	InFlight         int
	Pending          int
	RecentThroughput float64
	AverageLatencyMS float64
	Pressure         float64
	Labels           map[string]string
	Config           map[string]string
	LastHeartbeat    time.Time
	RegisteredAt     time.Time
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

// RunFilter captures query parameters for run listing.
type RunFilter struct {
	Status      string
	ExploitID   string
	TargetID    string
	ExploitName string
	TargetName  string
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
	RunStatusCanceled  RunStatus = "canceled"
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
	FlagStatusFailed     FlagStatus = "failed"
	// FlagStatusRejected is kept for backward compatibility; maps to failed.
	FlagStatusRejected   FlagStatus = FlagStatusFailed
	FlagStatusDuplicated FlagStatus = "duplicated"
	FlagStatusRetry      FlagStatus = "retry"
	FlagStatusDropped    FlagStatus = "dropped"
)

// DashboardStats aggregates counts for the overview dashboard.
type DashboardStats struct {
	ExploitCount    int
	TargetCount     int
	PendingFlags    int
	RunningRuns     int
	FlagStatusCount map[FlagStatus]int
}

// OverviewMatrixStatus enumerates cell states in the overview heatmap.
type OverviewMatrixStatus string

const (
	OverviewMatrixStatusSuccess OverviewMatrixStatus = "success"
	OverviewMatrixStatusFailed  OverviewMatrixStatus = "failed"
	OverviewMatrixStatusNone    OverviewMatrixStatus = "none"
)

// OverviewMatrixChallenge holds metadata for the matrix columns.
type OverviewMatrixChallenge struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OverviewMatrixTeam tracks the row label (team).
type OverviewMatrixTeam struct {
	Name string `json:"name"`
}

// OverviewMatrixCell summarises exploit attempts for a target/challenge pair.
type OverviewMatrixCell struct {
	ChallengeID     string               `json:"challengeId"`
	TeamName        string               `json:"teamName"`
	TargetID        string               `json:"targetId,omitempty"`
	Status          OverviewMatrixStatus `json:"status"`
	SuccessExploits []string             `json:"successExploits,omitempty"`
	FailedExploits  []string             `json:"failedExploits,omitempty"`
}

// OverviewMatrix aggregates state for the front-end heatmap.
type OverviewMatrix struct {
	Start        time.Time                 `json:"start"`
	End          time.Time                 `json:"end"`
	RangeMinutes int                       `json:"rangeMinutes"`
	Challenges   []OverviewMatrixChallenge `json:"challenges"`
	Teams        []OverviewMatrixTeam      `json:"teams"`
	Cells        []OverviewMatrixCell      `json:"cells"`
}

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

// SchedulerStrategy enumerates supported scheduling policies.
type SchedulerStrategy string

const (
	SchedulerStrategyInterval   SchedulerStrategy = "interval"
	SchedulerStrategyContinuous SchedulerStrategy = "continuous"
)

// SchedulerSettings capture runtime tunables for the exploit scheduler.
type SchedulerSettings struct {
	Strategy         SchedulerStrategy
	RoundInterval    time.Duration
	MaxDispatchBatch int
	ContinuousDelay  time.Duration
	UpdatedAt        time.Time
	Enabled          bool
}

// TargetExploitOverride records per-target enablement for specific exploits.
type TargetExploitOverride struct {
	TargetID  string
	ExploitID string
	Enabled   bool
	UpdatedAt time.Time
}

// LogFilter captures query parameters for log retrieval.
type LogFilter struct {
	Level  string
	Source string
	RunID  string
	FlagID string
	Limit  int
}
