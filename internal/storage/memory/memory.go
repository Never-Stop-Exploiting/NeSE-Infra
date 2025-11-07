package memory

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
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
	"awdinfra/internal/storage"
)

const runScriptName = "run.sh"

type memoryFile struct {
	Data    []byte
	ModTime time.Time
}

// Ensure Repository implements storage.Repository at compile time.
var _ storage.Repository = (*Repository)(nil)

// Repository offers a development-grade in-memory store.
type Repository struct {
	mu           sync.RWMutex
	challenges   map[string]domain.Challenge
	runs         map[string]domain.Run
	flags        map[string]domain.Flag
	targets      map[string]domain.Target
	exploits     map[string]domain.Exploit
	flagLogs     map[string][]string
	flagAttempts map[string][]domain.SubmissionAttempt
	exploitFiles map[string]map[string]memoryFile
	logs         []domain.LogEntry
	runners      map[string]domain.Runner
	scheduler    domain.SchedulerSettings
	overrides    map[string]map[string]domain.TargetExploitOverride
}

// New creates an empty repository.
func New() *Repository {
	repo := &Repository{
		challenges:   make(map[string]domain.Challenge),
		runs:         make(map[string]domain.Run),
		flags:        make(map[string]domain.Flag),
		targets:      make(map[string]domain.Target),
		exploits:     make(map[string]domain.Exploit),
		flagLogs:     make(map[string][]string),
		flagAttempts: make(map[string][]domain.SubmissionAttempt),
		exploitFiles: make(map[string]map[string]memoryFile),
		runners:      make(map[string]domain.Runner),
		scheduler: domain.SchedulerSettings{
			RoundInterval:    time.Minute * 5,
			MaxDispatchBatch: 50,
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

func (r *Repository) RunnableWork(ctx context.Context, limit int) ([]domain.RunWorkItem, error) {
	if limit <= 0 {
		limit = 1
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
	for _, run := range runCopies {
		if run.TargetID == "" {
			continue
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

			flagRegex, flagFile, timeout := jobHintsFromMetadata(exploit.Metadata)

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

func (r *Repository) GetSchedulerSettings(ctx context.Context) (domain.SchedulerSettings, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	settings := r.scheduler
	if settings.RoundInterval <= 0 {
		settings.RoundInterval = time.Minute * 5
	}
	if settings.MaxDispatchBatch <= 0 {
		settings.MaxDispatchBatch = 50
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
	r.mu.Lock()
	defer r.mu.Unlock()
	settings.UpdatedAt = time.Now().UTC()
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

func (r *Repository) ListRuns(ctx context.Context, limit int, cursor string) ([]domain.Run, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
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
	if runner.MaxParallel == 0 && _runner.MaxParallel > 0 {
		runner.MaxParallel = _runner.MaxParallel
	}
	if len(runner.Tags) == 0 && len(_runner.Tags) > 0 {
		runner.Tags = _runner.Tags
	}
	if runner.Labels == nil && _runner.Labels != nil {
		runner.Labels = _runner.Labels
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

func jobHintsFromMetadata(meta map[string]any) (string, string, time.Duration) {
	if meta == nil {
		return "", "", 0
	}
	flagRegex := ""
	flagFile := ""
	timeout := time.Duration(0)

	if value, ok := meta["flag_regex"].(string); ok {
		flagRegex = value
	} else if value, ok := meta["flagRegex"].(string); ok {
		flagRegex = value
	}

	if value, ok := meta["flag_file"].(string); ok {
		flagFile = value
	} else if value, ok := meta["flagFile"].(string); ok {
		flagFile = value
	}

	timeoutKeys := []string{"timeout_seconds", "timeoutSeconds"}
	for _, key := range timeoutKeys {
		if timeout > 0 {
			break
		}
		switch value := meta[key].(type) {
		case float64:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int32:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		case int64:
			if value > 0 {
				timeout = time.Duration(value) * time.Second
			}
		}
	}
	return flagRegex, flagFile, timeout
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
