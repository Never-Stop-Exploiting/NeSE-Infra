package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"awdinfra/internal/domain"
	"awdinfra/internal/storage"
)

// Ensure Repository implements storage.Repository at compile time.
var _ storage.Repository = (*Repository)(nil)

// Repository offers a development-grade in-memory store.
type Repository struct {
	mu               sync.RWMutex
	challenges       map[string]domain.Challenge
	runs             map[string]domain.Run
	flags            map[string]domain.Flag
	targets          map[string]domain.Target
	exploits         map[string]domain.Exploit
	exploitRevs      map[string][]domain.ExploitRevision
	flagLogs         map[string][]string
	flagAttempts     map[string][]domain.SubmissionAttempt
	exploitArtifacts map[string][]byte
	logs             []domain.LogEntry
	runners          map[string]domain.Runner
}

// New creates an empty repository.
func New() *Repository {
	repo := &Repository{
		challenges:       make(map[string]domain.Challenge),
		runs:             make(map[string]domain.Run),
		flags:            make(map[string]domain.Flag),
		targets:          make(map[string]domain.Target),
		exploits:         make(map[string]domain.Exploit),
		exploitRevs:      make(map[string][]domain.ExploitRevision),
		flagLogs:         make(map[string][]string),
		flagAttempts:     make(map[string][]domain.SubmissionAttempt),
		exploitArtifacts: make(map[string][]byte),
		runners:          make(map[string]domain.Runner),
	}
	repo.seed()
	return repo
}

func (r *Repository) CreateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if challenge.ID == "" {
		challenge.ID = newID("challenge")
	}
	now := time.Now().UTC()
	challenge.CreatedAt = now
	challenge.UpdatedAt = now
	r.challenges[challenge.ID] = challenge
	return challenge, ctx.Err()
}

func (r *Repository) UpdateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.challenges[challenge.ID]
	if !ok {
		return domain.Challenge{}, errors.New("challenge not found")
	}
	if challenge.Name != "" {
		existing.Name = challenge.Name
	}
	if challenge.Category != "" {
		existing.Category = challenge.Category
	}
	if challenge.ServiceType != "" {
		existing.ServiceType = challenge.ServiceType
	}
	if challenge.Description != "" {
		existing.Description = challenge.Description
	}
	if challenge.Metadata != nil {
		existing.Metadata = challenge.Metadata
	}
	existing.UpdatedAt = time.Now().UTC()
	r.challenges[existing.ID] = existing
	return existing, ctx.Err()
}

func (r *Repository) DeleteChallenge(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.challenges[id]; !ok {
		return errors.New("challenge not found")
	}
	delete(r.challenges, id)
	for tid, target := range r.targets {
		if target.ChallengeID == id {
			delete(r.targets, tid)
		}
	}
	for eid, exploit := range r.exploits {
		if exploit.ChallengeID == id {
			delete(r.exploits, eid)
			delete(r.exploitRevs, eid)
		}
	}
	return ctx.Err()
}

func (r *Repository) Challenges(ctx context.Context) ([]domain.Challenge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Challenge, 0, len(r.challenges))
	for _, challenge := range r.challenges {
		items = append(items, challenge)
	}
	return items, ctx.Err()
}

func (r *Repository) GetChallenge(ctx context.Context, id string) (domain.Challenge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	challenge, ok := r.challenges[id]
	if !ok {
		return domain.Challenge{}, errors.New("challenge not found")
	}
	return challenge, ctx.Err()
}

func (r *Repository) CreateRun(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return errors.New("nil run")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = *run
	return ctx.Err()
}

func (r *Repository) UpdateRun(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return errors.New("nil run")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runs[run.ID]; !exists {
		return errors.New("run not found")
	}
	r.runs[run.ID] = *run
	return ctx.Err()
}

func (r *Repository) RunnableWork(ctx context.Context, limit int) ([]domain.RunWorkItem, error) {
	// Development stub returns nothing.
	return nil, ctx.Err()
}

