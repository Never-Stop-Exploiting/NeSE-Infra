package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/events"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/scheduler"
	"awdinfra/internal/storage"
)

// Dispatcher consumes the primary run queue and routes jobs to runner-specific queues.
type Dispatcher struct {
	cfg    config.Config
	log    *slog.Logger
	broker queue.Broker
	repo   storage.Repository

	cacheMu       sync.RWMutex
	cacheRunners  []domain.Runner
	cacheExpiry   time.Time
	eventStream   string
	eventGroup    string
	eventConsumer string
}

func New(cfg config.Config, log *slog.Logger, broker queue.Broker, repo storage.Repository) *Dispatcher {
	eventStream := strings.TrimSpace(cfg.RunnerEventStream)
	if eventStream == "" {
		eventStream = "runner:events"
	}
	eventConsumer := cfg.Dispatcher.ID
	if strings.TrimSpace(eventConsumer) == "" {
		eventConsumer = "dispatcher"
	}
	return &Dispatcher{
		cfg:           cfg,
		log:           log,
		broker:        broker,
		repo:          repo,
		eventStream:   eventStream,
		eventGroup:    "dispatcher-events",
		eventConsumer: eventConsumer + "-events",
	}
}

// Run starts the routing loop until the context is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	if strings.TrimSpace(d.eventStream) != "" {
		go d.runEventLoop(ctx)
	}
	group := "dispatcher"
	consumer := strings.TrimSpace(d.cfg.Dispatcher.ID)
	if consumer == "" {
		consumer = "dispatcher"
	}
	block := d.cfg.Dispatcher.BlockTimeout
	if block <= 0 {
		block = 5 * time.Second
	}
	batch := d.cfg.Dispatcher.MaxBatch
	if batch <= 0 {
		batch = 32
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		messages, err := d.broker.Claim(ctx, d.cfg.RunQueue.Name, group, consumer, batch, block)
		if err != nil {
			if errors.Is(err, queue.ErrNoMessages) {
				continue
			}
			d.log.Error("dispatcher claim failed", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}
		for _, message := range messages {
			if err := d.handleMessage(ctx, group, message); err != nil {
				d.log.Warn("dispatch message failed",
					slog.Any("err", err),
					slog.String("message_id", message.ID))
			}
		}
	}
}

func (d *Dispatcher) handleMessage(ctx context.Context, group string, message queue.Message) error {
	payload, err := jobs.DecodeJobPayload(message.Body)
	if err != nil {
		d.log.Warn("discarding malformed job", slog.String("message_id", message.ID), slog.Any("err", err))
		return d.broker.Ack(ctx, d.cfg.RunQueue.Name, group, message.ID)
	}
	runner, ok, err := d.pickRunner(ctx, payload.RunnerTags)
	if err != nil {
		return d.retryLater(ctx, group, message.ID, err)
	}
	if !ok {
		// No runner currently matches; requeue for later.
		d.log.Warn("no runner available for job",
			slog.String("run_id", payload.RunID),
			slog.Any("required_tags", payload.RunnerTags))
		return d.retryLater(ctx, group, message.ID, errors.New("no runner available"))
	}
	queueName := queue.RunnerQueueName(d.cfg.RunQueue.RunnerPrefix, runner.ID, runner.InstanceID)
	if _, err := d.broker.Publish(ctx, queueName, message.Body); err != nil {
		return d.retryLater(ctx, group, message.ID, err)
	}
	if err := d.broker.Ack(ctx, d.cfg.RunQueue.Name, group, message.ID); err != nil {
		return err
	}
	if err := d.repo.AssignRunToRunner(ctx, payload.RunID, runner.ID, runner.InstanceID); err != nil {
		d.log.Warn("record run assignment failed",
			slog.Any("err", err),
			slog.String("run_id", payload.RunID),
			slog.String("runner_id", runner.ID),
			slog.String("instance_id", runner.InstanceID))
	}
	d.log.Debug("dispatched job",
		slog.String("run_id", payload.RunID),
		slog.String("runner_id", runner.ID),
		slog.String("instance_id", runner.InstanceID),
		slog.String("queue", queueName))
	return nil
}

func (d *Dispatcher) retryLater(ctx context.Context, group string, messageID string, reason error) error {
	delay := d.cfg.RunQueue.RetryDelay
	if delay < 0 {
		delay = 0
	}
	return d.broker.Nack(ctx, d.cfg.RunQueue.Name, group, messageID, delay)
}

