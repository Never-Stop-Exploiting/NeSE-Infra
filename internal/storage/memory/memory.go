package memory

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"awdinfra/internal/domain"
	"awdinfra/internal/jobs"
	"awdinfra/internal/storage"
)

const (
	runScriptName       = "run.sh"
	submitterScriptName = "submit.sh"
)

func defaultSubmitScript() string {
	return "#!/bin/sh\nset -euo pipefail\npython submit.py \"$1\"\n"
}

type memoryFile struct {
	Data    []byte
	ModTime time.Time
}

// Ensure Repository implements storage.Repository at compile time.
var _ storage.Repository = (*Repository)(nil)

// Repository offers a development-grade in-memory store.
type Repository struct {
	mu             sync.RWMutex
	challenges     map[string]domain.Challenge
	runs           map[string]domain.Run
	flags          map[string]domain.Flag
	targets        map[string]domain.Target
	exploits       map[string]domain.Exploit
	flagLogs       map[string][]string
	flagAttempts   map[string][]domain.SubmissionAttempt
	exploitFiles   map[string]map[string]memoryFile
	submitterFiles map[string]memoryFile
	logs           []domain.LogEntry
	runners        map[string]domain.Runner
	submitters     map[string]domain.Submitter
	scheduler      domain.SchedulerSettings
	overrides      map[string]map[string]domain.TargetExploitOverride
}