func (r *Repository) ListTargets(ctx context.Context) ([]domain.Target, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Target, 0, len(r.targets))
	for _, target := range r.targets {
		items = append(items, target)
	}
	return items, ctx.Err()
}

func (r *Repository) CreateTarget(ctx context.Context, target domain.Target) (domain.Target, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if target.ID == "" {
		target.ID = newID("target")
	}
	now := time.Now().UTC()
	target.CreatedAt = now
	target.UpdatedAt = now
	r.targets[target.ID] = target
	return target, ctx.Err()
}

func (r *Repository) BulkInsertTargets(ctx context.Context, targets []domain.Target) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for i := range targets {
		target := targets[i]
		if target.ID == "" {
			target.ID = newID("target")
		}
		target.CreatedAt = now
		target.UpdatedAt = now
		r.targets[target.ID] = target
	}
	return ctx.Err()
}

func (r *Repository) ListExploits(ctx context.Context) ([]domain.Exploit, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Exploit, 0, len(r.exploits))
	for _, exploit := range r.exploits {
		items = append(items, exploit)
	}
	return items, ctx.Err()
}

func (r *Repository) CreateExploit(ctx context.Context, exploit domain.Exploit) (domain.Exploit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exploit.ID == "" {
		exploit.ID = newID("exploit")
	}
	now := time.Now().UTC()
	exploit.CreatedAt = now
	exploit.UpdatedAt = now
	if exploit.Metadata == nil {
		exploit.Metadata = make(map[string]any)
	}
	r.exploits[exploit.ID] = exploit
	return exploit, ctx.Err()
}

func (r *Repository) SetExploitEnabled(ctx context.Context, exploitID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	exploit, ok := r.exploits[exploitID]
	if !ok {
		return errors.New("exploit not found")
	}
	exploit.Enabled = enabled
	exploit.UpdatedAt = time.Now().UTC()
	r.exploits[exploitID] = exploit
	return ctx.Err()
}

func (r *Repository) SetExploitTimeout(ctx context.Context, exploitID string, timeout time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	exploit, ok := r.exploits[exploitID]
	if !ok {
		return errors.New("exploit not found")
	}
	if exploit.Metadata == nil {
		exploit.Metadata = make(map[string]any)
	}
	exploit.Metadata["timeout_seconds"] = int(timeout.Seconds())
	exploit.UpdatedAt = time.Now().UTC()
	r.exploits[exploitID] = exploit
	return ctx.Err()
}

func (r *Repository) AddExploitRevision(ctx context.Context, revision domain.ExploitRevision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if revision.ID == "" {
		revision.ID = newID("rev")
	}
	revision.CreatedAt = time.Now().UTC()
	r.exploitRevs[revision.ExploitID] = append(r.exploitRevs[revision.ExploitID], revision)
	return ctx.Err()
}

func (r *Repository) SaveExploitArtifact(ctx context.Context, revisionID string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(data) == 0 {
		return errors.New("artifact empty")
	}
	r.exploitArtifacts[revisionID] = append([]byte(nil), data...)
	return ctx.Err()
}

func (r *Repository) LatestExploitArtifact(ctx context.Context, exploitID string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	revs := r.exploitRevs[exploitID]
	if len(revs) == 0 {
		return nil, errors.New("no revision")
	}
	rev := revs[len(revs)-1]
	artifact, ok := r.exploitArtifacts[rev.ID]
	if !ok {
		return nil, errors.New("artifact not found")
	}
	return append([]byte(nil), artifact...), ctx.Err()
}

