package scheduler

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/storage"
)

// Scheduler coordinates exploit dispatch respecting concurrency rules.
type Scheduler struct {
	cfg    config.Config
	log    *slog.Logger
	repo   storage.Repository
	broker queue.Broker
}

// New returns a configured scheduler.
func New(cfg config.Config, log *slog.Logger, repo storage.Repository, broker queue.Broker) *Scheduler {
	return &Scheduler{
		cfg:    cfg,
		log:    log,
		repo:   repo,
		broker: broker,
	}
}

// Run launches the scheduling loop until the provided context is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	baseInterval := time.Second
	if s.cfg.Scheduler.PollInterval > 0 && s.cfg.Scheduler.PollInterval < baseInterval {
		baseInterval = s.cfg.Scheduler.PollInterval
	}
	ticker := time.NewTicker(baseInterval)
	defer ticker.Stop()

	var lastRound time.Time

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping")
			return ctx.Err()
		case <-ticker.C:
			settings, err := s.repo.GetSchedulerSettings(ctx)
			if err != nil {
				s.log.Warn("load scheduler settings failed, falling back to config", slog.Any("err", err))
				settings.RoundInterval = s.cfg.Scheduler.PollInterval
				settings.MaxDispatchBatch = s.cfg.Scheduler.MaxDispatchBatch
				settings.Enabled = true
			}
			if !settings.Enabled {
				s.log.Debug("scheduler paused")
				continue
			}
			interval := settings.RoundInterval
			if interval <= 0 {
				interval = s.cfg.Scheduler.PollInterval
			}
			if interval <= 0 {
				interval = time.Second * 5
			}
			maxBatch := settings.MaxDispatchBatch
			if maxBatch <= 0 {
				maxBatch = s.cfg.Scheduler.MaxDispatchBatch
			}
			if lastRound.IsZero() || time.Since(lastRound) >= interval {
				if err := s.tick(ctx, maxBatch); err != nil {
					s.log.Error("scheduler tick failed", slog.Any("err", err))
				} else {
					lastRound = time.Now()
				}
			}
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, limit int) error {
	workItems, err := s.repo.RunnableWork(ctx, limit)
	if err != nil {
		return err
	}
	if len(workItems) == 0 {
		s.log.Debug("no runnable work items")
		return nil
	}

	runners, err := s.repo.ListRunners(ctx)
	if err != nil {
		s.log.Warn("failed to load runners for tag matching", slog.Any("err", err))
		runners = nil
	}

	dispatched := 0
	for _, item := range workItems {
		if !supportsRunnerTags(runners, item.Exploit.RunnerTags) {
			if len(item.Exploit.RunnerTags) > 0 {
				s.log.Debug("no matching runner tags",
					slog.String("run_id", item.RunID),
					slog.String("exploit_id", item.Exploit.ID),
					slog.Any("required_tags", item.Exploit.RunnerTags))
			}
			continue
		}
		if err := s.enqueueRun(ctx, item); err != nil {
			s.log.Warn("failed to enqueue run",
				slog.String("run_id", item.RunID),
				slog.String("exploit_id", item.Exploit.ID),
				slog.String("target_id", item.Target.ID),
				slog.Any("err", err))
			continue
		}
		dispatched++
	}

	if dispatched > 0 {
		s.log.Info("scheduled runs", slog.Int("count", dispatched), slog.Int("limit", limit))
	}
	return nil
}

func (s *Scheduler) enqueueRun(ctx context.Context, item domain.RunWorkItem) error {
	if len(item.Artifact) == 0 {
		return fmt.Errorf("empty artifact for run %s", item.RunID)
	}

	payload := jobs.JobPayload{
		RunID:       item.RunID,
		ExploitID:   item.Exploit.ID,
		ExploitName: item.Exploit.Name,
		TargetID:    item.Target.ID,
		TargetIP:    item.Target.IP,
		TargetPort:  item.Target.Port,
		FlagRegex:   item.FlagRegex,
		FlagFile:    item.FlagFile,
	}
	if item.Exploit.Metadata != nil {
		if script, ok := item.Exploit.Metadata["run_script"].(string); ok {
			payload.RunScript = script
		}
		if entry, ok := item.Exploit.Metadata["entrypoint"].(string); ok {
			payload.Entrypoint = entry
		}
	}
	if item.Timeout > 0 {
		payload.TimeoutSeconds = int(item.Timeout.Seconds())
	}
	if len(item.Exploit.RunnerTags) > 0 {
		payload.RunnerTags = item.Exploit.RunnerTags
	}
	sum := md5.Sum(item.Artifact)
	payload.ArtifactMD5 = hex.EncodeToString(sum[:])
	payload.ArtifactURI = fmt.Sprintf("/api/v1/exploits/%s/artifact", item.Exploit.ID)

	body, err := jobs.MarshalJobPayload(payload)
	if err != nil {
		return err
	}

	_, err = s.broker.Publish(ctx, s.cfg.RunQueue.Name, body)
	return err
}

func supportsRunnerTags(runners []domain.Runner, required []string) bool {
	if len(required) == 0 {
		return true
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, tag := range required {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value != "" {
			requiredSet[value] = struct{}{}
		}
	}
	if len(requiredSet) == 0 {
		return true
	}
	for _, runner := range runners {
		if len(runner.Tags) == 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(runner.Status)) != "online" {
			continue
		}
		tagSet := make(map[string]struct{}, len(runner.Tags))
		for _, tag := range runner.Tags {
			value := strings.ToLower(strings.TrimSpace(tag))
			if value != "" {
				tagSet[value] = struct{}{}
			}
		}
		match := true
		for tag := range requiredSet {
			if _, ok := tagSet[tag]; !ok {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