func (d *Dispatcher) handleRunnerEvent(ctx context.Context, evt events.RunnerEvent) {
	if strings.TrimSpace(evt.RunnerID) == "" {
		return
	}
	d.invalidateRunnerCache()
	switch evt.Type {
	case events.RunnerEventTypeStatus:
		status := strings.ToLower(strings.TrimSpace(evt.Status))
		immediate := evt.ForceDrain
		if status == "offline" || status == "restoring" {
			immediate = true
		}
		if immediate {
			go d.drainRunnerStream(context.Background(), evt.RunnerID, evt.InstanceID, evt.Reason, immediate)
		}
	case events.RunnerEventTypeInstance:
		if strings.TrimSpace(evt.InstanceID) != "" {
			immediate := evt.ForceDrain
			if !immediate {
				immediate = true
			}
			go d.drainRunnerStream(context.Background(), evt.RunnerID, evt.InstanceID, evt.Reason, immediate)
		}
	}
}

func (d *Dispatcher) drainRunnerStream(ctx context.Context, runnerID string, instanceID string, reason string, immediate bool) {
	if d.broker == nil {
		return
	}
	cleanID := strings.TrimSpace(runnerID)
	cleanInstance := strings.TrimSpace(instanceID)
	if cleanID == "" || cleanInstance == "" {
		return
	}
	runner, err := d.repo.GetRunner(ctx, cleanID)
	runnerType := runner.Type
	if err != nil {
		runnerType = ""
	}
	stream := queue.RunnerQueueName(d.cfg.RunQueue.RunnerPrefix, cleanID, cleanInstance)
	start := time.Now()
	d.log.Info("draining runner stream", slog.String("runner_id", cleanID), slog.String("instance_id", cleanInstance), slog.String("stream", stream), slog.String("reason", reason))
	group := runnerGroupName(runnerType)
	visibility := d.cfg.RunQueue.Visibility
	if visibility <= 0 {
		visibility = 30 * time.Second
	}
	processed := 0
	attempt := 0
	minIdle := visibility
	if immediate {
		minIdle = 0
	} else if minIdle <= 0 {
		minIdle = time.Second
	}

	assignedRuns, err := d.repo.ListRunsByRunnerInstance(ctx, cleanID, cleanInstance)
	if err != nil && ctx.Err() == nil {
		d.log.Warn("list assigned runs failed",
			slog.Any("err", err),
			slog.String("runner_id", cleanID),
			slog.String("instance_id", cleanInstance))
	} else {
		for _, run := range assignedRuns {
			switch run.Status {
			case domain.RunStatusPending, domain.RunStatusRunning:
				updated, cancelErr := d.cancelRunInternal(ctx, run, reason)
				if cancelErr != nil {
					d.log.Warn("cancel run for offline runner failed",
						slog.Any("err", cancelErr),
						slog.String("run_id", run.ID),
						slog.String("runner_id", cleanID),
						slog.String("instance_id", cleanInstance))
					continue
				}
				if err := d.rescheduleRun(ctx, updated); err != nil {
					d.log.Warn("failed to enqueue replacement run",
						slog.Any("err", err),
						slog.String("run_id", run.ID))
				}
			default:
			}
		}
	}

	for {
		attempt++
		foundWork := false
		reclaimed, err := d.broker.Reclaim(ctx, stream, group, reclaimerConsumerName(group), minIdle, 64)
		if err != nil {
			d.log.Warn("drain runner stream reclaim failed",
				slog.Any("err", err),
				slog.String("stream", stream),
				slog.String("group", group))
			break
		}
		if len(reclaimed) > 0 {
			foundWork = true
			d.log.Debug("drain runner stream reclaimed messages",
				slog.String("runner_id", cleanID),
				slog.String("instance_id", cleanInstance),
				slog.Int("attempt", attempt),
				slog.Int("messages", len(reclaimed)))
			for _, message := range reclaimed {
				if err := d.handleStaleRunnerMessage(ctx, stream, group, visibility, message); err != nil && ctx.Err() == nil {
					d.log.Warn("drain runner stream message failed",
						slog.Any("err", err),
						slog.String("stream", stream),
						slog.String("message_id", message.ID))
				}
				processed++
			}
		}
		if ctx.Err() != nil {
			break
		}
		if processed%64 == 0 {
			d.log.Debug("drain runner stream progress",
				slog.String("runner_id", cleanID),
				slog.String("instance_id", cleanInstance),
				slog.Int("processed", processed))
		}
		if !foundWork {
			d.log.Debug("drain runner stream idle",
				slog.String("runner_id", cleanID),
				slog.String("instance_id", cleanInstance),
				slog.Int("attempt", attempt))
			break
		}
	}
	if ctx.Err() != nil {
		d.log.Warn("drain runner stream interrupted",
			slog.String("runner_id", cleanID),
			slog.String("instance_id", cleanInstance),
			slog.String("reason", ctx.Err().Error()))
	}
	if err := d.broker.DeleteStream(ctx, stream); err != nil && ctx.Err() == nil {
		d.log.Debug("delete runner stream failed", slog.Any("err", err), slog.String("stream", stream))
	}
	d.log.Info("runner stream drained", slog.String("runner_id", cleanID), slog.String("instance_id", cleanInstance), slog.Duration("duration", time.Since(start)))
}

