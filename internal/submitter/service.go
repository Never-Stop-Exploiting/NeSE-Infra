package submitter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/queue"

	"golang.org/x/sync/errgroup"
)

const (
	defaultSubmitterGroup   = "submitter"
	defaultSubmitterLatency = 1.0 // seconds, used when we have no data yet
	throughputWindow        = time.Minute
)

// Service processes flags from Redis and reports status back to the control plane.
type Service struct {
	cfg            config.Config
	log            *slog.Logger
	broker         queue.Broker
	client         *ControlPlaneClient
	plugin         Plugin
	submitterID    string
	groupName      string
	consumerPrefix string

	concurrency       int
	heartbeatInterval time.Duration

	metrics  *metricsTracker
	inFlight int

	mu sync.Mutex
}

// New constructs a decoupled submitter service.
func New(cfg config.Config, log *slog.Logger, broker queue.Broker, client *ControlPlaneClient, submitterID string) *Service {
	plugin := NewHTTPPlugin(cfg.FlagSubmitter)
	if submitterID == "" {
		if host, err := os.Hostname(); err == nil && host != "" {
			submitterID = fmt.Sprintf("submitter-%s", host)
		} else {
			submitterID = "submitter"
		}
	}
	group := defaultSubmitterGroup
	heartbeat := cfg.FlagSubmitter.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = time.Second * 10
	}
	return &Service{
		cfg:               cfg,
		log:               log,
		broker:            broker,
		client:            client,
		plugin:            plugin,
		submitterID:       submitterID,
		groupName:         group,
		consumerPrefix:    submitterID,
		concurrency:       max(1, cfg.FlagSubmitter.Concurrency),
		heartbeatInterval: heartbeat,
		metrics:           newMetricsTracker(throughputWindow),
	}
}

// Run launches worker and heartbeat loops until context cancellation.
func (s *Service) Run(ctx context.Context) error {
	if s.client == nil {
		return errors.New("control plane client required")
	}
	if err := s.register(ctx); err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return s.heartbeatLoop(ctx)
	})
	for i := 0; i < s.concurrency; i++ {
		workerID := i + 1
		g.Go(func() error {
			return s.worker(ctx, workerID)
		})
	}
	s.log.Info("flag submitter started",
		slog.String("submitter_id", s.submitterID),
		slog.Int("workers", s.concurrency))
	return g.Wait()
}

func (s *Service) register(ctx context.Context) error {
	host, _ := os.Hostname()
	req := registerSubmitterRequest{
		ID:          s.submitterID,
		Type:        "http",
		Hostname:    host,
		Concurrency: s.concurrency,
		Status:      "online",
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	if err := s.client.Register(ctx, req); err != nil {
		return fmt.Errorf("register submitter: %w", err)
	}
	return nil
}

func (s *Service) heartbeatLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.sendHeartbeat(ctx)
		}
	}
}

