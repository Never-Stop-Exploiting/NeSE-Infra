package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/storage"
)

// Dispatcher consumes the primary run queue and routes jobs to runner-specific queues.
type Dispatcher struct {
	cfg    config.Config
	log    *slog.Logger
	broker queue.Broker
	repo   storage.Repository

	cacheMu      sync.RWMutex
	cacheRunners []domain.Runner
	cacheExpiry  time.Time
}

func New(cfg config.Config, log *slog.Logger, broker queue.Broker, repo storage.Repository) *Dispatcher {
	return &Dispatcher{
		cfg:    cfg,
		log:    log,
		broker: broker,
		repo:   repo,
	}
}

// Run starts the routing loop until the context is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
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
	queueName := queue.RunnerQueueName(d.cfg.RunQueue.RunnerPrefix, runner.ID)
	if _, err := d.broker.Publish(ctx, queueName, message.Body); err != nil {
		return d.retryLater(ctx, group, message.ID, err)
	}
	if err := d.broker.Ack(ctx, d.cfg.RunQueue.Name, group, message.ID); err != nil {
		return err
	}
	d.log.Debug("dispatched job",
		slog.String("run_id", payload.RunID),
		slog.String("runner_id", runner.ID),
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