func (d *Dispatcher) handleStaleRunnerMessage(ctx context.Context, stream string, group string, visibility time.Duration, message queue.Message) error {
	payload, err := jobs.DecodeJobPayload(message.Body)
	if err != nil {
		if ackErr := d.broker.Ack(ctx, stream, group, message.ID); ackErr != nil {
			return fmt.Errorf("ack malformed job: %w", ackErr)
		}
		return nil
	}
	run, err := d.repo.GetRun(ctx, payload.RunID)
	if err != nil {
		if ackErr := d.broker.Ack(ctx, stream, group, message.ID); ackErr != nil {
			return ackErr
		}
		return nil
	}
	switch run.Status {
	case domain.RunStatusPending, domain.RunStatusRunning:
		timeout := payload.Timeout(d.cfg.Runner.DefaultTimeout)
		createdAt := payload.CreationTime()
		if createdAt.IsZero() {
			createdAt = run.QueuedAt
		}
		expired := false
		if !createdAt.IsZero() && timeout > 0 {
			if now := time.Now(); now.After(createdAt.Add(timeout)) {
				expired = true
			}
		}
		reason := fmt.Sprintf("visibility timeout after %s", visibility)
		if expired {
			reason = fmt.Sprintf("run timeout after %s", timeout)
		}
		updated, cancelErr := d.cancelRunInternal(ctx, run, reason)
		if cancelErr != nil {
			return cancelErr
		}
		if err := d.rescheduleRun(ctx, updated); err != nil {
			d.log.Warn("failed to enqueue replacement run",
				slog.Any("err", err),
				slog.String("run_id", run.ID))
		}
	default:
		// no-op
	}
	if err := d.broker.Ack(ctx, stream, group, message.ID); err != nil {
		return err
	}
	return nil
}

func (d *Dispatcher) runEventLoop(ctx context.Context) {
	if d.broker == nil || strings.TrimSpace(d.eventStream) == "" {
		return
	}
	block := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		messages, err := d.broker.Claim(ctx, d.eventStream, d.eventGroup, d.eventConsumer, 32, block)
		if err != nil {
			if errors.Is(err, queue.ErrNoMessages) || ctx.Err() != nil {
				continue
			}
			d.log.Warn("event claim failed", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}
		for _, message := range messages {
			evt, decodeErr := events.Decode(message.Body)
			if decodeErr != nil {
				d.log.Warn("decode runner event failed", slog.Any("err", decodeErr))
				_ = d.broker.Ack(ctx, d.eventStream, d.eventGroup, message.ID)
				continue
			}
			d.log.Debug("runner event received",
				slog.String("type", evt.Type),
				slog.String("runner_id", evt.RunnerID),
				slog.String("instance_id", evt.InstanceID),
				slog.String("status", evt.Status))
			d.handleRunnerEvent(ctx, evt)
			if err := d.broker.Ack(ctx, d.eventStream, d.eventGroup, message.ID); err != nil && ctx.Err() == nil {
				d.log.Warn("ack runner event failed", slog.Any("err", err))
			}
		}
	}
}

func (d *Dispatcher) pickRunner(ctx context.Context, required []string) (domain.Runner, bool, error) {
	runners, err := d.cachedRunners(ctx)
	if err != nil {
		return domain.Runner{}, false, err
	}
	bestScore := math.MaxInt32
	var bestRunner domain.Runner
	for _, runner := range runners {
		status := strings.ToLower(strings.TrimSpace(runner.Status))
		if status != "online" {
			continue
		}
		if strings.TrimSpace(runner.InstanceID) == "" {
			continue
		}
		if !supportsTags(runner.Tags, required) {
			continue
		}
		load := runner.ActiveJobs
		if runner.MaxParallel > 0 && runner.ActiveJobs >= runner.MaxParallel {
			load += 1000
		}
		if load < bestScore {
			bestScore = load
			bestRunner = runner
		}
	}
	if bestScore == math.MaxInt32 {
		return domain.Runner{}, false, nil
	}
	return bestRunner, true, nil
}

