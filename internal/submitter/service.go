package submitter

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/queue"

	redis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

const (
	defaultSubmitterGroup   = "submitter"
	defaultSubmitterLatency = 1.0
	throughputWindow        = time.Minute
	submitterScriptName     = "submit.sh"
)

// Service consumes flags and executes submit.sh inside a chrooted workspace.
type Service struct {
	cfg         config.Config
	log         *slog.Logger
	broker      queue.Broker
	client      *ControlPanelClient
	submitterID string

	groupName      string
	consumerPrefix string

	concurrency       int
	heartbeatInterval time.Duration

	chrootPath    string
	packagePath   string
	workspace     string
	scriptTimeout time.Duration

	metrics  *metricsTracker
	inFlight int

	versionMu      sync.RWMutex
	packageVersion string

	mu sync.Mutex
}

// New constructs a submitter service that executes submit.sh.
func New(cfg config.Config, log *slog.Logger, broker queue.Broker, client *ControlPanelClient, submitterID string) *Service {
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
	chrootPath := cfg.FlagSubmitter.ChrootPath
	if strings.TrimSpace(chrootPath) == "" {
		chrootPath = "/env"
	}
	packagePath := cfg.FlagSubmitter.PackagePath
	if strings.TrimSpace(packagePath) == "" {
		packagePath = "/tmp/submitter"
	}
	timeout := cfg.FlagSubmitter.RequestTimeout
	if timeout <= 0 {
		timeout = time.Second * 10
	}
	workspace := filepath.Join(chrootPath, strings.TrimPrefix(packagePath, "/"))
	return &Service{
		cfg:               cfg,
		log:               log,
		broker:            broker,
		client:            client,
		submitterID:       submitterID,
		groupName:         group,
		consumerPrefix:    submitterID,
		concurrency:       max(1, cfg.FlagSubmitter.Concurrency),
		heartbeatInterval: heartbeat,
		chrootPath:        chrootPath,
		packagePath:       packagePath,
		workspace:         workspace,
		scriptTimeout:     timeout,
		metrics:           newMetricsTracker(throughputWindow),
	}
}