func (s *Service) sendHeartbeat(ctx context.Context) {
	stats := s.metrics.snapshot()
	req := heartbeatSubmitterRequest{
		ID:                s.submitterID,
		Status:            "online",
		Type:              "http",
		Hostname:          "",
		Concurrency:       s.concurrency,
		InFlight:          s.currentInFlight(),
		Pending:           0,
		RecentThroughput:  stats.throughput,
		AvgLatencyMs:      stats.avgLatency * 1000,
		Pressure:          stats.pressure(s.concurrency),
		WindowSeconds:     stats.windowSeconds,
		LatencySumSeconds: stats.latencySum,
		ProcessedCount:    stats.count,
		Labels:            nil,
		Config:            nil,
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	if err := s.client.Heartbeat(ctx, req); err != nil {
		s.log.Warn("submitter heartbeat failed", slog.Any("err", err))
	}
}

func (s *Service) worker(ctx context.Context, workerID int) error {
	consumer := s.consumerPrefix
	if s.concurrency > 1 {
		consumer = fmt.Sprintf("%s-%d", s.consumerPrefix, workerID)
	}
	for {
		select {
		case <-ctx.Done():
			s.log.Info("submitter worker stopping", slog.Int("worker", workerID))
			return ctx.Err()
		default:
		}
		if err := s.pollOnce(ctx, consumer); err != nil {
			if errors.Is(err, queue.ErrNoMessages) {
				time.Sleep(time.Millisecond * 100)
				continue
			}
			s.log.Warn("submitter poll failed", slog.Any("err", err), slog.Int("worker", workerID))
			time.Sleep(time.Second)
		}
	}
}

func (s *Service) pollOnce(ctx context.Context, consumer string) error {
	msgs, err := s.broker.Claim(ctx, s.cfg.FlagQueue.Name, s.groupName, consumer, 1, s.cfg.FlagSubmitter.RetryBackoff)
	if err != nil {
		return err
	}
	for _, msg := range msgs {
		retry, err := s.processMessage(ctx, msg)
		if err != nil || retry {
			delay := s.retryDelay()
			s.log.Warn("flag submission needs retry",
				slog.String("message_id", msg.ID),
				slog.Duration("delay", delay),
				slog.Any("err", err))
			_ = s.broker.Nack(ctx, s.cfg.FlagQueue.Name, s.groupName, msg.ID, delay)
			continue
		}
		if err := s.broker.Ack(ctx, s.cfg.FlagQueue.Name, s.groupName, msg.ID); err != nil {
			s.log.Warn("ack failed", slog.Any("err", err), slog.String("message_id", msg.ID))
		}
	}
	return nil
}

func (s *Service) processMessage(ctx context.Context, msg queue.Message) (retry bool, err error) {
	payload, err := DecodeMessage(msg.Body)
	if err != nil {
		return false, fmt.Errorf("decode payload: %w", err)
	}
	flagID := strings.TrimSpace(payload.FlagID)
	if flagID == "" {
		return false, fmt.Errorf("empty flag id")
	}

	startCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	flagInfo, err := s.client.SubmitStart(startCtx, flagID, s.submitterID)
	cancel()
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusNotFound, http.StatusConflict:
				s.log.Info("flag start skipped", slog.String("flag_id", flagID), slog.Int("status", apiErr.StatusCode))
				return false, nil
			}
		}
		return true, fmt.Errorf("submit start: %w", err)
	}
	flagValue := flagInfo.Value
	if strings.TrimSpace(flagValue) == "" {
		flagValue = payload.FlagValue
	}

	request := SubmissionRequest{
		FlagID:      flagID,
		FlagValue:   flagValue,
		ChallengeID: flagInfo.ChallengeID,
		TargetID:    flagInfo.TargetID,
		Metadata:    payload.Metadata,
	}

	s.incrementInFlight(1)
	defer s.incrementInFlight(-1)

	start := time.Now()
	result := s.plugin.Submit(ctx, request)
	latency := time.Since(start)
	s.metrics.record(latency)

	if result.Status == "" {
		result.Status = SubmissionStatusUnknown
	}
	statusCode := result.StatusCode
	message := result.Message
	if message == "" && result.Err != nil {
		message = result.Err.Error()
	}

	retryAfterSeconds := int(s.retryDelay().Seconds())
	if result.RetryAfter != nil && *result.RetryAfter > 0 {
		retryAfterSeconds = int(*result.RetryAfter)
	}

	finishReq := submitFinishRequest{
		SubmitterID:       s.submitterID,
		Status:            string(result.Status),
		StatusCode:        statusCode,
		Message:           message,
		LatencyMs:         float64(latency.Milliseconds()),
		RetryAfterSeconds: retryAfterSeconds,
		Payload: map[string]any{
			"plugin": s.plugin.ID(),
		},
	}
	if result.Err != nil {
		finishReq.Payload["error"] = result.Err.Error()
	}

	finishCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	resp, err := s.client.SubmitFinish(finishCtx, flagID, finishReq)
	cancel()
	if err != nil {
		return true, fmt.Errorf("submit finish: %w", err)
	}

	switch result.Status {
	case SubmissionStatusUnknown:
		return true, fmt.Errorf("submission status unknown for flag %s", flagID)
	}

	return resp.Retry, nil
}

func (s *Service) incrementInFlight(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight += delta
	if s.inFlight < 0 {
		s.inFlight = 0
	}
}

func (s *Service) currentInFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight
}

func (s *Service) retryDelay() time.Duration {
	delay := s.cfg.FlagSubmitter.RetryBackoff
	if delay <= 0 {
		delay = time.Second * 10
	}
	return delay
}

type metricsSnapshot struct {
	throughput    float64
	avgLatency    float64
	latencySum    float64
	count         int
	windowSeconds float64
}

func (m metricsSnapshot) pressure(concurrency int) float64 {
	if concurrency <= 0 {
		return 0
	}
	window := m.windowSeconds
	if window <= 0 {
		window = throughputWindow.Seconds()
	}
	capacity := float64(concurrency) * window
	if capacity <= 0 {
		return 0
	}
	return m.latencySum / capacity
}

type metricsTracker struct {
	window time.Duration
	mu     sync.Mutex
	events []metricEvent
}

type metricEvent struct {
	timestamp time.Time
	latency   float64
}

func newMetricsTracker(window time.Duration) *metricsTracker {
	if window <= 0 {
		window = time.Minute
	}
	return &metricsTracker{
		window: window,
		events: make([]metricEvent, 0, 64),
	}
}

func (m *metricsTracker) record(latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	seconds := latency.Seconds()
	if seconds <= 0 {
		seconds = defaultSubmitterLatency
	}
	m.events = append(m.events, metricEvent{timestamp: now, latency: seconds})
	m.prune(now)
}

func (m *metricsTracker) snapshot() metricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.prune(now)
	count := len(m.events)
	windowSeconds := m.window.Seconds()
	throughput := float64(count) / windowSeconds
	latencySum := 0.0
	for _, event := range m.events {
		latencySum += event.latency
	}
	avg := defaultSubmitterLatency
	if count > 0 {
		avg = latencySum / float64(count)
	}
	return metricsSnapshot{
		throughput:    throughput,
		avgLatency:    avg,
		latencySum:    latencySum,
		count:         count,
		windowSeconds: windowSeconds,
	}
}

func (m *metricsTracker) prune(now time.Time) {
	if len(m.events) == 0 {
		return
	}
	cutoff := now.Add(-m.window)
	idx := 0
	for idx < len(m.events) && m.events[idx].timestamp.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		m.events = append([]metricEvent(nil), m.events[idx:]...)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