func (d *Dispatcher) cachedRunners(ctx context.Context) ([]domain.Runner, error) {
	d.cacheMu.RLock()
	if time.Now().Before(d.cacheExpiry) && d.cacheRunners != nil {
		defer d.cacheMu.RUnlock()
		return d.cacheRunners, nil
	}
	d.cacheMu.RUnlock()
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	if time.Now().Before(d.cacheExpiry) && d.cacheRunners != nil {
		return d.cacheRunners, nil
	}
	runners, err := d.repo.ListRunners(ctx)
	if err != nil {
		return nil, err
	}
	d.cacheRunners = runners
	d.cacheExpiry = time.Now().Add(d.cfg.Dispatcher.RefreshInterval)
	return runners, nil
}

func (d *Dispatcher) invalidateRunnerCache() {
	d.cacheMu.Lock()
	d.cacheRunners = nil
	d.cacheExpiry = time.Time{}
	d.cacheMu.Unlock()
}

func supportsTags(runnerTags []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(runnerTags))
	for _, tag := range runnerTags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	for _, tag := range required {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value == "" {
			continue
		}
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func (d *Dispatcher) cancelRunInternal(ctx context.Context, run domain.Run, reason string) (domain.Run, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		if run.Status == domain.RunStatusPending {
			reason = "canceled before execution"
		} else {
			reason = "canceled during execution"
		}
	}
	now := time.Now().UTC()
	run.Status = domain.RunStatusCanceled
	if run.StartedAt.IsZero() {
		if !run.QueuedAt.IsZero() {
			run.StartedAt = run.QueuedAt
		} else {
			run.StartedAt = now
		}
	}
	run.FinishedAt = now
	run.ErrorMessage = reason
	if err := d.repo.UpdateRun(ctx, &run); err != nil {
		return run, err
	}
	return run, nil
}

func (d *Dispatcher) rescheduleRun(ctx context.Context, previous domain.Run) error {
	exploitID := strings.TrimSpace(previous.ExploitID)
	targetID := strings.TrimSpace(previous.TargetID)
	if exploitID == "" || targetID == "" {
		return fmt.Errorf("run %s missing exploit/target for reschedule", previous.ID)
	}
	exploit, err := d.repo.GetExploit(ctx, exploitID)
	if err != nil {
		return fmt.Errorf("load exploit %s: %w", exploitID, err)
	}
	target, err := d.repo.GetTarget(ctx, targetID)
	if err != nil {
		return fmt.Errorf("load target %s: %w", targetID, err)
	}
	if _, err := d.createRunForTarget(ctx, exploit, target); err != nil {
		return fmt.Errorf("create replacement run: %w", err)
	}
	return nil
}

func (d *Dispatcher) createRunForTarget(ctx context.Context, exploit domain.Exploit, target domain.Target) (domain.Run, error) {
	artifact, err := d.repo.LatestExploitArtifact(ctx, exploit.ID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("package exploit %s: %w", exploit.ID, err)
	}
	flagRegex, flagFile, timeout := jobs.HintsFromMetadata(exploit.Metadata)
	run := domain.Run{
		ExploitID: exploit.ID,
		TargetID:  target.ID,
		Status:    domain.RunStatusPending,
		QueuedAt:  time.Now().UTC(),
	}
	if err := d.repo.CreateRun(ctx, &run); err != nil {
		return domain.Run{}, fmt.Errorf("create run: %w", err)
	}
	item := domain.RunWorkItem{
		RunID:     run.ID,
		Exploit:   exploit,
		Target:    target,
		Artifact:  artifact,
		FlagRegex: flagRegex,
		FlagFile:  flagFile,
		Timeout:   timeout,
	}
	if err := scheduler.EnqueueRun(ctx, d.cfg, d.broker, item); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func runnerGroupName(runnerType string) string {
	value := strings.TrimSpace(runnerType)
	if value == "" {
		return "runner"
	}
	return fmt.Sprintf("runner-%s", value)
}

func reclaimerConsumerName(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return "reclaimer"
	}
	return fmt.Sprintf("%s-reclaimer", group)
}