func (r *Repository) UpdateFlag(ctx context.Context, flag *domain.Flag) error {
	if flag == nil {
		return errors.New("nil flag")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.flags[flag.ID]; !exists {
		return errors.New("flag not found")
	}
	r.flags[flag.ID] = *flag
	return ctx.Err()
}

func (r *Repository) InsertFlag(ctx context.Context, flag *domain.Flag) error {
	if flag == nil {
		return errors.New("nil flag")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	flag.CreatedAt = time.Now().UTC()
	r.flags[flag.ID] = *flag
	return ctx.Err()
}

func (r *Repository) ListFlags(ctx context.Context) ([]domain.Flag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Flag, 0, len(r.flags))
	for _, flag := range r.flags {
		items = append(items, flag)
	}
	return items, ctx.Err()
}

func (r *Repository) GetFlag(ctx context.Context, id string) (domain.Flag, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	flag, ok := r.flags[id]
	if !ok {
		return domain.Flag{}, errors.New("flag not found")
	}
	return flag, ctx.Err()
}

func (r *Repository) GetFlagLogs(ctx context.Context, id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	logs, ok := r.flagLogs[id]
	if !ok {
		return []string{}, ctx.Err()
	}
	return append([]string(nil), logs...), ctx.Err()
}

func (r *Repository) ListFlagAttempts(ctx context.Context, flagID string) ([]domain.SubmissionAttempt, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	attempts := r.flagAttempts[flagID]
	return append([]domain.SubmissionAttempt(nil), attempts...), ctx.Err()
}

func (r *Repository) AddFlagAttempt(ctx context.Context, flagID string, attempt domain.SubmissionAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempt.ID = newID("attempt")
	if attempt.SubmittedAt.IsZero() {
		attempt.SubmittedAt = time.Now().UTC()
	}
	r.flagAttempts[flagID] = append(r.flagAttempts[flagID], attempt)
	flag, ok := r.flags[flagID]
	if ok {
		flag.SubmissionCount++
		r.flags[flagID] = flag
	}
	logLine := fmt.Sprintf("%s status=%d %s", attempt.SubmittedAt.Format(time.RFC3339), attempt.StatusCode, attempt.Error)
	r.flagLogs[flagID] = append(r.flagLogs[flagID], logLine)
	return ctx.Err()
}

func (r *Repository) ListRuns(ctx context.Context) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		items = append(items, run)
	}
	return items, ctx.Err()
}

func (r *Repository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[id]
	if !ok {
		return domain.Run{}, errors.New("run not found")
	}
	return run, ctx.Err()
}

func (r *Repository) ListLogs(ctx context.Context, filter domain.LogFilter) ([]domain.LogEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var items []domain.LogEntry
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	for i := len(r.logs) - 1; i >= 0; i-- {
		entry := r.logs[i]
		if filter.Level != "" && entry.Level != filter.Level {
			continue
		}
		if filter.Source != "" && entry.Source != filter.Source {
			continue
		}
		if filter.RunID != "" && entry.RunID != filter.RunID {
			continue
		}
		if filter.FlagID != "" && entry.FlagID != filter.FlagID {
			continue
		}
		items = append(items, entry)
		if len(items) >= limit {
			break
		}
	}
	// Reverse to chronological order ascending
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, ctx.Err()
}

func (r *Repository) AppendLog(ctx context.Context, entry domain.LogEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.ID == "" {
		entry.ID = newID("log")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	r.logs = append(r.logs, entry)
	return ctx.Err()
}

func (r *Repository) ListRunners(ctx context.Context) ([]domain.Runner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Runner, 0, len(r.runners))
	for _, runner := range r.runners {
		items = append(items, runner)
	}
	return items, ctx.Err()
}

func (r *Repository) UpsertRunner(ctx context.Context, runner domain.Runner) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if runner.ID == "" {
		runner.ID = newID("runner")
	}
	if runner.RegisteredAt.IsZero() {
		runner.RegisteredAt = time.Now().UTC()
	}
	_runner := r.runners[runner.ID]
	if runner.LastHeartbeat.IsZero() {
		runner.LastHeartbeat = _runner.LastHeartbeat
	}
	r.runners[runner.ID] = runner
	return ctx.Err()
}

func (r *Repository) LogRunArtifact(ctx context.Context, runID string, uri string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, exists := r.runs[runID]
	if !exists {
		return errors.New("run not found")
	}
	run.LogURI = uri
	r.runs[runID] = run
	return ctx.Err()
}

