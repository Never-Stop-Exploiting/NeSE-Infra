package storage

import (
	"context"
	"errors"
	"time"

	"awdinfra/internal/domain"
)

var ErrExploitNotFound = errors.New("exploit not found")

// ExploitFile captures metadata about a file stored for an exploit.
type ExploitFile struct {
	Name    string
	Size    int64
	MD5     string
	ModTime time.Time
}

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
	GetSchedulerSettings(ctx context.Context) (domain.SchedulerSettings, error)
	SaveSchedulerSettings(ctx context.Context, settings domain.SchedulerSettings) (domain.SchedulerSettings, error)
	UpdateFlag(ctx context.Context, flag *domain.Flag) error
	InsertFlag(ctx context.Context, flag *domain.Flag) error
	LogRunArtifact(ctx context.Context, runID string, uri string) error
	ListTargets(ctx context.Context) ([]domain.Target, error)
	GetTarget(ctx context.Context, id string) (domain.Target, error)
	CreateTarget(ctx context.Context, target domain.Target) (domain.Target, error)
	UpdateTarget(ctx context.Context, target domain.Target) (domain.Target, error)
	DeleteTarget(ctx context.Context, id string) error
	BulkInsertTargets(ctx context.Context, targets []domain.Target) error
	ListExploits(ctx context.Context) ([]domain.Exploit, error)
	CreateExploit(ctx context.Context, exploit domain.Exploit) (domain.Exploit, error)
	SetExploitEnabled(ctx context.Context, exploitID string, enabled bool) error
	SetExploitTimeout(ctx context.Context, exploitID string, timeout time.Duration) error
	UpdateExploitMetadata(ctx context.Context, exploitID string, updates map[string]any) error
	GetExploitRunScript(ctx context.Context, exploitID string) (string, error)
	SaveExploitRunScript(ctx context.Context, exploitID string, script string) error
	DeleteExploit(ctx context.Context, exploitID string) error
	ListExploitFiles(ctx context.Context, exploitID string) ([]ExploitFile, error)
	GetExploitFile(ctx context.Context, exploitID string, name string) ([]byte, error)
	SaveExploitFile(ctx context.Context, exploitID string, name string, data []byte) error
	DeleteExploitFile(ctx context.Context, exploitID string, name string) error
	LatestExploitArtifact(ctx context.Context, exploitID string) ([]byte, error)
	ListTargetExploitOverrides(ctx context.Context) ([]domain.TargetExploitOverride, error)
	UpsertTargetExploitOverride(ctx context.Context, override domain.TargetExploitOverride) error
	DeleteTargetExploitOverride(ctx context.Context, targetID string, exploitID string) error
	ListFlags(ctx context.Context) ([]domain.Flag, error)
	GetFlag(ctx context.Context, id string) (domain.Flag, error)
	GetFlagLogs(ctx context.Context, id string) ([]string, error)
	ListFlagAttempts(ctx context.Context, flagID string) ([]domain.SubmissionAttempt, error)
	AddFlagAttempt(ctx context.Context, flagID string, attempt domain.SubmissionAttempt) error
	ListRuns(ctx context.Context, limit int, cursor string) ([]domain.Run, string, error)
	GetRun(ctx context.Context, id string) (domain.Run, error)
	ListLogs(ctx context.Context, filter domain.LogFilter) ([]domain.LogEntry, error)
	AppendLog(ctx context.Context, entry domain.LogEntry) error
	ListRunners(ctx context.Context) ([]domain.Runner, error)
	UpsertRunner(ctx context.Context, runner domain.Runner) error
	GetRunner(ctx context.Context, id string) (domain.Runner, error)
	DeleteRunner(ctx context.Context, id string) error
}