// Run launches worker loops until context cancellation.
func (s *Service) Run(ctx context.Context) error {
	if s.client == nil {
		return errors.New("control panel client required")
	}
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}
	if err := s.register(ctx); err != nil {
		return err
	}
	if err := s.syncPackage(ctx, ""); err != nil {
		s.log.Warn("initial package sync failed", slog.Any("err", err))
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return s.heartbeatLoop(ctx)
	})
	g.Go(func() error {
		return s.packageWatcher(ctx)
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
		Type:        "script",
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

// worker polls the queue and runs submit.sh per flag.
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
				time.Sleep(100 * time.Millisecond)
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

func (s *Service) processMessage(ctx context.Context, msg queue.Message) (bool, error) {
	payload, err := DecodeMessage(msg.Body)
	if err != nil {
		return false, fmt.Errorf("decode payload: %w", err)
	}
	flagID := strings.TrimSpace(payload.FlagID)
	if flagID == "" {
		return false, fmt.Errorf("empty flag id")
	}

	if err := s.ensurePackage(ctx); err != nil {
		return true, err
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
	result := s.executeScript(ctx, request)
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

	payloadInfo := map[string]any{
		"packageVersion": s.currentPackageVersion(),
	}
	if result.Err != nil {
		payloadInfo["error"] = result.Err.Error()
	}

	finishReq := submitFinishRequest{
		SubmitterID:       s.submitterID,
		Status:            string(result.Status),
		StatusCode:        statusCode,
		Message:           message,
		LatencyMs:         float64(latency.Milliseconds()),
		RetryAfterSeconds: retryAfterSeconds,
		Payload:           payloadInfo,
	}
	finishCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	resp, err := s.client.SubmitFinish(finishCtx, flagID, finishReq)
	cancel()
	if err != nil {
		return true, fmt.Errorf("submit finish: %w", err)
	}
	if result.Status == SubmissionStatusUnknown {
		return true, fmt.Errorf("submission status unknown for flag %s", flagID)
	}
	return resp.Retry, nil
}

func (s *Service) executeScript(ctx context.Context, request SubmissionRequest) SubmissionResult {
	result := SubmissionResult{}
	execCtx, cancel := context.WithTimeout(ctx, s.scriptTimeout)
	defer cancel()

	workspace := filepath.Join("/", strings.TrimPrefix(s.packagePath, "/"))
	command := fmt.Sprintf("cd \"$SUBMITTER_WORKSPACE\" && exec ./%s \"$@\"", submitterScriptName)
	cmd := exec.CommandContext(execCtx, "chroot", s.chrootPath, "/bin/sh", "-c", command, submitterScriptName, request.FlagValue)
	env := []string{
		fmt.Sprintf("FLAG_VALUE=%s", request.FlagValue),
		fmt.Sprintf("FLAG_ID=%s", request.FlagID),
		fmt.Sprintf("TARGET_ID=%s", request.TargetID),
		fmt.Sprintf("CHALLENGE_ID=%s", request.ChallengeID),
	}
	for key, value := range request.Metadata {
		env = append(env, fmt.Sprintf("SUBMIT_META_%s=%s", sanitizeEnvKey(key), value))
	}
	env = append(env, fmt.Sprintf("SUBMITTER_WORKSPACE=%s", workspace))
	cmd.Env = append(os.Environ(), env...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	rawOutput := strings.TrimSpace(stdout.String())
	if rawOutput == "" {
		result.Status = SubmissionStatusUnknown
		if err != nil {
			result.Err = err
		} else {
			result.Err = errors.New("submitter output empty")
		}
		return result
	}

	var payload scriptOutput
	if jsonErr := json.Unmarshal([]byte(rawOutput), &payload); jsonErr != nil {
		result.Status = SubmissionStatusUnknown
		result.Err = fmt.Errorf("decode submitter output: %w", jsonErr)
		return result
	}

	result.Status = parseSubmissionStatus(payload.Status)
	result.StatusCode = payload.StatusCode
	result.Message = payload.Message
	if payload.RetryAfter > 0 {
		after := int64(payload.RetryAfter)
		result.RetryAfter = &after
	}
	if err != nil {
		result.Err = err
	}
	logLine := strings.TrimSpace(stderr.String())
	if logLine != "" {
		s.log.Debug("submitter stderr",
			slog.String("submitter_id", s.submitterID),
			slog.String("stderr", logLine))
	}
	return result
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
		Type:              "script",
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
		Config: map[string]string{
			"packageVersion": s.currentPackageVersion(),
		},
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	if err := s.client.Heartbeat(ctx, req); err != nil {
		s.log.Warn("submitter heartbeat failed", slog.Any("err", err))
	}
}

func (s *Service) packageWatcher(ctx context.Context) error {
	channel := strings.TrimSpace(s.cfg.FlagSubmitter.PackageChannel)
	if channel == "" {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		if err := s.watchOnce(ctx, channel); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("package watcher error", slog.Any("err", err))
			time.Sleep(time.Second * 2)
		}
	}
}

func (s *Service) watchOnce(ctx context.Context, channel string) error {
	client, err := newRedisClient(s.cfg.Redis)
	if err != nil {
		return err
	}
	defer client.Close()
	pubsub := client.Subscribe(ctx, channel)
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return errors.New("pubsub channel closed")
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
				s.log.Warn("invalid package update payload", slog.Any("err", err))
				continue
			}
			msgType := strings.TrimSpace(fmt.Sprint(payload["type"]))
			if msgType != "" && msgType != "submitter-package" {
				continue
			}
			version := strings.TrimSpace(fmt.Sprint(payload["version"]))
			if err := s.syncPackage(ctx, version); err != nil {
				s.log.Warn("sync submitter package failed", slog.Any("err", err))
			} else if version != "" {
				s.log.Info("submitter package updated", slog.String("version", version))
			}
		}
	}
}

func (s *Service) ensurePackage(ctx context.Context) error {
	if s.currentPackageVersion() != "" {
		return nil
	}
	return s.syncPackage(ctx, "")
}