func (r *Repository) seed() {
	now := time.Now().UTC()
	challengeID := newID("challenge")
	r.challenges[challengeID] = domain.Challenge{
		ID:          challengeID,
		Name:        "Sample Challenge",
		Category:    "web",
		ServiceType: "http",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	t1, _ := r.CreateTarget(context.Background(), domain.Target{
		ChallengeID:      challengeID,
		TeamName:         "Team Alpha",
		IP:               "10.10.10.11",
		Port:             8080,
		Status:           "active",
		ConcurrencyLimit: 2,
	})
	_, _ = r.CreateTarget(context.Background(), domain.Target{
		ChallengeID:      challengeID,
		TeamName:         "Team Beta",
		IP:               "10.10.10.12",
		Port:             8080,
		Status:           "active",
		ConcurrencyLimit: 2,
	})

	exploit, _ := r.CreateExploit(context.Background(), domain.Exploit{
		ChallengeID:    challengeID,
		Name:           "Sample exploit",
		Description:    "Demo exploit for dashboard",
		Enabled:        true,
		RunnerTags:     []string{"python", "pwntools"},
		ScheduleWeight: 1,
		Metadata: map[string]any{
			"timeout_seconds": 120,
			"entrypoint":      "python exp.py",
		},
	})

	flagID := newID("flag")
	r.flags[flagID] = domain.Flag{
		ID:              flagID,
		Value:           "FLAG{DEMO}",
		ChallengeID:     challengeID,
		TargetID:        t1.ID,
		Status:          domain.FlagStatusRetry,
		SubmissionCount: 1,
		CreatedAt:       now.Add(-time.Minute),
	}
	r.flagLogs[flagID] = []string{
		repr("submitted", now.Add(-time.Minute*3), 202, "Accepted"),
		repr("submitted", now.Add(-time.Minute*2), 429, "Rate limited"),
	}
	r.flagAttempts[flagID] = []domain.SubmissionAttempt{
		{ID: newID("attempt"), FlagID: flagID, SubmitterID: "auto", StatusCode: 202, SubmittedAt: now.Add(-time.Minute * 3)},
		{ID: newID("attempt"), FlagID: flagID, SubmitterID: "auto", StatusCode: 429, Error: "Rate limited", SubmittedAt: now.Add(-time.Minute * 2), NextRetryAt: now.Add(-time.Minute)},
	}

	runner := domain.Runner{
		ID:            newID("runner"),
		Type:          "python",
		Hostname:      "runner-1",
		MaxParallel:   3,
		Status:        "online",
		LastHeartbeat: now.Add(-time.Second * 10),
		RegisteredAt:  now.Add(-time.Hour),
	}
	r.runners[runner.ID] = runner

	runID := newID("run")
	r.runs[runID] = domain.Run{
		ID:         runID,
		ExploitID:  exploit.ID,
		TargetID:   t1.ID,
		RunnerID:   runner.ID,
		Status:     domain.RunStatusSucceeded,
		QueuedAt:   now.Add(-time.Minute * 5),
		StartedAt:  now.Add(-time.Minute * 4),
		FinishedAt: now.Add(-time.Minute * 3),
		LogURI:     "",
		FlagID:     flagID,
	}

	r.logs = append(r.logs,
		domain.LogEntry{ID: newID("log"), Timestamp: now.Add(-time.Minute * 2), Level: "info", Source: "scheduler", Message: "Dispatched run", RunID: runID},
		domain.LogEntry{ID: newID("log"), Timestamp: now.Add(-time.Minute), Level: "info", Source: "runner", Message: "Run completed", RunID: runID},
	)
}

func newID(prefix string) string {
	id := atomic.AddUint64(&idCounter, 1)
	return fmt.Sprintf("%s-%d", prefix, id)
}

func repr(event string, ts time.Time, code int, msg string) string {
	return fmt.Sprintf("%s [%s] %d %s", ts.Format(time.RFC3339), event, code, msg)
}

var idCounter uint64
