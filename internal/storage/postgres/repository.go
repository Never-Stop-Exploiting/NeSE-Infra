package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"awdinfra/internal/domain"
	"awdinfra/internal/jobs"
	"awdinfra/internal/storage"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var _ storage.Repository = (*Repository)(nil)

// Config captures PostgreSQL connection parameters used by the repository.
type Config struct {
	DSN             string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ArtifactDir     string
}

// Repository implements storage.Repository backed by PostgreSQL.
type Repository struct {
	db          *sql.DB
	artifactDir string
}

// New initialises a PostgreSQL backed repository, ensuring the required schema exists.
func New(cfg Config) (*Repository, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("postgres DSN required")
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	db.SetMaxOpenConns(maxOpen)

	maxIdle := cfg.MaxIdleConns
	if maxIdle < 0 {
		maxIdle = 0
	}
	if maxIdle == 0 && cfg.MaxIdleConns == 0 {
		maxIdle = 5
	}
	db.SetMaxIdleConns(maxIdle)

	lifetime := cfg.ConnMaxLifetime
	if lifetime <= 0 {
		lifetime = time.Minute * 5
	}
	db.SetConnMaxLifetime(lifetime)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	schemaCtx, schemaCancel := context.WithTimeout(context.Background(), time.Second*30)
	defer schemaCancel()
	if err := ensureSchema(schemaCtx, db); err != nil {
		db.Close()
		return nil, err
	}

	artifactDir := strings.TrimSpace(cfg.ArtifactDir)
	if artifactDir == "" {
		artifactDir = filepath.Join(os.TempDir(), "awdinfra", "artifacts")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare artifact dir: %w", err)
	}

	return &Repository{
		db:          db,
		artifactDir: artifactDir,
	}, nil
}

// Close releases underlying database resources.
func (r *Repository) Close() error {
	return r.db.Close()
}

const schemaLockID int64 = 0x617764696e667261 // "awdinfra"

func ensureSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, schemaLockID); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS challenges (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT,
			service_type TEXT,
			description TEXT,
			metadata JSONB,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS targets (
			id TEXT PRIMARY KEY,
			challenge_id TEXT REFERENCES challenges(id) ON DELETE CASCADE,
			team_name TEXT NOT NULL,
			ip TEXT NOT NULL,
			port INTEGER NOT NULL,
			status TEXT NOT NULL,
			concurrency_limit INTEGER NOT NULL DEFAULT 1,
			labels JSONB,
			last_seen_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_targets_challenge_id ON targets(challenge_id);`,
		`CREATE TABLE IF NOT EXISTS exploits (
			id TEXT PRIMARY KEY,
			challenge_id TEXT REFERENCES challenges(id) ON DELETE SET NULL,
			name TEXT NOT NULL,
			description TEXT,
			enabled BOOLEAN NOT NULL DEFAULT false,
			runner_tags JSONB,
			schedule_weight INTEGER NOT NULL DEFAULT 1,
			cooldown_seconds INTEGER NOT NULL DEFAULT 0,
			last_run_at TIMESTAMPTZ,
			metadata JSONB,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_exploits_challenge_id ON exploits(challenge_id);`,
		`CREATE TABLE IF NOT EXISTS exploit_revisions (
			id TEXT PRIMARY KEY,
			exploit_id TEXT REFERENCES exploits(id) ON DELETE CASCADE,
			digest TEXT,
			storage_uri TEXT,
			command JSONB,
			env JSONB,
			created_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_exploit_revisions_exploit_id ON exploit_revisions(exploit_id);`,
		`CREATE TABLE IF NOT EXISTS exploit_artifacts (
			revision_id TEXT PRIMARY KEY REFERENCES exploit_revisions(id) ON DELETE CASCADE,
			file_name TEXT,
			md5 TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			exploit_id TEXT REFERENCES exploits(id) ON DELETE SET NULL,
			exploit_revision TEXT,
			target_id TEXT REFERENCES targets(id) ON DELETE SET NULL,
			runner_id TEXT,
			status TEXT NOT NULL,
			queued_at TIMESTAMPTZ,
			started_at TIMESTAMPTZ,
			finished_at TIMESTAMPTZ,
			log_uri TEXT,
			flag_id TEXT,
			error_message TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_flag_id ON runs(flag_id);`,
		`CREATE TABLE IF NOT EXISTS flags (
			id TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			challenge_id TEXT REFERENCES challenges(id) ON DELETE SET NULL,
			target_id TEXT REFERENCES targets(id) ON DELETE SET NULL,
			run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
			status TEXT NOT NULL,
			submission_count INTEGER NOT NULL DEFAULT 0,
			last_submitted_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_flags_status ON flags(status);`,
		`CREATE TABLE IF NOT EXISTS flag_logs (
			id BIGSERIAL PRIMARY KEY,
			flag_id TEXT REFERENCES flags(id) ON DELETE CASCADE,
			message TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_flag_logs_flag_id ON flag_logs(flag_id);`,
		`CREATE TABLE IF NOT EXISTS submission_attempts (
			id TEXT PRIMARY KEY,
			flag_id TEXT REFERENCES flags(id) ON DELETE CASCADE,
			submitter_id TEXT,
			status_code INTEGER,
			error TEXT,
			submitted_at TIMESTAMPTZ NOT NULL,
			next_retry_at TIMESTAMPTZ,
			payload JSONB
		);`,
		`CREATE INDEX IF NOT EXISTS idx_submission_attempts_flag_id ON submission_attempts(flag_id);`,
		`CREATE TABLE IF NOT EXISTS log_entries (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMPTZ NOT NULL,
			level TEXT,
			source TEXT,
			message TEXT,
			run_id TEXT,
			flag_id TEXT,
			fields JSONB
		);`,
		`CREATE INDEX IF NOT EXISTS idx_log_entries_flag_id ON log_entries(flag_id);`,
		`CREATE INDEX IF NOT EXISTS idx_log_entries_run_id ON log_entries(run_id);`,
		`CREATE TABLE IF NOT EXISTS scheduler_settings (
			id TEXT PRIMARY KEY,
			round_interval_seconds INTEGER NOT NULL,
			max_dispatch_batch INTEGER NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT false,
			strategy TEXT NOT NULL DEFAULT 'interval',
			continuous_delay_seconds INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO scheduler_settings (id, round_interval_seconds, max_dispatch_batch, updated_at, enabled, strategy, continuous_delay_seconds)
			VALUES ('default', 300, 50, NOW(), false, 'interval', 0)
			ON CONFLICT (id) DO NOTHING;`,
		`CREATE TABLE IF NOT EXISTS target_exploit_overrides (
			target_id TEXT REFERENCES targets(id) ON DELETE CASCADE,
			exploit_id TEXT REFERENCES exploits(id) ON DELETE CASCADE,
			enabled BOOLEAN NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (target_id, exploit_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_target_exploit_overrides_exploit_id ON target_exploit_overrides(exploit_id);`,
		`CREATE TABLE IF NOT EXISTS runners (
			id TEXT PRIMARY KEY,
			type TEXT,
			hostname TEXT,
			labels JSONB,
			tags JSONB,
			max_parallel INTEGER,
			active_jobs INTEGER,
			last_heartbeat TIMESTAMPTZ,
			status TEXT,
			registered_at TIMESTAMPTZ NOT NULL,
			config JSONB,
			last_ip TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS submitters (
			id TEXT PRIMARY KEY,
			type TEXT,
			hostname TEXT,
			status TEXT,
			concurrency INTEGER NOT NULL DEFAULT 1,
			in_flight INTEGER NOT NULL DEFAULT 0,
			pending INTEGER NOT NULL DEFAULT 0,
			recent_throughput DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
			pressure DOUBLE PRECISION NOT NULL DEFAULT 0,
			window_seconds DOUBLE PRECISION NOT NULL DEFAULT 60,
			latency_sum_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
			processed_count INTEGER NOT NULL DEFAULT 0,
			labels JSONB,
			config JSONB,
			last_heartbeat TIMESTAMPTZ,
			registered_at TIMESTAMPTZ
		);`,
		`ALTER TABLE runners ADD COLUMN IF NOT EXISTS tags JSONB;`,
		`ALTER TABLE runners ADD COLUMN IF NOT EXISTS config JSONB;`,
		`ALTER TABLE runners ADD COLUMN IF NOT EXISTS last_ip TEXT;`,
		`ALTER TABLE runners ADD COLUMN IF NOT EXISTS active_jobs INTEGER;`,
		`ALTER TABLE submitters ADD COLUMN IF NOT EXISTS window_seconds DOUBLE PRECISION NOT NULL DEFAULT 60;`,
		`ALTER TABLE submitters ADD COLUMN IF NOT EXISTS latency_sum_seconds DOUBLE PRECISION NOT NULL DEFAULT 0;`,
		`ALTER TABLE submitters ADD COLUMN IF NOT EXISTS processed_count INTEGER NOT NULL DEFAULT 0;`,
		`CREATE INDEX IF NOT EXISTS idx_submitters_status ON submitters(status);`,
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema transaction: %w", err)
	}
	return nil
}

func (r *Repository) Challenges(ctx context.Context) ([]domain.Challenge, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, category, service_type, description, metadata, created_at, updated_at
		FROM challenges
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Challenge
	for rows.Next() {
		var challenge domain.Challenge
		var metadata []byte
		var category, serviceType, description sql.NullString
		if err := rows.Scan(
			&challenge.ID,
			&challenge.Name,
			&category,
			&serviceType,
			&description,
			&metadata,
			&challenge.CreatedAt,
			&challenge.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if category.Valid {
			challenge.Category = category.String
		}
		if serviceType.Valid {
			challenge.ServiceType = serviceType.String
		}
		if description.Valid {
			challenge.Description = description.String
		}
		if len(metadata) > 0 {
			meta, err := unmarshalMap(metadata)
			if err != nil {
				return nil, err
			}
			challenge.Metadata = meta
		}
		items = append(items, challenge)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) GetChallenge(ctx context.Context, id string) (domain.Challenge, error) {
	var challenge domain.Challenge
	var metadata []byte
	var category, serviceType, description sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, category, service_type, description, metadata, created_at, updated_at
		FROM challenges
		WHERE id = $1`, id).Scan(
		&challenge.ID,
		&challenge.Name,
		&category,
		&serviceType,
		&description,
		&metadata,
		&challenge.CreatedAt,
		&challenge.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Challenge{}, errors.New("challenge not found")
	}
	if err != nil {
		return domain.Challenge{}, err
	}
	if category.Valid {
		challenge.Category = category.String
	}
	if serviceType.Valid {
		challenge.ServiceType = serviceType.String
	}
	if description.Valid {
		challenge.Description = description.String
	}
	if len(metadata) > 0 {
		meta, err := unmarshalMap(metadata)
		if err != nil {
			return domain.Challenge{}, err
		}
		challenge.Metadata = meta
	}
	return challenge, nil
}

func (r *Repository) CreateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error) {
	if strings.TrimSpace(challenge.Name) == "" {
		return domain.Challenge{}, errors.New("challenge name required")
	}
	if challenge.ID == "" {
		challenge.ID = generateID("challenge")
	}
	now := time.Now().UTC()
	challenge.CreatedAt = now
	challenge.UpdatedAt = now

	metadata, err := marshalJSON(challenge.Metadata)
	if err != nil {
		return domain.Challenge{}, err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO challenges (id, name, category, service_type, description, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		challenge.ID,
		challenge.Name,
		nullString(challenge.Category),
		nullString(challenge.ServiceType),
		nullString(challenge.Description),
		metadata,
		challenge.CreatedAt,
		challenge.UpdatedAt,
	)
	if err != nil {
		return domain.Challenge{}, err
	}
	return challenge, nil
}

func (r *Repository) UpdateChallenge(ctx context.Context, challenge domain.Challenge) (domain.Challenge, error) {
	if challenge.ID == "" {
		return domain.Challenge{}, errors.New("challenge id required")
	}
	existing, err := r.GetChallenge(ctx, challenge.ID)
	if err != nil {
		return domain.Challenge{}, err
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
	metadata, err := marshalJSON(existing.Metadata)
	if err != nil {
		return domain.Challenge{}, err
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE challenges
		SET name=$1, category=$2, service_type=$3, description=$4, metadata=$5, updated_at=$6
		WHERE id=$7`,
		existing.Name,
		nullString(existing.Category),
		nullString(existing.ServiceType),
		nullString(existing.Description),
		metadata,
		existing.UpdatedAt,
		existing.ID,
	)
	if err != nil {
		return domain.Challenge{}, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return domain.Challenge{}, errors.New("challenge not found")
	}
	return existing, nil
}

func (r *Repository) DeleteChallenge(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM challenges WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("challenge not found")
	}
	return nil
}

func (r *Repository) CreateRun(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return errors.New("nil run")
	}
	if run.ID == "" {
		run.ID = generateID("run")
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO runs (id, exploit_id, exploit_revision, target_id, runner_id, status, queued_at, started_at, finished_at, log_uri, flag_id, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		run.ID,
		nullString(run.ExploitID),
		nullString(run.ExploitRevision),
		nullString(run.TargetID),
		nullString(run.RunnerID),
		string(run.Status),
		nullTime(run.QueuedAt),
		nullTime(run.StartedAt),
		nullTime(run.FinishedAt),
		nullString(run.LogURI),
		nullString(run.FlagID),
		nullString(run.ErrorMessage),
	)
	return err
}

func (r *Repository) UpdateRun(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return errors.New("nil run")
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE runs
		SET exploit_id=$1, exploit_revision=$2, target_id=$3, runner_id=$4, status=$5, queued_at=$6, started_at=$7, finished_at=$8, log_uri=$9, flag_id=$10, error_message=$11
		WHERE id=$12`,
		nullString(run.ExploitID),
		nullString(run.ExploitRevision),
		nullString(run.TargetID),
		nullString(run.RunnerID),
		string(run.Status),
		nullTime(run.QueuedAt),
		nullTime(run.StartedAt),
		nullTime(run.FinishedAt),
		nullString(run.LogURI),
		nullString(run.FlagID),
		nullString(run.ErrorMessage),
		run.ID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("run not found")
	}
	return nil
}

func (r *Repository) RunnableWork(ctx context.Context, limit int, settings domain.SchedulerSettings) ([]domain.RunWorkItem, error) {
	if limit <= 0 {
		limit = 1
	}
	strategy := settings.Strategy
	if strategy == "" {
		strategy = domain.SchedulerStrategyInterval
	}

	targets, err := r.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}

	exploits, err := r.ListExploits(ctx)
	if err != nil {
		return nil, err
	}
	if len(exploits) == 0 {
		return nil, nil
	}

	overrides, err := r.ListTargetExploitOverrides(ctx)
	if err != nil {
		return nil, err
	}
	overrideIndex := make(map[string]map[string]domain.TargetExploitOverride)
	for _, override := range overrides {
		if overrideIndex[override.TargetID] == nil {
			overrideIndex[override.TargetID] = make(map[string]domain.TargetExploitOverride)
		}
		overrideIndex[override.TargetID][override.ExploitID] = override
	}

	activeCounts := make(map[string]int)
	rows, err := r.db.QueryContext(ctx, `
		SELECT target_id, COUNT(1)
		FROM runs
		WHERE status IN ('pending', 'running')
		GROUP BY target_id`)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var targetID sql.NullString
			var count int
			if scanErr := rows.Scan(&targetID, &count); scanErr != nil {
				return nil, scanErr
			}
			if targetID.Valid {
				activeCounts[targetID.String] = count
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	var pairActive map[string]int
	var lastFinished map[string]time.Time
	continuousDelay := settings.ContinuousDelay
	if continuousDelay < 0 {
		continuousDelay = 0
	}
	if strategy == domain.SchedulerStrategyContinuous {
		pairActive = make(map[string]int)
		pairRows, err := r.db.QueryContext(ctx, `
			SELECT target_id, exploit_id, COUNT(1)
			FROM runs
			WHERE status IN ('pending', 'running')
			GROUP BY target_id, exploit_id`)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if pairRows != nil {
			defer pairRows.Close()
			for pairRows.Next() {
				var targetID, exploitID sql.NullString
				var count int
				if scanErr := pairRows.Scan(&targetID, &exploitID, &count); scanErr != nil {
					return nil, scanErr
				}
				if targetID.Valid && exploitID.Valid {
					pairActive[schedulerPairKey(targetID.String, exploitID.String)] = count
				}
			}
			if err := pairRows.Err(); err != nil {
				return nil, err
			}
		}

		lastFinished = make(map[string]time.Time)
		lastRows, err := r.db.QueryContext(ctx, `
			SELECT target_id, exploit_id, MAX(COALESCE(finished_at, started_at, queued_at)) AS last_ts
			FROM runs
			WHERE status NOT IN ('pending', 'running')
				AND target_id IS NOT NULL
				AND exploit_id IS NOT NULL
			GROUP BY target_id, exploit_id`)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if lastRows != nil {
			defer lastRows.Close()
			for lastRows.Next() {
				var targetID, exploitID sql.NullString
				var ts sql.NullTime
				if scanErr := lastRows.Scan(&targetID, &exploitID, &ts); scanErr != nil {
					return nil, scanErr
				}
				if targetID.Valid && exploitID.Valid && ts.Valid {
					lastFinished[schedulerPairKey(targetID.String, exploitID.String)] = ts.Time
				}
			}
			if err := lastRows.Err(); err != nil {
				return nil, err
			}
		}
	}

	exploitsByChallenge := make(map[string][]domain.Exploit)
	for _, exploit := range exploits {
		exploitsByChallenge[exploit.ChallengeID] = append(exploitsByChallenge[exploit.ChallengeID], exploit)
	}

	work := make([]domain.RunWorkItem, 0, limit)
	now := time.Now().UTC()

	for _, target := range targets {
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
			if overrides := overrideIndex[target.ID]; overrides != nil {
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

			item := domain.RunWorkItem{
				RunID:     run.ID,
				Exploit:   exploit,
				Target:    target,
				Artifact:  artifact,
				FlagRegex: flagRegex,
				FlagFile:  flagFile,
				Timeout:   timeout,
			}
			work = append(work, item)
			activeCounts[target.ID]++
			if strategy == domain.SchedulerStrategyContinuous && pairKey != "" {
				pairActive[pairKey]++
			}

			if _, err := r.db.ExecContext(ctx, `
				UPDATE exploits
				SET last_run_at = $1
				WHERE id = $2`,
				now,
				exploit.ID,
			); err != nil {
				return nil, err
			}

			if activeCounts[target.ID] >= concurrency {
				break
			}
		}
	}

	return work, nil
}

func schedulerPairKey(targetID, exploitID string) string {
	return targetID + "|" + exploitID
}

func (r *Repository) GetSchedulerSettings(ctx context.Context) (domain.SchedulerSettings, error) {
	var roundSeconds int64
	var maxBatch int
	var updatedAt time.Time
	var enabled bool
	var strategy sql.NullString
	var continuousDelay sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT round_interval_seconds, max_dispatch_batch, updated_at, enabled, strategy, continuous_delay_seconds
		FROM scheduler_settings
		WHERE id = 'default'`).Scan(&roundSeconds, &maxBatch, &updatedAt, &enabled, &strategy, &continuousDelay)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SchedulerSettings{
			Strategy:         domain.SchedulerStrategyInterval,
			RoundInterval:    time.Minute * 5,
			MaxDispatchBatch: 50,
			ContinuousDelay:  0,
			Enabled:          false,
		}, nil
	}
	if err != nil {
		return domain.SchedulerSettings{}, err
	}
	settings := domain.SchedulerSettings{
		Strategy:         domain.SchedulerStrategy(strategy.String),
		RoundInterval:    time.Duration(roundSeconds) * time.Second,
		MaxDispatchBatch: maxBatch,
		ContinuousDelay:  time.Duration(continuousDelay.Int64) * time.Second,
		UpdatedAt:        updatedAt,
		Enabled:          enabled,
	}
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
	return settings, nil
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
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO scheduler_settings (id, round_interval_seconds, max_dispatch_batch, updated_at, enabled, strategy, continuous_delay_seconds)
		VALUES ('default', $1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET round_interval_seconds = EXCLUDED.round_interval_seconds,
			max_dispatch_batch = EXCLUDED.max_dispatch_batch,
			updated_at = EXCLUDED.updated_at,
			enabled = EXCLUDED.enabled,
			strategy = EXCLUDED.strategy,
			continuous_delay_seconds = EXCLUDED.continuous_delay_seconds`,
		int64(settings.RoundInterval/time.Second),
		settings.MaxDispatchBatch,
		now,
		settings.Enabled,
		string(strategy),
		int64(settings.ContinuousDelay/time.Second),
	)
	if err != nil {
		return domain.SchedulerSettings{}, err
	}
	settings.Strategy = strategy
	settings.UpdatedAt = now
	return settings, nil
}

func (r *Repository) ListTargetExploitOverrides(ctx context.Context) ([]domain.TargetExploitOverride, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT target_id, exploit_id, enabled, updated_at
		FROM target_exploit_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var overrides []domain.TargetExploitOverride
	for rows.Next() {
		var item domain.TargetExploitOverride
		if err := rows.Scan(&item.TargetID, &item.ExploitID, &item.Enabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		overrides = append(overrides, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return overrides, nil
}

func (r *Repository) UpsertTargetExploitOverride(ctx context.Context, override domain.TargetExploitOverride) error {
	if strings.TrimSpace(override.TargetID) == "" || strings.TrimSpace(override.ExploitID) == "" {
		return errors.New("target and exploit ids required")
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO target_exploit_overrides (target_id, exploit_id, enabled, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (target_id, exploit_id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
			updated_at = EXCLUDED.updated_at`,
		override.TargetID,
		override.ExploitID,
		override.Enabled,
		now,
	)
	return err
}

func (r *Repository) DeleteTargetExploitOverride(ctx context.Context, targetID string, exploitID string) error {
	if strings.TrimSpace(targetID) == "" || strings.TrimSpace(exploitID) == "" {
		return errors.New("target and exploit ids required")
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM target_exploit_overrides
		WHERE target_id = $1 AND exploit_id = $2`,
		targetID,
		exploitID,
	)
	return err
}

func (r *Repository) UpdateFlag(ctx context.Context, flag *domain.Flag) error {
	if flag == nil {
		return errors.New("nil flag")
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE flags
		SET value=$1, challenge_id=$2, target_id=$3, run_id=$4, status=$5, submission_count=$6, last_submitted_at=$7
		WHERE id=$8`,
		flag.Value,
		nullString(flag.ChallengeID),
		nullString(flag.TargetID),
		nullString(flag.RunID),
		string(flag.Status),
		flag.SubmissionCount,
		nullTime(flag.LastSubmittedAt),
		flag.ID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("flag not found")
	}
	return nil
}

func (r *Repository) InsertFlag(ctx context.Context, flag *domain.Flag) error {
	if flag == nil {
		return errors.New("nil flag")
	}
	if flag.ID == "" {
		flag.ID = generateID("flag")
	}
	if flag.Status == "" {
		flag.Status = domain.FlagStatusNew
	}
	if flag.CreatedAt.IsZero() {
		flag.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO flags (id, value, challenge_id, target_id, run_id, status, submission_count, last_submitted_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING`,
		flag.ID,
		flag.Value,
		nullString(flag.ChallengeID),
		nullString(flag.TargetID),
		nullString(flag.RunID),
		string(flag.Status),
		flag.SubmissionCount,
		nullTime(flag.LastSubmittedAt),
		flag.CreatedAt,
	)
	return err
}

func (r *Repository) LogRunArtifact(ctx context.Context, runID string, uri string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE runs
		SET log_uri=$1
		WHERE id=$2`,
		nullString(uri),
		runID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("run not found")
	}
	return nil
}

func (r *Repository) ListTargets(ctx context.Context) ([]domain.Target, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, challenge_id, team_name, ip, port, status, concurrency_limit, labels, last_seen_at, created_at, updated_at
		FROM targets
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []domain.Target
	for rows.Next() {
		var target domain.Target
		var labels []byte
		var lastSeen sql.NullTime
		var challengeID sql.NullString
		if err := rows.Scan(
			&target.ID,
			&challengeID,
			&target.TeamName,
			&target.IP,
			&target.Port,
			&target.Status,
			&target.ConcurrencyLimit,
			&labels,
			&lastSeen,
			&target.CreatedAt,
			&target.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if challengeID.Valid {
			target.ChallengeID = challengeID.String
		}
		if lastSeen.Valid {
			target.LastSeenAt = lastSeen.Time
		}
		if len(labels) > 0 {
			data, err := unmarshalStringMap(labels)
			if err != nil {
				return nil, err
			}
			target.Labels = data
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (r *Repository) GetTarget(ctx context.Context, id string) (domain.Target, error) {
	var target domain.Target
	var challengeID sql.NullString
	var labels []byte
	var lastSeen sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, challenge_id, team_name, ip, port, status, concurrency_limit, labels, last_seen_at, created_at, updated_at
		FROM targets
		WHERE id = $1`,
		id,
	).Scan(
		&target.ID,
		&challengeID,
		&target.TeamName,
		&target.IP,
		&target.Port,
		&target.Status,
		&target.ConcurrencyLimit,
		&labels,
		&lastSeen,
		&target.CreatedAt,
		&target.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Target{}, errors.New("target not found")
	}
	if err != nil {
		return domain.Target{}, err
	}
	if challengeID.Valid {
		target.ChallengeID = challengeID.String
	}
	if lastSeen.Valid {
		target.LastSeenAt = lastSeen.Time
	}
	if len(labels) > 0 {
		data, err := unmarshalStringMap(labels)
		if err != nil {
			return domain.Target{}, err
		}
		target.Labels = data
	}
	return target, nil
}

func (r *Repository) CreateTarget(ctx context.Context, target domain.Target) (domain.Target, error) {
	if target.TeamName == "" || target.IP == "" || target.Port <= 0 {
		return domain.Target{}, errors.New("invalid target")
	}
	if target.ID == "" {
		target.ID = generateID("target")
	}
	now := time.Now().UTC()
	target.CreatedAt = now
	target.UpdatedAt = now
	if target.ConcurrencyLimit <= 0 {
		target.ConcurrencyLimit = 1
	}
	if target.Status == "" {
		target.Status = "active"
	}

	labels, err := marshalJSON(target.Labels)
	if err != nil {
		return domain.Target{}, err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO targets (id, challenge_id, team_name, ip, port, status, concurrency_limit, labels, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		target.ID,
		nullString(target.ChallengeID),
		target.TeamName,
		target.IP,
		target.Port,
		target.Status,
		target.ConcurrencyLimit,
		labels,
		nullTime(target.LastSeenAt),
		target.CreatedAt,
		target.UpdatedAt,
	)
	if err != nil {
		return domain.Target{}, err
	}
	return target, nil
}

func (r *Repository) UpdateTarget(ctx context.Context, target domain.Target) (domain.Target, error) {
	if strings.TrimSpace(target.ID) == "" {
		return domain.Target{}, errors.New("target id required")
	}
	existing, err := r.GetTarget(ctx, target.ID)
	if err != nil {
		return domain.Target{}, err
	}
	updated := existing
	if strings.TrimSpace(target.TeamName) != "" {
		updated.TeamName = target.TeamName
	}
	if target.Port > 0 {
		updated.Port = target.Port
	}
	if strings.TrimSpace(target.IP) != "" {
		updated.IP = target.IP
	}
	if strings.TrimSpace(target.Status) != "" {
		updated.Status = target.Status
	}
	if strings.TrimSpace(target.ChallengeID) != "" {
		updated.ChallengeID = target.ChallengeID
	}
	if target.ConcurrencyLimit > 0 {
		updated.ConcurrencyLimit = target.ConcurrencyLimit
	}
	if target.Labels != nil {
		updated.Labels = target.Labels
	}
	updated.UpdatedAt = time.Now().UTC()

	labels, err := marshalJSON(updated.Labels)
	if err != nil {
		return domain.Target{}, err
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE targets
		SET challenge_id=$1,
			team_name=$2,
			ip=$3,
			port=$4,
			status=$5,
			concurrency_limit=$6,
			labels=$7,
			updated_at=$8
		WHERE id=$9`,
		nullString(updated.ChallengeID),
		updated.TeamName,
		updated.IP,
		updated.Port,
		updated.Status,
		updated.ConcurrencyLimit,
		labels,
		updated.UpdatedAt,
		updated.ID,
	)
	if err != nil {
		return domain.Target{}, err
	}
	return updated, nil
}

func (r *Repository) DeleteTarget(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("target id required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM target_exploit_overrides WHERE target_id = $1`, id); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM targets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("target not found")
	}
	return tx.Commit()
}

func (r *Repository) BulkInsertTargets(ctx context.Context, targets []domain.Target) error {
	if len(targets) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO targets (id, challenge_id, team_name, ip, port, status, concurrency_limit, labels, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, target := range targets {
		if target.ID == "" {
			target.ID = generateID("target")
		}
		if target.ConcurrencyLimit <= 0 {
			target.ConcurrencyLimit = 1
		}
		if target.Status == "" {
			target.Status = "active"
		}
		target.CreatedAt = now
		target.UpdatedAt = now
		labels, err := marshalJSON(target.Labels)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx,
			target.ID,
			nullString(target.ChallengeID),
			target.TeamName,
			target.IP,
			target.Port,
			target.Status,
			target.ConcurrencyLimit,
			labels,
			nullTime(target.LastSeenAt),
			target.CreatedAt,
			target.UpdatedAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) ListExploits(ctx context.Context) ([]domain.Exploit, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, challenge_id, name, description, enabled, runner_tags, schedule_weight, cooldown_seconds, last_run_at, metadata, created_at, updated_at
		FROM exploits
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exploits []domain.Exploit
	for rows.Next() {
		var exploit domain.Exploit
		var runnerTags, metadata []byte
		var lastRun sql.NullTime
		var challengeID, description sql.NullString
		var cooldownSeconds int64

		if err := rows.Scan(
			&exploit.ID,
			&challengeID,
			&exploit.Name,
			&description,
			&exploit.Enabled,
			&runnerTags,
			&exploit.ScheduleWeight,
			&cooldownSeconds,
			&lastRun,
			&metadata,
			&exploit.CreatedAt,
			&exploit.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if challengeID.Valid {
			exploit.ChallengeID = challengeID.String
		}
		if description.Valid {
			exploit.Description = description.String
		}
		if lastRun.Valid {
			exploit.LastRunAt = lastRun.Time
		}
		if len(runnerTags) > 0 {
			tags, err := unmarshalStringSlice(runnerTags)
			if err != nil {
				return nil, err
			}
			exploit.RunnerTags = tags
		}
		if len(metadata) > 0 {
			meta, err := unmarshalMap(metadata)
			if err != nil {
				return nil, err
			}
			exploit.Metadata = meta
		}
		if cooldownSeconds > 0 {
			exploit.Cooldown = time.Duration(cooldownSeconds) * time.Second
		} else {
			exploit.Cooldown = 0
		}
		exploits = append(exploits, exploit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return exploits, nil
}

func (r *Repository) GetExploit(ctx context.Context, id string) (domain.Exploit, error) {
	var exploit domain.Exploit
	var runnerTags, metadata []byte
	var lastRun sql.NullTime
	var challengeID, description sql.NullString
	var cooldownSeconds int64
	err := r.db.QueryRowContext(ctx, `
		SELECT id, challenge_id, name, description, enabled, runner_tags, schedule_weight, cooldown_seconds, last_run_at, metadata, created_at, updated_at
		FROM exploits
		WHERE id = $1`,
		id,
	).Scan(
		&exploit.ID,
		&challengeID,
		&exploit.Name,
		&description,
		&exploit.Enabled,
		&runnerTags,
		&exploit.ScheduleWeight,
		&cooldownSeconds,
		&lastRun,
		&metadata,
		&exploit.CreatedAt,
		&exploit.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Exploit{}, storage.ErrExploitNotFound
	}
	if err != nil {
		return domain.Exploit{}, err
	}
	if challengeID.Valid {
		exploit.ChallengeID = challengeID.String
	}
	if description.Valid {
		exploit.Description = description.String
	}
	if lastRun.Valid {
		exploit.LastRunAt = lastRun.Time
	}
	if len(runnerTags) > 0 {
		tags, err := unmarshalStringSlice(runnerTags)
		if err != nil {
			return domain.Exploit{}, err
		}
		exploit.RunnerTags = tags
	}
	if len(metadata) > 0 {
		meta, err := unmarshalMap(metadata)
		if err != nil {
			return domain.Exploit{}, err
		}
		exploit.Metadata = meta
	}
	if cooldownSeconds > 0 {
		exploit.Cooldown = time.Duration(cooldownSeconds) * time.Second
	}
	return exploit, nil
}

func (r *Repository) CreateExploit(ctx context.Context, exploit domain.Exploit) (domain.Exploit, error) {
	if strings.TrimSpace(exploit.Name) == "" {
		return domain.Exploit{}, errors.New("exploit name required")
	}
	if exploit.ID == "" {
		exploit.ID = generateID("exploit")
	}
	now := time.Now().UTC()
	exploit.CreatedAt = now
	exploit.UpdatedAt = now
	if exploit.ScheduleWeight == 0 {
		exploit.ScheduleWeight = 1
	}

	runnerTags, err := marshalJSON(exploit.RunnerTags)
	if err != nil {
		return domain.Exploit{}, err
	}
	metadata, err := marshalJSON(exploit.Metadata)
	if err != nil {
		return domain.Exploit{}, err
	}

	cooldownSeconds := int(exploit.Cooldown.Seconds())
	if cooldownSeconds < 0 {
		cooldownSeconds = 0
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO exploits (id, challenge_id, name, description, enabled, runner_tags, schedule_weight, cooldown_seconds, last_run_at, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		exploit.ID,
		nullString(exploit.ChallengeID),
		exploit.Name,
		nullString(exploit.Description),
		exploit.Enabled,
		runnerTags,
		exploit.ScheduleWeight,
		cooldownSeconds,
		nullTime(exploit.LastRunAt),
		metadata,
		exploit.CreatedAt,
		exploit.UpdatedAt,
	)
	if err != nil {
		return domain.Exploit{}, err
	}
	entry := ""
	if exploit.Metadata != nil {
		if value, ok := exploit.Metadata["entrypoint"].(string); ok {
			entry = value
		}
	}
	if _, err := r.ensureDefaultRunScript(exploit.ID, entry); err != nil {
		return domain.Exploit{}, err
	}
	return exploit, nil
}

func (r *Repository) DeleteExploit(ctx context.Context, exploitID string) error {
	if strings.TrimSpace(exploitID) == "" {
		return errors.New("exploit id required")
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM exploits WHERE id = $1`, exploitID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return storage.ErrExploitNotFound
	}
	if err := os.RemoveAll(r.exploitDir(exploitID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cleanup exploit directory: %w", err)
	}
	return nil
}

func (r *Repository) SetExploitEnabled(ctx context.Context, exploitID string, enabled bool) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE exploits
		SET enabled=$1, updated_at=$2
		WHERE id=$3`,
		enabled,
		time.Now().UTC(),
		exploitID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return storage.ErrExploitNotFound
	}
	return nil
}

func (r *Repository) SetExploitTimeout(ctx context.Context, exploitID string, timeout time.Duration) error {
	var metadataRaw []byte
	err := r.db.QueryRowContext(ctx, `SELECT metadata FROM exploits WHERE id = $1`, exploitID).Scan(&metadataRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrExploitNotFound
	}
	if err != nil {
		return err
	}
	meta, err := unmarshalMap(metadataRaw)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["timeout_seconds"] = int(timeout.Seconds())
	metadata, err := marshalJSON(meta)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE exploits
		SET metadata=$1, updated_at=$2
		WHERE id=$3`,
		metadata,
		time.Now().UTC(),
		exploitID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return storage.ErrExploitNotFound
	}
	return nil
}

func (r *Repository) UpdateExploitMetadata(ctx context.Context, exploitID string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	var metadataRaw []byte
	err := r.db.QueryRowContext(ctx, `SELECT metadata FROM exploits WHERE id = $1`, exploitID).Scan(&metadataRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrExploitNotFound
	}
	if err != nil {
		return err
	}
	meta, err := unmarshalMap(metadataRaw)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	for key, value := range updates {
		if value == nil {
			delete(meta, key)
		} else {
			meta[key] = value
		}
	}
	metadata, err := marshalJSON(meta)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE exploits
		SET metadata=$1, updated_at=$2
		WHERE id=$3`,
		metadata,
		time.Now().UTC(),
		exploitID,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return storage.ErrExploitNotFound
	}
	return nil
}

func (r *Repository) GetExploitRunScript(ctx context.Context, exploitID string) (string, error) {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return "", err
	}
	entry, err := r.exploitEntrypoint(ctx, exploitID)
	if err != nil {
		return "", err
	}
	path := r.runScriptPath(exploitID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r.ensureDefaultRunScript(exploitID, entry)
	}
	if err != nil {
		return "", fmt.Errorf("read run.sh: %w", err)
	}
	return string(data), nil
}

func (r *Repository) SaveExploitRunScript(ctx context.Context, exploitID string, script string) error {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return err
	}
	if err := r.writeRunScript(exploitID, script); err != nil {
		return err
	}
	return nil
}

func (r *Repository) ListExploitFiles(ctx context.Context, exploitID string) ([]storage.ExploitFile, error) {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return nil, err
	}
	entry, err := r.exploitEntrypoint(ctx, exploitID)
	if err != nil {
		return nil, err
	}
	if _, err := r.ensureDefaultRunScript(exploitID, entry); err != nil {
		return nil, err
	}
	return r.listExploitFiles(exploitID)
}

func (r *Repository) GetExploitFile(ctx context.Context, exploitID string, name string) ([]byte, error) {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return nil, err
	}
	if name == runScriptName {
		script, err := r.GetExploitRunScript(ctx, exploitID)
		if err != nil {
			return nil, err
		}
		return []byte(script), nil
	}
	data, err := r.readExploitFile(exploitID, name)
	if errors.Is(err, errFileNotFound) {
		return nil, errors.New("file not found")
	}
	return data, err
}

func (r *Repository) SaveExploitFile(ctx context.Context, exploitID string, name string, data []byte) error {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("file name required")
	}
	if name == runScriptName {
		return r.SaveExploitRunScript(ctx, exploitID, string(data))
	}
	if len(data) == 0 {
		return errors.New("file empty")
	}
	return r.writeExploitFile(exploitID, name, data)
}

func (r *Repository) DeleteExploitFile(ctx context.Context, exploitID string, name string) error {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return err
	}
	return r.deleteExploitFile(exploitID, name)
}

func (r *Repository) LatestExploitArtifact(ctx context.Context, exploitID string) ([]byte, error) {
	if err := r.ensureExploitExists(ctx, exploitID); err != nil {
		return nil, err
	}
	// Ensure run.sh exists before packaging
	if _, err := r.GetExploitRunScript(ctx, exploitID); err != nil {
		return nil, err
	}
	archive, err := r.packageExploitDir(exploitID)
	if err != nil {
		return nil, err
	}
	return archive, nil
}

func (r *Repository) ListFlags(ctx context.Context) ([]domain.Flag, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, value, challenge_id, target_id, run_id, status, submission_count, last_submitted_at, created_at
		FROM flags
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flags []domain.Flag
	for rows.Next() {
		var flag domain.Flag
		var lastSubmitted sql.NullTime
		var challengeID, targetID, runID sql.NullString
		var status string
		if err := rows.Scan(
			&flag.ID,
			&flag.Value,
			&challengeID,
			&targetID,
			&runID,
			&status,
			&flag.SubmissionCount,
			&lastSubmitted,
			&flag.CreatedAt,
		); err != nil {
			return nil, err
		}
		if challengeID.Valid {
			flag.ChallengeID = challengeID.String
		}
		if targetID.Valid {
			flag.TargetID = targetID.String
		}
		if runID.Valid {
			flag.RunID = runID.String
		}
		flag.Status = domain.FlagStatus(status)
		if lastSubmitted.Valid {
			flag.LastSubmittedAt = lastSubmitted.Time
		}
		flags = append(flags, flag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return flags, nil
}

func (r *Repository) GetFlag(ctx context.Context, id string) (domain.Flag, error) {
	var flag domain.Flag
	var lastSubmitted sql.NullTime
	var challengeID, targetID, runID sql.NullString
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, value, challenge_id, target_id, run_id, status, submission_count, last_submitted_at, created_at
		FROM flags
		WHERE id = $1`,
		id,
	).Scan(
		&flag.ID,
		&flag.Value,
		&challengeID,
		&targetID,
		&runID,
		&status,
		&flag.SubmissionCount,
		&lastSubmitted,
		&flag.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Flag{}, errors.New("flag not found")
	}
	if err != nil {
		return domain.Flag{}, err
	}
	if challengeID.Valid {
		flag.ChallengeID = challengeID.String
	}
	if targetID.Valid {
		flag.TargetID = targetID.String
	}
	if runID.Valid {
		flag.RunID = runID.String
	}
	flag.Status = domain.FlagStatus(status)
	if lastSubmitted.Valid {
		flag.LastSubmittedAt = lastSubmitted.Time
	}
	return flag, nil
}

func (r *Repository) GetFlagLogs(ctx context.Context, id string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT message
		FROM flag_logs
		WHERE flag_id = $1
		ORDER BY created_at ASC`,
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return nil, err
		}
		logs = append(logs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (r *Repository) DashboardStats(ctx context.Context) (domain.DashboardStats, error) {
	stats := domain.DashboardStats{
		FlagStatusCount: make(map[domain.FlagStatus]int),
	}

	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM exploits`).Scan(&stats.ExploitCount); err != nil {
		return stats, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM targets`).Scan(&stats.TargetCount); err != nil {
		return stats, err
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM runs WHERE status IN ('pending', 'running')`).Scan(&stats.RunningRuns); err != nil {
		return stats, err
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM flags WHERE status <> 'accepted'`).Scan(&stats.PendingFlags); err != nil {
		return stats, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) 
		FROM flags
		GROUP BY status`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return stats, err
		}
		stats.FlagStatusCount[domain.FlagStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

func (r *Repository) OverviewMatrix(ctx context.Context, start, end time.Time) (domain.OverviewMatrix, error) {
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

	challengeRows, err := r.db.QueryContext(ctx, `SELECT id, name FROM challenges ORDER BY name ASC`)
	if err != nil {
		return matrix, err
	}
	defer challengeRows.Close()
	for challengeRows.Next() {
		var challenge domain.OverviewMatrixChallenge
		if err := challengeRows.Scan(&challenge.ID, &challenge.Name); err != nil {
			return matrix, err
		}
		matrix.Challenges = append(matrix.Challenges, challenge)
	}
	if err := challengeRows.Err(); err != nil {
		return matrix, err
	}

	targetRows, err := r.db.QueryContext(ctx, `SELECT id, challenge_id, team_name FROM targets`)
	if err != nil {
		return matrix, err
	}
	defer targetRows.Close()

	targetByChallenge := make(map[string]map[string]string)
	teamSet := make(map[string]struct{})
	for targetRows.Next() {
		var id, challengeID, teamName string
		if err := targetRows.Scan(&id, &challengeID, &teamName); err != nil {
			return matrix, err
		}
		teamName = strings.TrimSpace(teamName)
		if teamName == "" {
			continue
		}
		teamSet[teamName] = struct{}{}
		if targetByChallenge[challengeID] == nil {
			targetByChallenge[challengeID] = make(map[string]string)
		}
		if _, exists := targetByChallenge[challengeID][teamName]; !exists {
			targetByChallenge[challengeID][teamName] = id
		}
	}
	if err := targetRows.Err(); err != nil {
		return matrix, err
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

	rows, err := r.db.QueryContext(ctx, `
		SELECT r.target_id, r.status, e.name, COALESCE(f.status, '')
		FROM runs r
		JOIN exploits e ON e.id = r.exploit_id
		LEFT JOIN flags f ON f.run_id = r.id
		WHERE r.target_id IS NOT NULL AND (
			(COALESCE(r.finished_at, r.started_at, r.queued_at) BETWEEN $1 AND $2)
			OR (f.id IS NOT NULL AND COALESCE(f.last_submitted_at, f.created_at, r.finished_at, r.started_at, r.queued_at) BETWEEN $1 AND $2)
		)
	`, start, end)
	if err != nil {
		return matrix, err
	}
	defer rows.Close()

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

	for rows.Next() {
		var targetID, runStatus, exploitName, flagStatus string
		if err := rows.Scan(&targetID, &runStatus, &exploitName, &flagStatus); err != nil {
			return matrix, err
		}
		targetID = strings.TrimSpace(targetID)
		if targetID == "" {
			continue
		}
		runStatus = strings.ToLower(strings.TrimSpace(runStatus))
		flagStatus = strings.ToLower(strings.TrimSpace(flagStatus))

		acc := accum[targetID]
		if acc == nil {
			acc = &cellAccumulator{
				success: make(map[string]struct{}),
				failed:  make(map[string]struct{}),
			}
			accum[targetID] = acc
		}

		if runStatus == string(domain.RunStatusSucceeded) && flagStatus == string(domain.FlagStatusAccepted) {
			acc.success[exploitName] = struct{}{}
			continue
		}

		failure := false
		switch runStatus {
		case string(domain.RunStatusFailed), string(domain.RunStatusTimeout), string(domain.RunStatusCanceled):
			failure = true
		}
		if flagStatus != "" && flagStatus != string(domain.FlagStatusAccepted) {
			failure = true
		}
		if runStatus == string(domain.RunStatusSucceeded) && flagStatus == "" {
			failure = true
		}

		if failure {
			acc.failed[exploitName] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return matrix, err
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
	return matrix, nil
}

func (r *Repository) ListFlagAttempts(ctx context.Context, flagID string) ([]domain.SubmissionAttempt, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, flag_id, submitter_id, status_code, error, submitted_at, next_retry_at, payload
		FROM submission_attempts
		WHERE flag_id = $1
		ORDER BY submitted_at ASC`,
		flagID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []domain.SubmissionAttempt
	for rows.Next() {
		var attempt domain.SubmissionAttempt
		var nextRetry sql.NullTime
		var payload []byte
		var submitterID, errMsg sql.NullString
		if err := rows.Scan(
			&attempt.ID,
			&attempt.FlagID,
			&submitterID,
			&attempt.StatusCode,
			&errMsg,
			&attempt.SubmittedAt,
			&nextRetry,
			&payload,
		); err != nil {
			return nil, err
		}
		if submitterID.Valid {
			attempt.SubmitterID = submitterID.String
		}
		if errMsg.Valid {
			attempt.Error = errMsg.String
		}
		if nextRetry.Valid {
			attempt.NextRetryAt = nextRetry.Time
		}
		if len(payload) > 0 {
			data, err := unmarshalMap(payload)
			if err != nil {
				return nil, err
			}
			attempt.Payload = data
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attempts, nil
}

func (r *Repository) AddFlagAttempt(ctx context.Context, flagID string, attempt domain.SubmissionAttempt) error {
	if flagID == "" {
		return errors.New("flag id required")
	}
	if attempt.ID == "" {
		attempt.ID = generateID("attempt")
	}
	if attempt.SubmittedAt.IsZero() {
		attempt.SubmittedAt = time.Now().UTC()
	}
	attempt.FlagID = flagID

	payload, err := marshalJSON(attempt.Payload)
	if err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO submission_attempts (id, flag_id, submitter_id, status_code, error, submitted_at, next_retry_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		attempt.ID,
		flagID,
		attempt.SubmitterID,
		attempt.StatusCode,
		attempt.Error,
		attempt.SubmittedAt,
		nullTime(attempt.NextRetryAt),
		payload,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE flags
		SET submission_count = submission_count + 1
		WHERE id = $1`,
		flagID,
	); err != nil {
		return err
	}

	logLine := fmt.Sprintf("%s status=%d %s", attempt.SubmittedAt.Format(time.RFC3339), attempt.StatusCode, attempt.Error)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO flag_logs (flag_id, message, created_at)
		VALUES ($1, $2, $3)`,
		flagID,
		logLine,
		attempt.SubmittedAt,
	); err != nil {
		return err
	}

	return tx.Commit()
}

const runOrderingExpression = "COALESCE(queued_at, started_at, finished_at, TIMESTAMP '1970-01-01')"

func buildRunFilterClause(filter domain.RunFilter, startIdx int) (string, []any) {
	clauses := make([]string, 0, 6)
	args := make([]any, 0, 6)

	addEquality := func(value string, column string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		position := startIdx + len(args)
		clauses = append(clauses, fmt.Sprintf("runs.%s = $%d", column, position))
		args = append(args, value)
	}

	addLike := func(value string, clause string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		position := startIdx + len(args)
		clauses = append(clauses, fmt.Sprintf(clause, position))
		args = append(args, "%"+strings.ToLower(value)+"%")
	}

	addEquality(filter.Status, "status")
	addEquality(filter.ExploitID, "exploit_id")
	addEquality(filter.TargetID, "target_id")
	addLike(filter.ExploitName, "runs.exploit_id IN (SELECT id FROM exploits WHERE LOWER(name) LIKE $%d)")
	addLike(filter.TargetName, "runs.target_id IN (SELECT id FROM targets WHERE LOWER(team_name) LIKE $%d)")

	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func (r *Repository) ListRuns(ctx context.Context, filter domain.RunFilter, limit int, cursor string) ([]domain.Run, string, error) {
	if limit <= 0 {
		limit = 50
	}
	queryLimit := limit + 1
	var rows *sql.Rows
	var err error
	if cursor != "" {
		anchor, anchorErr := r.GetRun(ctx, cursor)
		if anchorErr != nil {
			cursor = ""
		} else {
			anchorTime := runOrderingTime(anchor)
			args := []any{
				anchorTime,
				cursor,
			}
			filterClause, filterArgs := buildRunFilterClause(filter, len(args)+1)
			args = append(args, filterArgs...)
			args = append(args, queryLimit)
			limitPos := len(args)
			query := fmt.Sprintf(`
				SELECT id, exploit_id, exploit_revision, target_id, runner_id, status, queued_at, started_at, finished_at, log_uri, flag_id, error_message
				FROM runs
				WHERE ((%s) < $1 OR ((%s) = $1 AND id < $2))%s
				ORDER BY %s DESC, id DESC
				LIMIT $%d`,
				runOrderingExpression,
				runOrderingExpression,
				filterClause,
				runOrderingExpression,
				limitPos,
			)
			rows, err = r.db.QueryContext(ctx, query, args...)
		}
	}
	if cursor == "" && rows == nil {
		args := []any{}
		filterClause, filterArgs := buildRunFilterClause(filter, len(args)+1)
		args = append(args, filterArgs...)
		args = append(args, queryLimit)
		limitPos := len(args)
		query := fmt.Sprintf(`
			SELECT id, exploit_id, exploit_revision, target_id, runner_id, status, queued_at, started_at, finished_at, log_uri, flag_id, error_message
			FROM runs
			WHERE 1=1%s
			ORDER BY %s DESC, id DESC
			LIMIT $%d`,
			filterClause,
			runOrderingExpression,
			limitPos,
		)
		rows, err = r.db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var runs []domain.Run
	for rows.Next() {
		var run domain.Run
		var queued, started, finished sql.NullTime
		var exploitID, exploitRevision, targetID, runnerID, logURI, flagID, errorMessage sql.NullString
		var status string
		if err := rows.Scan(
			&run.ID,
			&exploitID,
			&exploitRevision,
			&targetID,
			&runnerID,
			&status,
			&queued,
			&started,
			&finished,
			&logURI,
			&flagID,
			&errorMessage,
		); err != nil {
			return nil, "", err
		}
		if exploitID.Valid {
			run.ExploitID = exploitID.String
		}
		if exploitRevision.Valid {
			run.ExploitRevision = exploitRevision.String
		}
		if targetID.Valid {
			run.TargetID = targetID.String
		}
		if runnerID.Valid {
			run.RunnerID = runnerID.String
		}
		if logURI.Valid {
			run.LogURI = logURI.String
		}
		if flagID.Valid {
			run.FlagID = flagID.String
		}
		if errorMessage.Valid {
			run.ErrorMessage = errorMessage.String
		}
		run.Status = domain.RunStatus(status)
		if queued.Valid {
			run.QueuedAt = queued.Time
		}
		if started.Valid {
			run.StartedAt = started.Time
		}
		if finished.Valid {
			run.FinishedAt = finished.Time
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var nextCursor string
	if len(runs) > limit {
		nextCursor = runs[limit-1].ID
		runs = runs[:limit]
	}
	return runs, nextCursor, nil
}

func (r *Repository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	var run domain.Run
	var queued, started, finished sql.NullTime
	var exploitID, exploitRevision, targetID, runnerID, logURI, flagID, errorMessage sql.NullString
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, exploit_id, exploit_revision, target_id, runner_id, status, queued_at, started_at, finished_at, log_uri, flag_id, error_message
		FROM runs
		WHERE id = $1`,
		id,
	).Scan(
		&run.ID,
		&exploitID,
		&exploitRevision,
		&targetID,
		&runnerID,
		&status,
		&queued,
		&started,
		&finished,
		&logURI,
		&flagID,
		&errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, errors.New("run not found")
	}
	if err != nil {
		return domain.Run{}, err
	}
	if exploitID.Valid {
		run.ExploitID = exploitID.String
	}
	if exploitRevision.Valid {
		run.ExploitRevision = exploitRevision.String
	}
	if targetID.Valid {
		run.TargetID = targetID.String
	}
	if runnerID.Valid {
		run.RunnerID = runnerID.String
	}
	if logURI.Valid {
		run.LogURI = logURI.String
	}
	if flagID.Valid {
		run.FlagID = flagID.String
	}
	if errorMessage.Valid {
		run.ErrorMessage = errorMessage.String
	}
	run.Status = domain.RunStatus(status)
	if queued.Valid {
		run.QueuedAt = queued.Time
	}
	if started.Valid {
		run.StartedAt = started.Time
	}
	if finished.Valid {
		run.FinishedAt = finished.Time
	}
	return run, nil
}

func (r *Repository) ListRunnerActiveRuns(ctx context.Context, runnerID string) ([]domain.Run, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, exploit_id, exploit_revision, target_id, runner_id, status, queued_at, started_at, finished_at, log_uri, flag_id, error_message
		FROM runs
		WHERE runner_id = $1 AND status = 'running'`,
		runnerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []domain.Run
	for rows.Next() {
		var run domain.Run
		var exploitID, exploitRevision, targetID, runner, logURI, flagID, errorMessage sql.NullString
		var queued, started, finished sql.NullTime
		var status string
		if scanErr := rows.Scan(
			&run.ID,
			&exploitID,
			&exploitRevision,
			&targetID,
			&runner,
			&status,
			&queued,
			&started,
			&finished,
			&logURI,
			&flagID,
			&errorMessage,
		); scanErr != nil {
			return nil, scanErr
		}
		if exploitID.Valid {
			run.ExploitID = exploitID.String
		}
		if exploitRevision.Valid {
			run.ExploitRevision = exploitRevision.String
		}
		if targetID.Valid {
			run.TargetID = targetID.String
		}
		if runner.Valid {
			run.RunnerID = runner.String
		}
		if logURI.Valid {
			run.LogURI = logURI.String
		}
		if flagID.Valid {
			run.FlagID = flagID.String
		}
		if errorMessage.Valid {
			run.ErrorMessage = errorMessage.String
		}
		if queued.Valid {
			run.QueuedAt = queued.Time
		}
		if started.Valid {
			run.StartedAt = started.Time
		}
		if finished.Valid {
			run.FinishedAt = finished.Time
		}
		run.Status = domain.RunStatus(status)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *Repository) ListLogs(ctx context.Context, filter domain.LogFilter) ([]domain.LogEntry, error) {
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 5)
	argPos := 1

	if filter.Level != "" {
		conditions = append(conditions, fmt.Sprintf("level = $%d", argPos))
		args = append(args, filter.Level)
		argPos++
	}
	if filter.Source != "" {
		conditions = append(conditions, fmt.Sprintf("source = $%d", argPos))
		args = append(args, filter.Source)
		argPos++
	}
	if filter.RunID != "" {
		conditions = append(conditions, fmt.Sprintf("run_id = $%d", argPos))
		args = append(args, filter.RunID)
		argPos++
	}
	if filter.FlagID != "" {
		conditions = append(conditions, fmt.Sprintf("flag_id = $%d", argPos))
		args = append(args, filter.FlagID)
		argPos++
	}

	query := `
		SELECT id, timestamp, level, source, message, run_id, flag_id, fields
		FROM log_entries`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY timestamp DESC"

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []domain.LogEntry
	for rows.Next() {
		var entry domain.LogEntry
		var fields []byte
		var runID, flagID sql.NullString
		if err := rows.Scan(
			&entry.ID,
			&entry.Timestamp,
			&entry.Level,
			&entry.Source,
			&entry.Message,
			&runID,
			&flagID,
			&fields,
		); err != nil {
			return nil, err
		}
		if runID.Valid {
			entry.RunID = runID.String
		}
		if flagID.Valid {
			entry.FlagID = flagID.String
		}
		if len(fields) > 0 {
			var data map[string]any
			if err := json.Unmarshal(fields, &data); err != nil {
				return nil, err
			}
			entry.Fields = data
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(entries)
	return entries, nil
}

func (r *Repository) AppendLog(ctx context.Context, entry domain.LogEntry) error {
	if entry.ID == "" {
		entry.ID = generateID("log")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	fields, err := marshalJSON(entry.Fields)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO log_entries (id, timestamp, level, source, message, run_id, flag_id, fields)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		entry.ID,
		entry.Timestamp,
		entry.Level,
		entry.Source,
		entry.Message,
		nullString(entry.RunID),
		nullString(entry.FlagID),
		fields,
	)
	return err
}

func (r *Repository) ListRunners(ctx context.Context) ([]domain.Runner, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, hostname, labels, tags, max_parallel, active_jobs, last_heartbeat, status, registered_at, config, last_ip
		FROM runners
		ORDER BY hostname ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runners []domain.Runner
	for rows.Next() {
		var runner domain.Runner
		var labels, tags, configData []byte
		var activeJobs sql.NullInt64
		var lastHeartbeat sql.NullTime
		var typ, hostname, status, lastIP sql.NullString
		if err := rows.Scan(
			&runner.ID,
			&typ,
			&hostname,
			&labels,
			&tags,
			&runner.MaxParallel,
			&activeJobs,
			&lastHeartbeat,
			&status,
			&runner.RegisteredAt,
			&configData,
			&lastIP,
		); err != nil {
			return nil, err
		}
		if typ.Valid {
			runner.Type = typ.String
		}
		if hostname.Valid {
			runner.Hostname = hostname.String
		}
		if lastHeartbeat.Valid {
			runner.LastHeartbeat = lastHeartbeat.Time
		}
		if status.Valid {
			runner.Status = status.String
		}
		if len(labels) > 0 {
			data, err := unmarshalStringMap(labels)
			if err != nil {
				return nil, err
			}
			runner.Labels = data
		}
		if len(tags) > 0 {
			list, err := unmarshalStringSlice(tags)
			if err != nil {
				return nil, err
			}
			runner.Tags = list
		}
		if len(configData) > 0 {
			cfg, err := unmarshalStringMap(configData)
			if err != nil {
				return nil, err
			}
			runner.Config = cfg
		}
		if activeJobs.Valid {
			runner.ActiveJobs = int(activeJobs.Int64)
		}
		if lastIP.Valid {
			runner.LastIP = lastIP.String
		}
		runners = append(runners, runner)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runners, nil
}

func (r *Repository) GetRunner(ctx context.Context, id string) (domain.Runner, error) {
	var runner domain.Runner
	var labels, tags, configData []byte
	var typ, hostname, status, lastIP sql.NullString
	var lastHeartbeat sql.NullTime
	var activeJobs sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT id, type, hostname, labels, tags, max_parallel, active_jobs, last_heartbeat, status, registered_at, config, last_ip
		FROM runners
		WHERE id = $1`, id,
	).Scan(
		&runner.ID,
		&typ,
		&hostname,
		&labels,
		&tags,
		&runner.MaxParallel,
		&activeJobs,
		&lastHeartbeat,
		&status,
		&runner.RegisteredAt,
		&configData,
		&lastIP,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Runner{}, errors.New("runner not found")
	}
	if err != nil {
		return domain.Runner{}, err
	}
	if typ.Valid {
		runner.Type = typ.String
	}
	if hostname.Valid {
		runner.Hostname = hostname.String
	}
	if len(labels) > 0 {
		data, err := unmarshalStringMap(labels)
		if err != nil {
			return domain.Runner{}, err
		}
		runner.Labels = data
	}
	if len(tags) > 0 {
		list, err := unmarshalStringSlice(tags)
		if err != nil {
			return domain.Runner{}, err
		}
		runner.Tags = list
	}
	if len(configData) > 0 {
		cfg, err := unmarshalStringMap(configData)
		if err != nil {
			return domain.Runner{}, err
		}
		runner.Config = cfg
	}
	if activeJobs.Valid {
		runner.ActiveJobs = int(activeJobs.Int64)
	}
	if lastHeartbeat.Valid {
		runner.LastHeartbeat = lastHeartbeat.Time
	}
	if status.Valid {
		runner.Status = status.String
	}
	if lastIP.Valid {
		runner.LastIP = lastIP.String
	}
	return runner, nil
}

func (r *Repository) UpsertRunner(ctx context.Context, runner domain.Runner) error {
	if runner.ID == "" {
		runner.ID = generateID("runner")
	}
	if runner.RegisteredAt.IsZero() {
		runner.RegisteredAt = time.Now().UTC()
	}
	labels, err := marshalJSON(runner.Labels)
	if err != nil {
		return err
	}
	tags, err := marshalJSON(runner.Tags)
	if err != nil {
		return err
	}
	configData, err := marshalJSON(runner.Config)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO runners (id, type, hostname, labels, tags, max_parallel, active_jobs, last_heartbeat, status, registered_at, config, last_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE
		SET type = EXCLUDED.type,
		    hostname = EXCLUDED.hostname,
		    labels = EXCLUDED.labels,
		    tags = EXCLUDED.tags,
		    max_parallel = EXCLUDED.max_parallel,
		    active_jobs = EXCLUDED.active_jobs,
		    last_heartbeat = EXCLUDED.last_heartbeat,
		    status = EXCLUDED.status,
		    registered_at = EXCLUDED.registered_at,
		    config = EXCLUDED.config,
		    last_ip = EXCLUDED.last_ip`,
		runner.ID,
		runner.Type,
		runner.Hostname,
		labels,
		tags,
		runner.MaxParallel,
		runner.ActiveJobs,
		nullTime(runner.LastHeartbeat),
		runner.Status,
		runner.RegisteredAt,
		configData,
		nullString(runner.LastIP),
	)
	return err
}

func (r *Repository) DeleteRunner(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM runners WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("runner not found")
	}
	return nil
}

func (r *Repository) ListSubmitters(ctx context.Context) ([]domain.Submitter, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, type, hostname, status, concurrency, in_flight, pending, recent_throughput,
		       avg_latency_ms, pressure, window_seconds, latency_sum_seconds, processed_count,
		       labels, config, last_heartbeat, registered_at
		FROM submitters
		ORDER BY hostname ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var submitters []domain.Submitter
	for rows.Next() {
		var item domain.Submitter
		var typ, hostname, status sql.NullString
		var labelsData, configData []byte
		var lastHeartbeat sql.NullTime
		if err := rows.Scan(
			&item.ID,
			&typ,
			&hostname,
			&status,
			&item.Concurrency,
			&item.InFlight,
			&item.Pending,
			&item.RecentThroughput,
			&item.AverageLatencyMS,
			&item.Pressure,
			&item.WindowSeconds,
			&item.LatencySumSeconds,
			&item.ProcessedCount,
			&labelsData,
			&configData,
			&lastHeartbeat,
			&item.RegisteredAt,
		); err != nil {
			return nil, err
		}
		if typ.Valid {
			item.Type = typ.String
		}
		if hostname.Valid {
			item.Hostname = hostname.String
		}
		if status.Valid {
			item.Status = status.String
		}
		if lastHeartbeat.Valid {
			item.LastHeartbeat = lastHeartbeat.Time
		}
		if len(labelsData) > 0 {
			data, err := unmarshalStringMap(labelsData)
			if err != nil {
				return nil, err
			}
			item.Labels = data
		}
		if len(configData) > 0 {
			data, err := unmarshalStringMap(configData)
			if err != nil {
				return nil, err
			}
			item.Config = data
		}
		submitters = append(submitters, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return submitters, nil
}

func (r *Repository) GetSubmitter(ctx context.Context, id string) (domain.Submitter, error) {
	var item domain.Submitter
	var typ, hostname, status sql.NullString
	var labelsData, configData []byte
	var lastHeartbeat sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, type, hostname, status, concurrency, in_flight, pending, recent_throughput,
		       avg_latency_ms, pressure, window_seconds, latency_sum_seconds, processed_count,
		       labels, config, last_heartbeat, registered_at
		FROM submitters
		WHERE id = $1`, id,
	).Scan(
		&item.ID,
		&typ,
		&hostname,
		&status,
		&item.Concurrency,
		&item.InFlight,
		&item.Pending,
		&item.RecentThroughput,
		&item.AverageLatencyMS,
		&item.Pressure,
		&item.WindowSeconds,
		&item.LatencySumSeconds,
		&item.ProcessedCount,
		&labelsData,
		&configData,
		&lastHeartbeat,
		&item.RegisteredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Submitter{}, errors.New("submitter not found")
	}
	if err != nil {
		return domain.Submitter{}, err
	}
	if typ.Valid {
		item.Type = typ.String
	}
	if hostname.Valid {
		item.Hostname = hostname.String
	}
	if status.Valid {
		item.Status = status.String
	}
	if lastHeartbeat.Valid {
		item.LastHeartbeat = lastHeartbeat.Time
	}
	if len(labelsData) > 0 {
		data, err := unmarshalStringMap(labelsData)
		if err != nil {
			return domain.Submitter{}, err
		}
		item.Labels = data
	}
	if len(configData) > 0 {
		data, err := unmarshalStringMap(configData)
		if err != nil {
			return domain.Submitter{}, err
		}
		item.Config = data
	}
	return item, nil
}

func (r *Repository) UpsertSubmitter(ctx context.Context, submitter domain.Submitter) error {
	if strings.TrimSpace(submitter.ID) == "" {
		return errors.New("submitter id required")
	}
	labels, err := marshalJSON(submitter.Labels)
	if err != nil {
		return err
	}
	configData, err := marshalJSON(submitter.Config)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO submitters (id, type, hostname, status, concurrency, in_flight, pending, recent_throughput,
		                        avg_latency_ms, pressure, window_seconds, latency_sum_seconds, processed_count,
		                        labels, config, last_heartbeat, registered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO UPDATE
		SET type = EXCLUDED.type,
		    hostname = EXCLUDED.hostname,
		    status = EXCLUDED.status,
		    concurrency = EXCLUDED.concurrency,
		    in_flight = EXCLUDED.in_flight,
		    pending = EXCLUDED.pending,
		    recent_throughput = EXCLUDED.recent_throughput,
		    avg_latency_ms = EXCLUDED.avg_latency_ms,
		    pressure = EXCLUDED.pressure,
		    window_seconds = EXCLUDED.window_seconds,
		    latency_sum_seconds = EXCLUDED.latency_sum_seconds,
		    processed_count = EXCLUDED.processed_count,
		    labels = EXCLUDED.labels,
		    config = EXCLUDED.config,
		    last_heartbeat = EXCLUDED.last_heartbeat,
		    registered_at = EXCLUDED.registered_at`,
		submitter.ID,
		nullString(submitter.Type),
		nullString(submitter.Hostname),
		nullString(submitter.Status),
		submitter.Concurrency,
		submitter.InFlight,
		submitter.Pending,
		submitter.RecentThroughput,
		submitter.AverageLatencyMS,
		submitter.Pressure,
		submitter.WindowSeconds,
		submitter.LatencySumSeconds,
		submitter.ProcessedCount,
		labels,
		configData,
		nullTime(submitter.LastHeartbeat),
		submitter.RegisteredAt,
	)
	return err
}

func (r *Repository) DeleteSubmitter(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("submitter id required")
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM submitters WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("submitter not found")
	}
	return nil
}

func (r *Repository) ensureExploitExists(ctx context.Context, exploitID string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM exploits WHERE id = $1)`, exploitID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return storage.ErrExploitNotFound
	}
	return nil
}

func (r *Repository) exploitEntrypoint(ctx context.Context, exploitID string) (string, error) {
	meta, err := r.fetchExploitMetadata(ctx, exploitID)
	if err != nil {
		return "", err
	}
	if meta == nil {
		return "", nil
	}
	if entry, ok := meta["entrypoint"].(string); ok {
		return entry, nil
	}
	return "", nil
}

func (r *Repository) fetchExploitMetadata(ctx context.Context, exploitID string) (map[string]any, error) {
	var metadataRaw []byte
	err := r.db.QueryRowContext(ctx, `SELECT metadata FROM exploits WHERE id = $1`, exploitID).Scan(&metadataRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrExploitNotFound
	}
	if err != nil {
		return nil, err
	}
	meta, err := unmarshalMap(metadataRaw)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

func runOrderingTime(run domain.Run) time.Time {
	if !run.QueuedAt.IsZero() {
		return run.QueuedAt
	}
	if !run.StartedAt.IsZero() {
		return run.StartedAt
	}
	if !run.FinishedAt.IsZero() {
		return run.FinishedAt
	}
	return time.Unix(0, 0).UTC()
}
