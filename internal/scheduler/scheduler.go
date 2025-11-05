package scheduler

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
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
	ticker := time.NewTicker(s.cfg.Scheduler.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := s.tick(ctx); err != nil {
				s.log.Error("scheduler tick failed", slog.Any("err", err))
			}
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) error {
	workItems, err := s.repo.RunnableWork(ctx, s.cfg.Scheduler.MaxDispatchBatch)
	if err != nil {
		return err
	}
	if len(workItems) == 0 {
		s.log.Debug("no runnable work items")
		return nil
	}

	dispatched := 0
	for _, item := range workItems {
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
		s.log.Info("scheduled runs", slog.Int("count", dispatched))
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
	payload.ArtifactBase64 = base64.StdEncoding.EncodeToString(item.Artifact)

	body, err := jobs.MarshalJobPayload(payload)
	if err != nil {
		return err
	}

	_, err = s.broker.Publish(ctx, s.cfg.RunQueue.Name, body)
	return err
}