// New creates an empty repository.
func New() *Repository {
	repo := &Repository{
		challenges:     make(map[string]domain.Challenge),
		runs:           make(map[string]domain.Run),
		flags:          make(map[string]domain.Flag),
		targets:        make(map[string]domain.Target),
		exploits:       make(map[string]domain.Exploit),
		flagLogs:       make(map[string][]string),
		flagAttempts:   make(map[string][]domain.SubmissionAttempt),
		exploitFiles:   make(map[string]map[string]memoryFile),
		submitterFiles: make(map[string]memoryFile),
		runners:        make(map[string]domain.Runner),
		submitters:     make(map[string]domain.Submitter),
		scheduler: domain.SchedulerSettings{
			Strategy:         domain.SchedulerStrategyInterval,
			RoundInterval:    time.Minute * 5,
			MaxDispatchBatch: 50,
			ContinuousDelay:  0,
			UpdatedAt:        time.Now().UTC(),
			Enabled:          false,
		},
		overrides: make(map[string]map[string]domain.TargetExploitOverride),
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
	if strings.TrimSpace(challenge.FlagRegex) != "" {
		existing.FlagRegex = challenge.FlagRegex
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
			delete(r.exploitFiles, eid)
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

func (r *Repository) RunnableWork(ctx context.Context, limit int, settings domain.SchedulerSettings) ([]domain.RunWorkItem, error) {
	if limit <= 0 {
		limit = 1
	}
	strategy := settings.Strategy
	if strategy == "" {
		strategy = domain.SchedulerStrategyInterval
	}

	r.mu.RLock()
	targetCopies := make([]domain.Target, 0, len(r.targets))
	for _, target := range r.targets {
		targetCopies = append(targetCopies, target)
	}
	exploitCopies := make([]domain.Exploit, 0, len(r.exploits))
	for _, exploit := range r.exploits {
		exploitCopies = append(exploitCopies, exploit)
	}
	challengeCopies := make(map[string]domain.Challenge, len(r.challenges))
	for id, challenge := range r.challenges {
		challengeCopies[id] = challenge
	}
	overrideCopies := make(map[string]map[string]domain.TargetExploitOverride, len(r.overrides))
	for targetID, overrides := range r.overrides {
		copyMap := make(map[string]domain.TargetExploitOverride, len(overrides))
		for exploitID, override := range overrides {
			copyMap[exploitID] = override
		}
		overrideCopies[targetID] = copyMap
	}
	runCopies := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		runCopies = append(runCopies, run)
	}
	r.mu.RUnlock()

	activeCounts := make(map[string]int)
	var pairActive map[string]int
	var lastFinished map[string]time.Time
	continuousDelay := settings.ContinuousDelay
	if continuousDelay < 0 {
		continuousDelay = 0
	}
	if strategy == domain.SchedulerStrategyContinuous {
		pairActive = make(map[string]int)
		lastFinished = make(map[string]time.Time)
	}
	for _, run := range runCopies {
		if run.TargetID == "" {
			continue
		}
		if strategy == domain.SchedulerStrategyContinuous && run.ExploitID != "" {
			key := schedulerPairKey(run.TargetID, run.ExploitID)
			switch run.Status {
			case domain.RunStatusPending, domain.RunStatusRunning:
				pairActive[key]++
			default:
				finish := run.FinishedAt
				if finish.IsZero() {
					finish = run.StartedAt
				}
				if finish.IsZero() {
					finish = run.QueuedAt
				}
				if !finish.IsZero() && finish.After(lastFinished[key]) {
					lastFinished[key] = finish
				}
			}
		}
		if run.Status == domain.RunStatusPending || run.Status == domain.RunStatusRunning {
			activeCounts[run.TargetID]++
		}
	}

	exploitsByChallenge := make(map[string][]domain.Exploit)
	for _, exploit := range exploitCopies {
		exploitsByChallenge[exploit.ChallengeID] = append(exploitsByChallenge[exploit.ChallengeID], exploit)
	}

	work := make([]domain.RunWorkItem, 0, limit)
	now := time.Now().UTC()

	for _, target := range targetCopies {
		if len(work) >= limit {
			break
		}
		status := strings.ToLower(strings.TrimSpace(target.Status))
		if status != "" && status != "active" {
			continue
		}
		concurrency := target.ConcurrencyLimit
		if concurrency <= 0 {
			concurrency = 1
		}
		if activeCounts[target.ID] >= concurrency {
			continue
		}

		candidates := exploitsByChallenge[target.ChallengeID]
		if len(candidates) == 0 {
			continue
		}

		for _, exploit := range candidates {
			if len(work) >= limit {
				break
			}
			enabled := exploit.Enabled
			if overrides := overrideCopies[target.ID]; overrides != nil {
				if override, ok := overrides[exploit.ID]; ok {
					enabled = override.Enabled
				}
			}
			if !enabled {
				continue
			}

			var pairKey string
			if strategy == domain.SchedulerStrategyContinuous {
				pairKey = schedulerPairKey(target.ID, exploit.ID)
				if pairActive[pairKey] > 0 {
					continue
				}
				last := lastFinished[pairKey]
				if !last.IsZero() && now.Sub(last) < continuousDelay {
					continue
				}
			}

			artifact, err := r.LatestExploitArtifact(ctx, exploit.ID)
			if err != nil || len(artifact) == 0 {
				continue
			}

			run := domain.Run{
				ExploitID: exploit.ID,
				TargetID:  target.ID,
				Status:    domain.RunStatusPending,
				QueuedAt:  now,
			}
			if err := r.CreateRun(ctx, &run); err != nil {
				return nil, err
			}

			flagRegex, flagFile, timeout := jobs.HintsFromMetadata(exploit.Metadata)
			challenge := challengeCopies[target.ChallengeID]
			flagRegex = domain.ResolveFlagRegex(flagRegex, challenge.FlagRegex)

			work = append(work, domain.RunWorkItem{
				RunID:     run.ID,
				Exploit:   exploit,
				Target:    target,
				Artifact:  artifact,
				FlagRegex: flagRegex,
				FlagFile:  flagFile,
				Timeout:   timeout,
			})
			activeCounts[target.ID]++
			if strategy == domain.SchedulerStrategyContinuous && pairKey != "" {
				pairActive[pairKey]++
			}
			if activeCounts[target.ID] >= concurrency {
				break
			}
		}
	}

	if ctx.Err() != nil {
		return work, ctx.Err()
	}
	return work, nil
}

func schedulerPairKey(targetID, exploitID string) string {
	return targetID + "|" + exploitID
}

func (r *Repository) GetSchedulerSettings(ctx context.Context) (domain.SchedulerSettings, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	settings := r.scheduler
	if settings.Strategy == "" {
		settings.Strategy = domain.SchedulerStrategyInterval
	}
	if settings.RoundInterval <= 0 {
		settings.RoundInterval = time.Minute * 5
	}
	if settings.MaxDispatchBatch <= 0 {
		settings.MaxDispatchBatch = 50
	}
	if settings.ContinuousDelay < 0 {
		settings.ContinuousDelay = 0
	}
	return settings, ctx.Err()
}

func (r *Repository) SaveSchedulerSettings(ctx context.Context, settings domain.SchedulerSettings) (domain.SchedulerSettings, error) {
	if settings.RoundInterval <= 0 {
		return domain.SchedulerSettings{}, errors.New("round interval must be positive")
	}
	if settings.MaxDispatchBatch <= 0 {
		return domain.SchedulerSettings{}, errors.New("max dispatch batch must be positive")
	}
	strategy := settings.Strategy
	if strategy == "" {
		strategy = domain.SchedulerStrategyInterval
	}
	switch strategy {
	case domain.SchedulerStrategyInterval, domain.SchedulerStrategyContinuous:
	default:
		return domain.SchedulerSettings{}, errors.New("invalid scheduler strategy")
	}
	if settings.ContinuousDelay < 0 {
		return domain.SchedulerSettings{}, errors.New("continuous delay must be non-negative")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	settings.UpdatedAt = time.Now().UTC()
	settings.Strategy = strategy
	r.scheduler = settings
	return settings, ctx.Err()
}

func (r *Repository) ListTargetExploitOverrides(ctx context.Context) ([]domain.TargetExploitOverride, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.TargetExploitOverride, 0)
	for _, byExploit := range r.overrides {
		for _, override := range byExploit {
			result = append(result, override)
		}
	}
	return result, ctx.Err()
}

func (r *Repository) UpsertTargetExploitOverride(ctx context.Context, override domain.TargetExploitOverride) error {
	if strings.TrimSpace(override.TargetID) == "" || strings.TrimSpace(override.ExploitID) == "" {
		return errors.New("target and exploit ids required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.overrides[override.TargetID]; !ok {
		r.overrides[override.TargetID] = make(map[string]domain.TargetExploitOverride)
	}
	override.UpdatedAt = time.Now().UTC()
	r.overrides[override.TargetID][override.ExploitID] = override
	return ctx.Err()
}

func (r *Repository) DeleteTargetExploitOverride(ctx context.Context, targetID string, exploitID string) error {
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(exploitID) == "" {
		return errors.New("target and exploit ids required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if byExploit, ok := r.overrides[targetID]; ok {
		delete(byExploit, exploitID)
		if len(byExploit) == 0 {
			delete(r.overrides, targetID)
		}
	}
	return ctx.Err()
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

func (r *Repository) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	target, ok := r.targets[id]
	if !ok {
		return domain.Target{}, errors.New("target not found")
	}
	return target, ctx.Err()
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

func (r *Repository) UpdateTarget(ctx context.Context, target domain.Target) (domain.Target, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.targets[target.ID]
	if !ok {
		return domain.Target{}, errors.New("target not found")
	}
	if strings.TrimSpace(target.TeamName) != "" {
		existing.TeamName = target.TeamName
	}
	if strings.TrimSpace(target.IP) != "" {
		existing.IP = target.IP
	}
	if target.Port > 0 {
		existing.Port = target.Port
	}
	if strings.TrimSpace(target.Status) != "" {
		existing.Status = target.Status
	}
	if target.ConcurrencyLimit > 0 {
		existing.ConcurrencyLimit = target.ConcurrencyLimit
	}
	if target.Labels != nil {
		existing.Labels = target.Labels
	}
	if target.ChallengeID != "" {
		existing.ChallengeID = target.ChallengeID
	}
	existing.UpdatedAt = time.Now().UTC()
	r.targets[target.ID] = existing
	return existing, ctx.Err()
}

func (r *Repository) DeleteTarget(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.targets[id]; !ok {
		return errors.New("target not found")
	}
	delete(r.targets, id)
	delete(r.overrides, id)
	return ctx.Err()
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

func (r *Repository) GetExploit(ctx context.Context, id string) (domain.Exploit, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exploit, ok := r.exploits[id]
	if !ok {
		return domain.Exploit{}, storage.ErrExploitNotFound
	}
	return exploit, ctx.Err()
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
	entry := ""
	if exploit.Metadata != nil {
		if val, ok := exploit.Metadata["entrypoint"].(string); ok {
			entry = val
		}
	}
	if _, err := r.ensureRunScriptLocked(exploit.ID, entry); err != nil {
		return domain.Exploit{}, err
	}
	return exploit, ctx.Err()
}

func (r *Repository) SetExploitEnabled(ctx context.Context, exploitID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	exploit, ok := r.exploits[exploitID]
	if !ok {
		return storage.ErrExploitNotFound
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
		return storage.ErrExploitNotFound
	}
	if exploit.Metadata == nil {
		exploit.Metadata = make(map[string]any)
	}
	exploit.Metadata["timeout_seconds"] = int(timeout.Seconds())
	exploit.UpdatedAt = time.Now().UTC()
	r.exploits[exploitID] = exploit
	return ctx.Err()
}

func (r *Repository) DeleteExploit(ctx context.Context, exploitID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return storage.ErrExploitNotFound
	}
	delete(r.exploits, exploitID)
	delete(r.exploitFiles, exploitID)
	return ctx.Err()
}

func (r *Repository) UpdateExploitMetadata(ctx context.Context, exploitID string, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	exploit, ok := r.exploits[exploitID]
	if !ok {
		return storage.ErrExploitNotFound
	}
	if exploit.Metadata == nil {
		exploit.Metadata = make(map[string]any)
	}
	for key, value := range updates {
		if value == nil {
			delete(exploit.Metadata, key)
			continue
		}
		exploit.Metadata[key] = value
	}
	exploit.UpdatedAt = time.Now().UTC()
	r.exploits[exploitID] = exploit
	entry := r.exploitEntrypointLocked(exploitID)
	if _, err := r.ensureRunScriptLocked(exploitID, entry); err != nil {
		return err
	}
	return ctx.Err()
}

func (r *Repository) LatestExploitArtifact(ctx context.Context, exploitID string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return nil, storage.ErrExploitNotFound
	}
	entry := r.exploitEntrypointLocked(exploitID)
	if _, err := r.ensureRunScriptLocked(exploitID, entry); err != nil {
		return nil, err
	}
	files := r.ensureExploitFilesLocked(exploitID)
	archive, err := createZipFromFilesLocked(files)
	if err != nil {
		return nil, err
	}
	return archive, ctx.Err()
}

func (r *Repository) GetExploitRunScript(ctx context.Context, exploitID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return "", storage.ErrExploitNotFound
	}
	entry := r.exploitEntrypointLocked(exploitID)
	script, err := r.ensureRunScriptLocked(exploitID, entry)
	if err != nil {
		return "", err
	}
	return script, ctx.Err()
}

func (r *Repository) SaveExploitRunScript(ctx context.Context, exploitID string, script string) error {
	if strings.TrimSpace(script) == "" {
		return errors.New("run script cannot be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return storage.ErrExploitNotFound
	}
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	files := r.ensureExploitFilesLocked(exploitID)
	files[runScriptName] = memoryFile{
		Data:    []byte(script),
		ModTime: time.Now().UTC(),
	}
	return ctx.Err()
}

func (r *Repository) ListExploitFiles(ctx context.Context, exploitID string) ([]storage.ExploitFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return nil, storage.ErrExploitNotFound
	}
	entry := r.exploitEntrypointLocked(exploitID)
	if _, err := r.ensureRunScriptLocked(exploitID, entry); err != nil {
		return nil, err
	}
	files := r.ensureExploitFilesLocked(exploitID)
	result := make([]storage.ExploitFile, 0, len(files))
	for name, file := range files {
		sum := md5.Sum(file.Data)
		result = append(result, storage.ExploitFile{
			Name:    name,
			Size:    int64(len(file.Data)),
			MD5:     hex.EncodeToString(sum[:]),
			ModTime: file.ModTime,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == runScriptName {
			return true
		}
		if result[j].Name == runScriptName {
			return false
		}
		return result[i].Name < result[j].Name
	})
	return result, ctx.Err()
}

func (r *Repository) GetExploitFile(ctx context.Context, exploitID string, name string) ([]byte, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" {
		return nil, errors.New("file name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return nil, storage.ErrExploitNotFound
	}
	files := r.ensureExploitFilesLocked(exploitID)
	if name == runScriptName {
		entry := r.exploitEntrypointLocked(exploitID)
		script, err := r.ensureRunScriptLocked(exploitID, entry)
		if err != nil {
			return nil, err
		}
		return []byte(script), ctx.Err()
	}
	file, ok := files[name]
	if !ok {
		return nil, errors.New("file not found")
	}
	return cloneBytes(file.Data), ctx.Err()
}

func (r *Repository) SaveExploitFile(ctx context.Context, exploitID string, name string, data []byte) error {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" {
		return errors.New("file name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return storage.ErrExploitNotFound
	}
	if name == runScriptName {
		return r.SaveExploitRunScript(ctx, exploitID, string(data))
	}
	if len(data) == 0 {
		return errors.New("file empty")
	}
	files := r.ensureExploitFilesLocked(exploitID)
	files[name] = memoryFile{
		Data:    cloneBytes(data),
		ModTime: time.Now().UTC(),
	}
	return ctx.Err()
}

func (r *Repository) DeleteExploitFile(ctx context.Context, exploitID string, name string) error {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" {
		return errors.New("file name required")
	}
	if name == runScriptName {
		return errors.New("run.sh cannot be deleted")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.exploits[exploitID]; !ok {
		return storage.ErrExploitNotFound
	}
	files := r.ensureExploitFilesLocked(exploitID)
	if _, ok := files[name]; !ok {
		return errors.New("file not found")
	}
	delete(files, name)
	return ctx.Err()
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

func (r *Repository) DashboardStats(ctx context.Context) (domain.DashboardStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := domain.DashboardStats{
		FlagStatusCount: make(map[domain.FlagStatus]int),
	}
	stats.ExploitCount = len(r.exploits)
	stats.TargetCount = len(r.targets)
	for _, run := range r.runs {
		if run.Status == domain.RunStatusPending || run.Status == domain.RunStatusRunning {
			stats.RunningRuns++
		}
	}
	for _, flag := range r.flags {
		stats.FlagStatusCount[flag.Status]++
		if flag.Status != domain.FlagStatusAccepted {
			stats.PendingFlags++
		}
	}
	return stats, ctx.Err()
}

func (r *Repository) OverviewMatrix(ctx context.Context, start, end time.Time) (domain.OverviewMatrix, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		end = start.Add(time.Minute)
	}
	matrix := domain.OverviewMatrix{
		Start:        start,
		End:          end,
		RangeMinutes: int(end.Sub(start).Minutes()),
	}
	if matrix.RangeMinutes <= 0 {
		matrix.RangeMinutes = 1
	}

	challengeList := make([]domain.Challenge, 0, len(r.challenges))
	for _, challenge := range r.challenges {
		challengeList = append(challengeList, challenge)
	}
	sort.Slice(challengeList, func(i, j int) bool {
		return strings.ToLower(challengeList[i].Name) < strings.ToLower(challengeList[j].Name)
	})
	for _, challenge := range challengeList {
		matrix.Challenges = append(matrix.Challenges, domain.OverviewMatrixChallenge{
			ID:   challenge.ID,
			Name: challenge.Name,
		})
	}

	targetByChallenge := make(map[string]map[string]string)
	teamSet := make(map[string]struct{})
	for id, target := range r.targets {
		teamName := strings.TrimSpace(target.TeamName)
		if teamName == "" {
			continue
		}
		teamSet[teamName] = struct{}{}
		if targetByChallenge[target.ChallengeID] == nil {
			targetByChallenge[target.ChallengeID] = make(map[string]string)
		}
		if _, exists := targetByChallenge[target.ChallengeID][teamName]; !exists {
			targetByChallenge[target.ChallengeID][teamName] = id
		}
	}

	teamNames := make([]string, 0, len(teamSet))
	for name := range teamSet {
		teamNames = append(teamNames, name)
	}
	sort.Strings(teamNames)
	matrix.Teams = make([]domain.OverviewMatrixTeam, len(teamNames))
	for i, name := range teamNames {
		matrix.Teams[i] = domain.OverviewMatrixTeam{Name: name}
	}

	type cellAccumulator struct {
		success map[string]struct{}
		failed  map[string]struct{}
	}
	accum := make(map[string]*cellAccumulator)

	getExploitName := func(exploitID string) string {
		if exploit, ok := r.exploits[exploitID]; ok {
			if strings.TrimSpace(exploit.Name) != "" {
				return exploit.Name
			}
		}
		return exploitID
	}

	for _, run := range r.runs {
		targetID := strings.TrimSpace(run.TargetID)
		if targetID == "" {
			continue
		}
		eventTime := run.FinishedAt
		if eventTime.IsZero() {
			eventTime = run.StartedAt
		}
		if eventTime.IsZero() {
			eventTime = run.QueuedAt
		}

		flag, hasFlag := r.flags[run.FlagID]
		if !hasFlag && run.FlagID == "" {
			for _, candidate := range r.flags {
				if candidate.RunID == run.ID {
					flag = candidate
					hasFlag = true
					break
				}
			}
		}

		flagTime := flag.LastSubmittedAt
		if flagTime.IsZero() {
			flagTime = flag.CreatedAt
		}

		inWindow := (!eventTime.IsZero() && !eventTime.Before(start) && !eventTime.After(end)) ||
			(hasFlag && !flagTime.IsZero() && !flagTime.Before(start) && !flagTime.After(end))
		if !inWindow {
			continue
		}

		exploitName := getExploitName(run.ExploitID)
		acc := accum[targetID]
		if acc == nil {
			acc = &cellAccumulator{
				success: make(map[string]struct{}),
				failed:  make(map[string]struct{}),
			}
			accum[targetID] = acc
		}

		success := run.Status == domain.RunStatusSucceeded &&
			hasFlag &&
			(flag.Status == domain.FlagStatusAccepted || flag.Status == domain.FlagStatusDuplicated)
		if success {
			acc.success[exploitName] = struct{}{}
			continue
		}

		failure := false
		switch run.Status {
		case domain.RunStatusFailed, domain.RunStatusTimeout, domain.RunStatusCanceled:
			failure = true
		}
		if hasFlag &&
			flag.Status != domain.FlagStatusAccepted &&
			flag.Status != domain.FlagStatusDuplicated {
			failure = true
		}
		if run.Status == domain.RunStatusSucceeded && !hasFlag {
			failure = true
		}
		if failure {
			acc.failed[exploitName] = struct{}{}
		}
	}

	toSortedSlice := func(set map[string]struct{}) []string {
		if len(set) == 0 {
			return nil
		}
		items := make([]string, 0, len(set))
		for value := range set {
			items = append(items, value)
		}
		sort.Strings(items)
		return items
	}

	totalCells := len(matrix.Challenges) * len(matrix.Teams)
	matrix.Cells = make([]domain.OverviewMatrixCell, 0, totalCells)
	for _, teamName := range teamNames {
		for _, challenge := range matrix.Challenges {
			targetID := ""
			if byTeam := targetByChallenge[challenge.ID]; byTeam != nil {
				targetID = byTeam[teamName]
			}
			cell := domain.OverviewMatrixCell{
				ChallengeID: challenge.ID,
				TeamName:    teamName,
				TargetID:    targetID,
				Status:      domain.OverviewMatrixStatusNone,
			}
			if targetID != "" {
				if acc := accum[targetID]; acc != nil {
					if len(acc.success) > 0 {
						cell.Status = domain.OverviewMatrixStatusSuccess
						cell.SuccessExploits = toSortedSlice(acc.success)
					} else if len(acc.failed) > 0 {
						cell.Status = domain.OverviewMatrixStatusFailed
						cell.FailedExploits = toSortedSlice(acc.failed)
					}
				}
			}
			matrix.Cells = append(matrix.Cells, cell)
		}
	}

	return matrix, ctx.Err()
}

func (r *Repository) ListRuns(ctx context.Context, filter domain.RunFilter, limit int, cursor string) ([]domain.Run, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		if !matchesRunFilter(run, filter, r) {
			continue
		}
		items = append(items, run)
	}
	sort.Slice(items, func(i, j int) bool {
		ti := runReferenceTime(items[i])
		tj := runReferenceTime(items[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return items[i].ID > items[j].ID
	})

	start := 0
	if cursor != "" {
		for idx, run := range items {
			if run.ID == cursor {
				start = idx + 1
				break
			}
		}
	}
	if limit <= 0 {
		limit = 50
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	chunk := append([]domain.Run(nil), items[start:end]...)
	nextCursor := ""
	if end < len(items) && len(chunk) > 0 {
		nextCursor = chunk[len(chunk)-1].ID
	}
	return chunk, nextCursor, ctx.Err()
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

func (r *Repository) ListRunnerActiveRuns(ctx context.Context, runnerID string) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var runs []domain.Run
	for _, run := range r.runs {
		if run.RunnerID == runnerID && run.Status == domain.RunStatusRunning {
			runs = append(runs, run)
		}
	}
	return runs, ctx.Err()
}

func (r *Repository) ListActiveRuns(ctx context.Context) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var runs []domain.Run
	for _, run := range r.runs {
		if run.Status == domain.RunStatusPending || run.Status == domain.RunStatusRunning {
			runs = append(runs, run)
		}
	}
	return runs, ctx.Err()
}

func (r *Repository) AssignRunToRunner(ctx context.Context, runID, runnerID, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[strings.TrimSpace(runID)]
	if !ok {
		return errors.New("run not found")
	}
	if run.Status != domain.RunStatusPending && run.Status != domain.RunStatusRunning {
		return errors.New("run not assignable")
	}
	run.RunnerID = strings.TrimSpace(runnerID)
	run.RunnerInstanceID = strings.TrimSpace(instanceID)
	r.runs[run.ID] = run
	return ctx.Err()
}

func (r *Repository) ListRunsByRunnerInstance(ctx context.Context, runnerID, instanceID string) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var runs []domain.Run
	targetRunner := strings.TrimSpace(runnerID)
	targetInstance := strings.TrimSpace(instanceID)
	for _, run := range r.runs {
		if strings.TrimSpace(run.RunnerID) != targetRunner {
			continue
		}
		if strings.TrimSpace(run.RunnerInstanceID) != targetInstance {
			continue
		}
		if run.Status != domain.RunStatusPending && run.Status != domain.RunStatusRunning {
			continue
		}
		runs = append(runs, run)
	}
	return runs, ctx.Err()
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

func (r *Repository) GetRunner(ctx context.Context, id string) (domain.Runner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runner, ok := r.runners[id]
	if !ok {
		return domain.Runner{}, errors.New("runner not found")
	}
	return runner, ctx.Err()
}

func (r *Repository) UpsertRunner(ctx context.Context, runner domain.Runner) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if runner.ID == "" {
		runner.ID = newID("runner")
	}
	_runner := r.runners[runner.ID]
	if runner.RegisteredAt.IsZero() {
		if !_runner.RegisteredAt.IsZero() {
			runner.RegisteredAt = _runner.RegisteredAt
		} else {
			runner.RegisteredAt = time.Now().UTC()
		}
	}
	if runner.LastHeartbeat.IsZero() {
		runner.LastHeartbeat = _runner.LastHeartbeat
	}
	if runner.Type == "" {
		runner.Type = _runner.Type
	}
	if runner.Hostname == "" {
		runner.Hostname = _runner.Hostname
	}
	if runner.InstanceID == "" {
		runner.InstanceID = _runner.InstanceID
	}
	if runner.MaxParallel == 0 && _runner.MaxParallel > 0 {
		runner.MaxParallel = _runner.MaxParallel
	}
	if len(runner.Tags) == 0 && len(_runner.Tags) > 0 {
		runner.Tags = _runner.Tags
	}
	if runner.Labels == nil && _runner.Labels != nil {
		runner.Labels = _runner.Labels
	}
	if runner.Config == nil && _runner.Config != nil {
		runner.Config = _runner.Config
	}
	if runner.LastIP == "" {
		runner.LastIP = _runner.LastIP
	}
	r.runners[runner.ID] = runner
	return ctx.Err()
}

func (r *Repository) DeleteRunner(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runners[id]; !ok {
		return errors.New("runner not found")
	}
	delete(r.runners, id)
	return ctx.Err()
}

func (r *Repository) ListSubmitters(ctx context.Context) ([]domain.Submitter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Submitter, 0, len(r.submitters))
	for _, submitter := range r.submitters {
		items = append(items, submitter)
	}
	return items, ctx.Err()
}

func (r *Repository) GetSubmitter(ctx context.Context, id string) (domain.Submitter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	submitter, ok := r.submitters[id]
	if !ok {
		return domain.Submitter{}, sql.ErrNoRows
	}
	return submitter, ctx.Err()
}

func (r *Repository) UpsertSubmitter(ctx context.Context, submitter domain.Submitter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if submitter.ID == "" {
		submitter.ID = newID("submitter")
	}
	current := r.submitters[submitter.ID]
	if submitter.RegisteredAt.IsZero() {
		if !current.RegisteredAt.IsZero() {
			submitter.RegisteredAt = current.RegisteredAt
		} else {
			submitter.RegisteredAt = time.Now().UTC()
		}
	}
	if submitter.LastHeartbeat.IsZero() {
		submitter.LastHeartbeat = current.LastHeartbeat
	}
	if submitter.Type == "" {
		submitter.Type = current.Type
	}
	if submitter.Hostname == "" {
		submitter.Hostname = current.Hostname
	}
	if submitter.Labels == nil && current.Labels != nil {
		submitter.Labels = current.Labels
	}
	if submitter.Config == nil && current.Config != nil {
		submitter.Config = current.Config
	}
	if submitter.Concurrency == 0 && current.Concurrency > 0 {
		submitter.Concurrency = current.Concurrency
	}
	if submitter.WindowSeconds <= 0 {
		if current.WindowSeconds > 0 {
			submitter.WindowSeconds = current.WindowSeconds
		} else {
			submitter.WindowSeconds = float64(time.Minute.Seconds())
		}
	}
	r.submitters[submitter.ID] = submitter
	return ctx.Err()
}

func (r *Repository) DeleteSubmitter(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.submitters[id]; !ok {
		return errors.New("submitter not found")
	}
	delete(r.submitters, id)
	return ctx.Err()
}

func (r *Repository) ListSubmitterFiles(ctx context.Context) ([]storage.ExploitFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.ensureSubmitterScriptLocked(); err != nil {
		return nil, err
	}
	files := r.ensureSubmitterFilesLocked()
	result := make([]storage.ExploitFile, 0, len(files))
	for name, file := range files {
		hash := md5.Sum(file.Data)
		result = append(result, storage.ExploitFile{
			Name:    name,
			Size:    int64(len(file.Data)),
			MD5:     fmt.Sprintf("%x", hash[:]),
			ModTime: file.ModTime,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == submitterScriptName {
			return true
		}
		if result[j].Name == submitterScriptName {
			return false
		}
		return result[i].Name < result[j].Name
	})
	return result, ctx.Err()
}

func (r *Repository) SaveSubmitterFile(ctx context.Context, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(name) == "" {
		return errors.New("file name required")
	}
	name = filepath.Base(name)
	if name == "" {
		return errors.New("invalid file name")
	}
	files := r.ensureSubmitterFilesLocked()
	if name == submitterScriptName {
		return r.SaveSubmitterScript(ctx, string(data))
	}
	files[name] = memoryFile{
		Data:    append([]byte(nil), data...),
		ModTime: time.Now().UTC(),
	}
	return ctx.Err()
}

func (r *Repository) DeleteSubmitterFile(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = filepath.Base(name)
	if name == "" {
		return errors.New("file name required")
	}
	if name == submitterScriptName {
		return errors.New("submit.sh cannot be deleted")
	}
	delete(r.submitterFiles, name)
	return ctx.Err()
}

func (r *Repository) ReadSubmitterFile(ctx context.Context, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = filepath.Base(name)
	if name == "" {
		return nil, errors.New("file name required")
	}
	file, ok := r.submitterFiles[name]
	if !ok {
		return nil, errors.New("file not found")
	}
	return append([]byte(nil), file.Data...), ctx.Err()
}

func (r *Repository) GetSubmitterScript(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	script, err := r.ensureSubmitterScriptLocked()
	return script, errors.Join(err, ctx.Err())
}

func (r *Repository) SaveSubmitterScript(ctx context.Context, script string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(script) == "" {
		return errors.New("submit.sh cannot be empty")
	}
	r.submitterFiles[submitterScriptName] = memoryFile{
		Data:    []byte(script),
		ModTime: time.Now().UTC(),
	}
	return ctx.Err()
}

func (r *Repository) SubmitterArtifact(ctx context.Context) ([]byte, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.ensureSubmitterScriptLocked(); err != nil {
		return nil, "", err
	}
	files := r.ensureSubmitterFilesLocked()
	data, err := createZipFromFilesLocked(files)
	if err != nil {
		return nil, "", err
	}
	sum := md5.Sum(data)
	return data, fmt.Sprintf("%x", sum[:]), ctx.Err()
}

func (r *Repository) ensureExploitFilesLocked(exploitID string) map[string]memoryFile {
	files, ok := r.exploitFiles[exploitID]
	if !ok {
		files = make(map[string]memoryFile)
		r.exploitFiles[exploitID] = files
	}
	return files
}

func (r *Repository) exploitEntrypointLocked(exploitID string) string {
	exploit, ok := r.exploits[exploitID]
	if !ok || exploit.Metadata == nil {
		return ""
	}
	if entry, ok := exploit.Metadata["entrypoint"].(string); ok {
		return entry
	}
	return ""
}

func (r *Repository) ensureRunScriptLocked(exploitID string, entrypoint string) (string, error) {
	files := r.ensureExploitFilesLocked(exploitID)
	if file, ok := files[runScriptName]; ok && len(file.Data) > 0 {
		return string(file.Data), nil
	}
	script := defaultRunScript(entrypoint)
	files[runScriptName] = memoryFile{
		Data:    []byte(script),
		ModTime: time.Now().UTC(),
	}
	return script, nil
}

func (r *Repository) ensureSubmitterFilesLocked() map[string]memoryFile {
	files := r.submitterFiles
	if files == nil {
		files = make(map[string]memoryFile)
		r.submitterFiles = files
	}
	return files
}

func (r *Repository) ensureSubmitterScriptLocked() (string, error) {
	files := r.ensureSubmitterFilesLocked()
	if file, ok := files[submitterScriptName]; ok && len(file.Data) > 0 {
		return string(file.Data), nil
	}
	script := defaultSubmitScript()
	files[submitterScriptName] = memoryFile{
		Data:    []byte(script),
		ModTime: time.Now().UTC(),
	}
	return script, nil
}

func createZipFromFilesLocked(files map[string]memoryFile) ([]byte, error) {
	if len(files) == 0 {
		return nil, errors.New("no artifact files")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for _, name := range names {
		file := files[name]
		header := &zip.FileHeader{
			Name:   filepath.ToSlash(name),
			Method: zip.Deflate,
		}
		modTime := file.ModTime
		if modTime.IsZero() {
			modTime = time.Unix(0, 0)
		}
		header.SetModTime(modTime)
		mode := os.FileMode(0o644)
		if filepath.Base(name) == runScriptName {
			mode = 0o755
		}
		header.SetMode(mode)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			zw.Close()
			return nil, err
		}
		if _, err := writer.Write(file.Data); err != nil {
			zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}

func defaultRunScript(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		entry = "python exp.py"
	}
	if strings.HasPrefix(entry, "#!") {
		if strings.HasSuffix(entry, "\n") {
			return entry
		}
		return entry + "\n"
	}
	return fmt.Sprintf("#!/bin/sh\nset -euo pipefail\n%s \"$1\" \"$2\"\n", entry)
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
		Tags:          []string{"python", "pwntools"},
		ActiveJobs:    0,
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

func matchesRunFilter(run domain.Run, filter domain.RunFilter, repo *Repository) bool {
	if value := strings.TrimSpace(filter.Status); value != "" && !strings.EqualFold(value, string(run.Status)) {
		return false
	}
	if value := strings.TrimSpace(filter.ExploitID); value != "" && !strings.EqualFold(value, run.ExploitID) {
		return false
	}
	if value := strings.TrimSpace(filter.TargetID); value != "" && !strings.EqualFold(value, run.TargetID) {
		return false
	}
	if value := strings.TrimSpace(filter.ChallengeID); value != "" {
		target, ok := repo.targets[run.TargetID]
		if !ok || !strings.EqualFold(target.ChallengeID, value) {
			return false
		}
	}
	if value := strings.TrimSpace(filter.ExploitName); value != "" {
		name := ""
		if exploit, ok := repo.exploits[run.ExploitID]; ok {
			name = exploit.Name
		}
		if name == "" || !strings.Contains(strings.ToLower(name), strings.ToLower(value)) {
			return false
		}
	}
	if value := strings.TrimSpace(filter.TargetName); value != "" {
		name := ""
		if target, ok := repo.targets[run.TargetID]; ok {
			name = target.TeamName
		}
		if name == "" || !strings.Contains(strings.ToLower(name), strings.ToLower(value)) {
			return false
		}
	}
	referenceTime := runReferenceTime(run)
	if !filter.QueuedAfter.IsZero() {
		if referenceTime.IsZero() || referenceTime.Before(filter.QueuedAfter) {
			return false
		}
	}
	if !filter.QueuedBefore.IsZero() {
		if referenceTime.IsZero() || referenceTime.After(filter.QueuedBefore) {
			return false
		}
	}
	return true
}

func runReferenceTime(run domain.Run) time.Time {
	if !run.QueuedAt.IsZero() {
		return run.QueuedAt
	}
	if !run.StartedAt.IsZero() {
		return run.StartedAt
	}
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return time.Time{}
}
