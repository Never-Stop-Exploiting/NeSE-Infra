package storage

import (
	"context"
	"time"

	"awdinfra/internal/domain"
)

// Repository encapsulates persistence logic for structured data.
type Repository interface {
	Challenges(ctx context.Context) ([]domain.Challenge, error)
	GetChallenge(ctx context.Context, id string) (domain.Challenge, error)
	CreateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error)
	UpdateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error)
	DeleteChallenge(ctx context.Context, id string) error
	CreateRun(ctx context.Context, run *domain.Run) error
	UpdateRun(ctx context.Context, run *domain.Run) error
	RunnableWork(ctx context.Context, limit int) ([]domain.RunWorkItem, error)
	UpdateFlag(ctx context.Context, flag *domain.Flag) error
	InsertFlag(ctx context.Context, flag *domain.Flag) error
	LogRunArtifact(ctx context.Context, runID string, uri string) error
	ListTargets(ctx context.Context) ([]domain.Target, error)
	CreateTarget(ctx context.Context, target domain.Target) (domain.Target, error)
	BulkInsertTargets(ctx context.Context, targets []domain.Target) error
	ListExploits(ctx context.Context) ([]domain.Exploit, error)
	CreateExploit(ctx context.Context, exploit domain.Exploit) (domain.Exploit, error)
	SetExploitEnabled(ctx context.Context, exploitID string, enabled bool) error
	SetExploitTimeout(ctx context.Context, exploitID string, timeout time.Duration) error
	AddExploitRevision(ctx context.Context, revision domain.ExploitRevision) error
	SaveExploitArtifact(ctx context.Context, revisionID string, data []byte) error
	LatestExploitArtifact(ctx context.Context, exploitID string) ([]byte, error)
	ListFlags(ctx context.Context) ([]domain.Flag, error)
	GetFlag(ctx context.Context, id string) (domain.Flag, error)
	GetFlagLogs(ctx context.Context, id string) ([]string, error)
	ListFlagAttempts(ctx context.Context, flagID string) ([]domain.SubmissionAttempt, error)
	AddFlagAttempt(ctx context.Context, flagID string, attempt domain.SubmissionAttempt) error
	ListRuns(ctx context.Context) ([]domain.Run, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	ListLogs(ctx context.Context, filter domain.LogFilter) ([]domain.LogEntry, error)
	AppendLog(ctx context.Context, entry domain.LogEntry) error
	ListRunners(ctx context.Context) ([]domain.Runner, error)
	UpsertRunner(ctx context.Context, runner domain.Runner) error
}