func (s *Service) syncPackage(ctx context.Context, expectedVersion string) error {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if expectedVersion != "" && expectedVersion == s.packageVersion {
		return nil
	}
	pkg, err := s.client.DownloadSubmitterPackage(ctx)
	if err != nil {
		return fmt.Errorf("download submitter package: %w", err)
	}
	if pkg.Version != "" && pkg.Version == s.packageVersion {
		return nil
	}
	if err := s.installPackage(pkg.Data); err != nil {
		return err
	}
	if pkg.Version != "" {
		s.packageVersion = pkg.Version
	} else {
		s.packageVersion = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return nil
}

func (s *Service) installPackage(data []byte) error {
	tmp := s.workspace + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("prepare tmp workspace: %w", err)
	}
	if err := unzipData(data, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(s.workspace); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace workspace: %w", err)
	}
	if err := os.Rename(tmp, s.workspace); err != nil {
		return fmt.Errorf("activate workspace: %w", err)
	}
	return nil
}

func unzipData(data []byte, dest string) error {
	reader := bytes.NewReader(data)
	zr, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	for _, file := range zr.File {
		if err := extractZipEntry(dest, file); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(dest string, file *zip.File) error {
	target := filepath.Join(dest, file.Name)
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		return fmt.Errorf("zip entry escapes workspace: %s", file.Name)
	}
	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return err
	}
	return nil
}

func (s *Service) currentPackageVersion() string {
	s.versionMu.RLock()
	defer s.versionMu.RUnlock()
	return s.packageVersion
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

func parseSubmissionStatus(value string) SubmissionStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accepted", "success":
		return SubmissionStatusAccepted
	case "failed", "failure":
		return SubmissionStatusFailed
	case "duplicated", "duplicate":
		return SubmissionStatusDuplicated
	default:
		return SubmissionStatusUnknown
	}
}

type scriptOutput struct {
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retryAfter"`
}

func sanitizeEnvKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "KEY"
	}
	key = strings.ToUpper(key)
	replacer := strings.NewReplacer("-", "_", " ", "_", ".", "_")
	return replacer.Replace(key)
}

type metricsTracker struct {
	mu      sync.Mutex
	window  time.Duration
	entries []metricsEntry
}

type metricsEntry struct {
	timestamp time.Time
	latency   time.Duration
}

func newMetricsTracker(window time.Duration) *metricsTracker {
	if window <= 0 {
		window = time.Minute
	}
	return &metricsTracker{
		window:  window,
		entries: make([]metricsEntry, 0, 64),
	}
}

func (m *metricsTracker) record(latency time.Duration) {
	if latency < 0 {
		latency = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.entries = append(m.entries, metricsEntry{timestamp: now, latency: latency})
	m.pruneLocked(now)
}

func (m *metricsTracker) snapshot() metricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.pruneLocked(now)
	var sum float64
	for _, entry := range m.entries {
		sum += entry.latency.Seconds()
	}
	count := len(m.entries)
	windowSeconds := m.window.Seconds()
	throughput := 0.0
	avgLatency := 0.0
	if count > 0 && windowSeconds > 0 {
		throughput = float64(count) / windowSeconds
		avgLatency = sum / float64(count)
	}
	return metricsSnapshot{
		throughput:    throughput,
		avgLatency:    avgLatency,
		latencySum:    sum,
		count:         count,
		windowSeconds: windowSeconds,
	}
}

func (m *metricsTracker) pruneLocked(now time.Time) {
	cutoff := now.Add(-m.window)
	idx := 0
	for _, entry := range m.entries {
		if entry.timestamp.After(cutoff) {
			break
		}
		idx++
	}
	if idx > 0 && idx < len(m.entries) {
		m.entries = append([]metricsEntry(nil), m.entries[idx:]...)
	} else if idx >= len(m.entries) {
		m.entries = m.entries[:0]
	}
}

type metricsSnapshot struct {
	throughput    float64
	avgLatency    float64
	latencySum    float64
	count         int
	windowSeconds float64
}

func (m metricsSnapshot) pressure(concurrency int) float64 {
	if concurrency <= 0 || m.windowSeconds <= 0 {
		return 0
	}
	capacity := float64(concurrency) * m.windowSeconds
	if capacity == 0 {
		return 0
	}
	return m.latencySum / capacity
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func newRedisClient(cfg config.RedisConfig) (redis.UniversalClient, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLSEnabled {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}
