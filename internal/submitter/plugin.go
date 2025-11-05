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

// SubmissionResult captures plugin outcome.
type SubmissionResult struct {
	StatusCode int
	Message    string
	RetryAfter *int64 // seconds until retry; nil means no retry
	Err        error
}
