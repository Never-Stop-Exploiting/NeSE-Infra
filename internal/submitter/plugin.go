package submitter

import "context"

// Plugin represents a pluggable flag submission strategy.
type Plugin interface {
	ID() string
	Submit(ctx context.Context, request SubmissionRequest) SubmissionResult
}

// SubmissionRequest captures data needed by plugins.
type SubmissionRequest struct {
	FlagID      string
	FlagValue   string
	ChallengeID string
	TargetID    string
	Metadata    map[string]string
}

// SubmissionStatus captures the high-level outcome of a submission attempt.
type SubmissionStatus string

const (
	SubmissionStatusAccepted   SubmissionStatus = "accepted"
	SubmissionStatusFailed     SubmissionStatus = "failed"
	SubmissionStatusDuplicated SubmissionStatus = "duplicated"
	SubmissionStatusUnknown    SubmissionStatus = "unknown"
)

// SubmissionResult captures plugin outcome.
type SubmissionResult struct {
	Status     SubmissionStatus
	StatusCode int
	Message    string
	RetryAfter *int64 // seconds until retry; nil means no retry
	Err        error
}
