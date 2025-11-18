package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"awdinfra/internal/config"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/submitter"

	"golang.org/x/sync/errgroup"
)

// Agent consumes run jobs and executes exploits in an isolated workspace.
type Agent struct {
	cfg             config.Config
	log             *slog.Logger
	broker          queue.Broker
	controlClient   *ControlPanelClient
	hostname        string
	activeJobs      atomic.Int32
	lastHeartbeat   time.Time
	brokerStatus    string
	baseLabels      map[string]string
	runnerTagsSet   map[string]struct{}
	artifactCache   *artifactCache
	instanceMu      sync.RWMutex
	warningMatchers []*regexp.Regexp
}

var errJobDiscard = errors.New("runner job discarded")

// New constructs an Agent.
func New(cfg config.Config, log *slog.Logger, broker queue.Broker, control *ControlPanelClient, brokerStatus string) *Agent {
	tagSet := make(map[string]struct{}, len(cfg.Runner.Tags))
	for _, tag := range cfg.Runner.Tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value != "" {
			tagSet[value] = struct{}{}
		}
	}
	cache, err := newArtifactCache(cfg.Runner.ArtifactCacheDir, cfg.Runner.ArtifactCacheTTL, cfg.Runner.ArtifactCacheMaxBytes, log)
	if err != nil {
		log.Warn("artifact cache disabled", slog.Any("err", err))
		cache = nil
	}
	var warningMatchers []*regexp.Regexp
	for _, pattern := range cfg.Runner.WarningPatterns {
		re, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			log.Warn("ignore invalid warning pattern", slog.String("pattern", pattern), slog.Any("err", compileErr))
			continue
		}
		warningMatchers = append(warningMatchers, re)
	}
	return &Agent{
		cfg:             cfg,
		log:             log,
		broker:          broker,
		controlClient:   control,
		brokerStatus:    brokerStatus,
		baseLabels:      map[string]string{"broker": brokerStatus},
		runnerTagsSet:   tagSet,
		artifactCache:   cache,
		warningMatchers: warningMatchers,
	}
}

// Run starts the main event loop until context cancellation.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.registerSelf(ctx); err != nil {
		return err
	}
	a.sendHeartbeat(ctx)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return a.consumeLoop(ctx)
	})

	g.Go(func() error {
		return a.heartbeatLoop(ctx)
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return ctx.Err()
}

func (a *Agent) consumeLoop(ctx context.Context) error {
	group := runnerGroup(a.cfg.Runner.Type)
	maxParallel := max(1, a.cfg.Runner.MaxParallelJobs)
	block := a.cfg.Runner.HeartbeatInterval * 2
	if block <= 0 {
		block = time.Second * 20
	}

	retryDelay := a.cfg.RunQueue.RetryDelay
	if retryDelay <= 0 {
		retryDelay = time.Second * 5
	}

	sema := make(chan struct{}, maxParallel)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		queueName := a.runQueueName()
		consumer := a.consumerName()
		available := maxParallel - len(sema)
		if available <= 0 {
			time.Sleep(time.Millisecond * 50)
			continue
		}

		messages, err := a.broker.Claim(ctx, queueName, group, consumer, available, block)
		if err != nil {
			if errors.Is(err, queue.ErrNoMessages) {
				continue
			}
			a.log.Error("poll failed", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}

		for _, message := range messages {
			msgCopy := message
			streamName := queueName
			sema <- struct{}{}
			go func() {
				defer func() { <-sema }()
				a.log.Debug("received job", slog.String("id", msgCopy.ID))
				a.activeJobs.Add(1)
				defer a.activeJobs.Add(-1)
				if err := a.handleMessage(ctx, msgCopy); err != nil {
					if errors.Is(err, errJobDiscard) {
						a.log.Info("job discarded", slog.String("id", msgCopy.ID))
						if ackErr := a.broker.Ack(ctx, streamName, group, msgCopy.ID); ackErr != nil {
							a.log.Warn("failed to ack discarded job", slog.Any("err", ackErr), slog.String("id", msgCopy.ID))
						}
						return
					}
					a.log.Error("job failed", slog.Any("err", err), slog.String("id", msgCopy.ID))
					if err := a.broker.Nack(ctx, streamName, group, msgCopy.ID, retryDelay); err != nil {
						a.log.Warn("failed to nack message", slog.Any("err", err), slog.String("id", msgCopy.ID))
					}
					return
				}
				if err := a.broker.Ack(ctx, streamName, group, msgCopy.ID); err != nil {
					a.log.Warn("failed to ack message", slog.Any("err", err), slog.String("id", msgCopy.ID))
				}
			}()
		}
	}
}

func (a *Agent) handleMessage(ctx context.Context, msg queue.Message) error {
	payload, err := jobs.DecodeJobPayload(msg.Body)
	if err != nil {
		a.log.Error("decode job payload failed",
			slog.String("message_id", msg.ID),
			slog.Any("err", err))
		return nil
	}

	runID := payload.RunID
	runnerIdent := runnerID(a.cfg.Runner.ID)

	if !a.supportsTags(payload.RunnerTags) {
		a.log.Info("skipping job due to runner tag mismatch",
			slog.String("run_id", runID),
			slog.Any("required_tags", payload.RunnerTags),
			slog.Any("runner_tags", a.cfg.Runner.Tags))
		return fmt.Errorf("runner lacks required tags")
	}

	artifact, err := a.resolveArtifact(ctx, payload)
	if err != nil {
		a.log.Error("resolve artifact failed",
			slog.String("run_id", runID),
			slog.Any("err", err))
		if completeErr := a.reportRunCompletion(ctx, payload, runnerIdent, RunResult{
			Status: "failed",
			Error:  fmt.Sprintf("artifact: %v", err),
		}); completeErr != nil {
			a.log.Warn("failed to report run completion",
				slog.String("run_id", runID),
				slog.Any("err", completeErr))
		}
		return nil
	}

	if err := a.reportRunStart(ctx, payload, runnerIdent); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			a.log.Info("run canceled before start",
				slog.String("run_id", runID))
			return errJobDiscard
		}
		a.log.Warn("failed to mark run started",
			slog.String("run_id", runID),
			slog.Any("err", err))
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, payload.Timeout(a.cfg.Runner.DefaultTimeout))
	defer cancel()

	var remoteCanceled atomic.Bool
	if a.controlClient != nil && strings.TrimSpace(runID) != "" {
		go a.monitorRunCancellation(runCtx, payload.RunID, cancel, &remoteCanceled)
	}

	workspace, relPath, err := a.prepareWorkspace(payload.RunID, artifact)
	if err != nil {
		a.log.Error("prepare workspace failed",
			slog.String("run_id", runID),
			slog.Any("err", err))
		if completeErr := a.reportRunCompletion(ctx, payload, runnerIdent, RunResult{
			Status: "failed",
			Error:  fmt.Sprintf("workspace: %v", err),
		}); completeErr != nil {
			a.log.Warn("failed to report run completion",
				slog.String("run_id", runID),
				slog.Any("err", completeErr))
		}
		return nil
	}
	a.log.Info("workspace prepared",
		slog.String("run_id", runID),
		slog.String("workspace", workspace),
		slog.String("relative_path", relPath))
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			a.log.Warn("failed to cleanup workspace", slog.Any("err", err), slog.String("workspace", workspace))
		}
	}()

	if err := a.ensureRunScript(workspace, payload); err != nil {
		a.log.Error("prepare run script failed",
			slog.String("run_id", runID),
			slog.Any("err", err))
		if completeErr := a.reportRunCompletion(ctx, payload, runnerIdent, RunResult{
			Status: "failed",
			Error:  fmt.Sprintf("run script: %v", err),
		}); completeErr != nil {
			a.log.Warn("failed to report run completion",
				slog.String("run_id", runID),
				slog.Any("err", completeErr))
		}
		return nil
	}

	stdout, stderr, execErr := a.execute(runCtx, relPath, payload.TargetIP, payload.TargetPort)
	if execErr != nil {
		if errors.Is(execErr, context.Canceled) && remoteCanceled.Load() {
			a.log.Info("run canceled by control panel",
				slog.String("run_id", runID))
		} else {
			a.log.Warn("exploit execution failed",
				slog.String("run_id", runID),
				slog.Any("err", execErr))
		}
	}

	flagValue, flagSource, flagErr := a.extractFlag(workspace, stdout, payload)
	if flagErr != nil {
		a.log.Warn("flag extraction failed",
			slog.String("run_id", runID),
			slog.Any("err", flagErr))
	} else {
		a.log.Info("flag captured",
			slog.String("run_id", runID),
			slog.String("flag", flagValue),
			slog.String("source", flagSource))
	}

	if len(stderr) > 0 {
		a.log.Debug("stderr output",
			slog.String("run_id", runID),
			slog.String("stderr", a.trimOutputString(stderr)))
	}

	status := "succeeded"
	errorMessage := ""
	if execErr != nil {
		if errors.Is(execErr, context.DeadlineExceeded) {
			status = "timeout"
		} else {
			status = "failed"
		}
		errorMessage = execErr.Error()
	}
	if flagErr != nil {
		if status == "succeeded" {
			status = "failed"
		}
		if errorMessage == "" {
			errorMessage = flagErr.Error()
		} else {
			errorMessage = fmt.Sprintf("%s; %s", errorMessage, flagErr.Error())
		}
	}
	warningReason := ""
	if status == "succeeded" {
		warningReason = a.detectWarning(stdout, stderr)
		if warningReason != "" {
			status = "warning"
			if errorMessage == "" {
				errorMessage = warningReason
			} else {
				errorMessage = fmt.Sprintf("%s; %s", errorMessage, warningReason)
			}
			a.log.Warn("warning pattern matched",
				slog.String("run_id", runID),
				slog.String("reason", warningReason))
		}
	}

	result := RunResult{
		Status:     status,
		Error:      errorMessage,
		FlagValue:  flagValue,
		FlagSource: flagSource,
		Stdout:     a.trimOutputString(stdout),
		Stderr:     a.trimOutputString(stderr),
	}

	if remoteCanceled.Load() {
		a.log.Info("skip completion report for canceled run", slog.String("run_id", runID))
		return nil
	}

	if err := a.reportRunCompletion(ctx, payload, runnerIdent, result); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			a.log.Info("run completion rejected (canceled)",
				slog.String("run_id", runID))
		} else {
			a.log.Warn("failed to report run completion",
				slog.String("run_id", runID),
				slog.Any("err", err))
		}
	}

	if flagValue != "" && a.controlClient == nil {
		a.publishFlagDirect(ctx, payload, flagValue)
	}

	return nil
}

func (a *Agent) heartbeatLoop(ctx context.Context) error {
	interval := a.cfg.Runner.HeartbeatInterval
	if interval <= 0 {
		interval = time.Second * 10
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.sendHeartbeat(ctx)
		}
	}
}

func (a *Agent) sendHeartbeat(ctx context.Context) {
	if a.controlClient == nil {
		a.log.Info("runner heartbeat", slog.String("runner_id", a.cfg.Runner.ID))
		return
	}
	hbCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	req := heartbeatRequest{
		ID:          runnerID(a.cfg.Runner.ID),
		InstanceID:  a.ensureInstanceID(),
		Status:      "online",
		Type:        a.cfg.Runner.Type,
		Hostname:    a.hostname,
		Tags:        a.cfg.Runner.Tags,
		MaxParallel: a.cfg.Runner.MaxParallelJobs,
		ActiveJobs:  int(a.activeJobs.Load()),
		Labels:      a.runnerLabels(),
		Config:      a.heartbeatConfig(),
	}
	if err := a.controlClient.Heartbeat(hbCtx, req); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			a.log.Warn("heartbeat rejected; rotating instance id",
				slog.String("runner_id", req.ID),
				slog.Any("err", err))
			if rotateErr := a.rotateInstance(ctx); rotateErr != nil {
				a.log.Error("failed to rotate instance id",
					slog.String("runner_id", req.ID),
					slog.Any("err", rotateErr))
			}
		} else {
			a.log.Warn("failed to send heartbeat", slog.Any("err", err))
		}
		return
	}
	a.lastHeartbeat = time.Now().UTC()
}

func (a *Agent) rotateInstance(ctx context.Context) error {
	newID := GenerateInstanceID()
	a.instanceMu.Lock()
	a.cfg.Runner.InstanceID = newID
	a.instanceMu.Unlock()
	a.log.Info("rotated runner instance id",
		slog.String("runner_id", runnerID(a.cfg.Runner.ID)),
		slog.String("instance_id", newID))
	if err := a.registerSelf(ctx); err != nil {
		return err
	}
	a.sendHeartbeat(ctx)
	return nil
}

func (a *Agent) registerSelf(ctx context.Context) error {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = runnerID(a.cfg.Runner.ID)
	}
	a.hostname = host
	instanceID := a.ensureInstanceID()
	labels := map[string]string{
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"app":       a.cfg.AppName,
		"hostname":  host,
		"broker":    a.brokerStatus,
		"instance":  instanceID,
		"runner_id": runnerID(a.cfg.Runner.ID),
	}
	a.baseLabels = cloneLabels(labels)
	if a.controlClient == nil {
		a.log.Warn("control panel client disabled; skipping registration",
			slog.String("runner_id", runnerID(a.cfg.Runner.ID)))
		return nil
	}
	a.log.Info("runner initialized",
		slog.String("runner_id", runnerID(a.cfg.Runner.ID)),
		slog.String("instance_id", instanceID),
		slog.String("control_panel", a.controlClient.baseURL.String()))
	return nil
}

func (a *Agent) resolveArtifact(ctx context.Context, payload jobs.JobPayload) (artifactPayload, error) {
	if data, err := payload.ArtifactBytes(); err == nil {
		if err := verifyArtifactBytesMD5(payload.ArtifactMD5, data); err != nil {
			return artifactPayload{}, err
		}
		return artifactPayload{inline: data}, nil
	} else if payload.ArtifactURI == "" {
		return artifactPayload{}, err
	}
	if payload.ArtifactMD5 != "" && a.artifactCache != nil {
		if cached, ok := a.artifactCache.get(payload.ArtifactMD5); ok {
			file, err := os.Open(cached.path)
			if err == nil {
				if err := verifyArtifactFileMD5(payload.ArtifactMD5, file); err == nil {
					return artifactPayload{file: file, size: cached.size}, nil
				}
				a.log.Warn("artifact cache md5 mismatch",
					slog.String("run_id", payload.RunID),
					slog.String("artifact_md5", payload.ArtifactMD5),
					slog.Any("err", err))
				file.Close()
				a.artifactCache.remove(payload.ArtifactMD5)
			} else {
				a.log.Warn("artifact cache file open failed",
					slog.String("run_id", payload.RunID),
					slog.String("artifact_md5", payload.ArtifactMD5),
					slog.Any("err", err))
				a.artifactCache.remove(payload.ArtifactMD5)
			}
		}
	}
	if a.controlClient == nil {
		return artifactPayload{}, fmt.Errorf("artifact uri provided but control panel client is not configured")
	}
	downloadCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	a.log.Info("downloading artifact",
		slog.String("run_id", payload.RunID),
		slog.String("exploit_id", payload.ExploitID),
		slog.String("artifact_uri", payload.ArtifactURI))
	data, err := a.controlClient.DownloadArtifact(downloadCtx, payload.ArtifactURI)
	if err != nil {
		return artifactPayload{}, fmt.Errorf("download artifact: %w", err)
	}
	if err := verifyArtifactBytesMD5(payload.ArtifactMD5, data); err != nil {
		return artifactPayload{}, err
	}
	if payload.ArtifactMD5 != "" && a.artifactCache != nil {
		if cached, cacheErr := a.artifactCache.store(payload.ArtifactMD5, data); cacheErr == nil {
			file, err := os.Open(cached.path)
			if err == nil {
				return artifactPayload{file: file, size: cached.size, inline: data}, nil
			}
			a.log.Warn("artifact cache file open failed after store",
				slog.String("run_id", payload.RunID),
				slog.String("artifact_md5", payload.ArtifactMD5),
				slog.Any("err", err))
			a.artifactCache.remove(payload.ArtifactMD5)
		} else {
			a.log.Warn("artifact cache store failed",
				slog.String("run_id", payload.RunID),
				slog.Any("err", cacheErr))
		}
	}
	return artifactPayload{inline: data}, nil
}

func runnerID(value string) string {
	if value == "" {
		return "local-runner"
	}
	return value
}

func runnerGroup(value string) string {
	if value != "" {
		return fmt.Sprintf("runner-%s", value)
	}
	return "runner"
}

func (a *Agent) runQueueName() string {
	return queue.RunnerQueueName(a.cfg.RunQueue.RunnerPrefix, runnerID(a.cfg.Runner.ID), a.currentInstanceID())
}

func (a *Agent) consumerName() string {
	instanceID := a.currentInstanceID()
	base := runnerID(a.cfg.Runner.ID)
	if instanceID == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, instanceID)
}

func (a *Agent) currentInstanceID() string {
	a.instanceMu.RLock()
	defer a.instanceMu.RUnlock()
	return strings.TrimSpace(a.cfg.Runner.InstanceID)
}

func (a *Agent) ensureInstanceID() string {
	a.instanceMu.Lock()
	defer a.instanceMu.Unlock()
	id := strings.TrimSpace(a.cfg.Runner.InstanceID)
	if id == "" {
		id = GenerateInstanceID()
		a.cfg.Runner.InstanceID = id
	}
	return id
}

func (a *Agent) ensureRunScript(workspace string, payload jobs.JobPayload) error {
	runPath := filepath.Join(workspace, "run.sh")
	if _, err := os.Stat(runPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	scriptContent := payload.RunScript
	if scriptContent == "" {
		entry := strings.TrimSpace(payload.Entrypoint)
		if entry == "" {
			return fmt.Errorf("missing run.sh and entrypoint")
		}
		scriptContent = fmt.Sprintf("#!/bin/sh\nset -euo pipefail\n%s \"$1\" \"$2\"\n", entry)
	}
	if !strings.HasPrefix(scriptContent, "#!") {
		scriptContent = "#!/bin/sh\n" + scriptContent
	}
	if !strings.HasSuffix(scriptContent, "\n") {
		scriptContent += "\n"
	}
	if err := os.WriteFile(runPath, []byte(scriptContent), 0o755); err != nil {
		return err
	}
	return os.Chmod(runPath, 0o755)
}

func (a *Agent) reportRunStart(ctx context.Context, payload jobs.JobPayload, runnerID string) error {
	if a.controlClient == nil || strings.TrimSpace(payload.RunID) == "" {
		return nil
	}
	startCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	if err := a.controlClient.MarkRunStarted(startCtx, payload.RunID, runnerID, a.currentInstanceID()); err != nil {
		return err
	}
	return nil
}

func (a *Agent) reportRunCompletion(ctx context.Context, payload jobs.JobPayload, runnerID string, result RunResult) error {
	if a.controlClient == nil || strings.TrimSpace(payload.RunID) == "" {
		return nil
	}
	finishCtx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	return a.controlClient.CompleteRun(finishCtx, payload.RunID, runnerID, a.currentInstanceID(), result)
}

func (a *Agent) monitorRunCancellation(ctx context.Context, runID string, cancel context.CancelFunc, canceled *atomic.Bool) {
	if a.controlClient == nil || strings.TrimSpace(runID) == "" {
		return
	}
	interval := time.Second
	if a.cfg.Runner.HeartbeatInterval > 0 && a.cfg.Runner.HeartbeatInterval < interval {
		interval = a.cfg.Runner.HeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := a.fetchRunInfo(ctx, runID)
			if err != nil {
				a.log.Debug("poll run status failed",
					slog.String("run_id", runID),
					slog.Any("err", err))
				continue
			}
			if strings.EqualFold(info.Status, "canceled") {
				canceled.Store(true)
				cancel()
				return
			}
		}
	}
}

func (a *Agent) fetchRunInfo(ctx context.Context, runID string) (RunInfo, error) {
	// Reuse parent context but ensure we bound request duration.
	requestCtx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	return a.controlClient.GetRun(requestCtx, runID)
}

func (a *Agent) detectWarning(stdout, stderr []byte) string {
	if len(a.warningMatchers) == 0 {
		return ""
	}
	if len(stdout) > 0 {
		for _, matcher := range a.warningMatchers {
			if matcher.Match(stdout) {
				return fmt.Sprintf("stdout matched warning pattern %q", matcher.String())
			}
		}
	}
	if len(stderr) > 0 {
		for _, matcher := range a.warningMatchers {
			if matcher.Match(stderr) {
				return fmt.Sprintf("stderr matched warning pattern %q", matcher.String())
			}
		}
	}
	return ""
}

func (a *Agent) trimOutputString(data []byte) string {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	limit := a.cfg.Runner.LogOutputLimit
	if limit <= 0 {
		limit = 4096
	}
	raw := []byte(text)
	if len(raw) <= limit {
		return text
	}
	truncated := raw[:limit]
	for len(truncated) > 0 && !utf8.Valid(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	if len(truncated) == 0 {
		return "…"
	}
	return string(truncated) + "…"
}

func (a *Agent) publishFlagDirect(ctx context.Context, payload jobs.JobPayload, flagValue string) {
	if a.broker == nil || strings.TrimSpace(flagValue) == "" {
		return
	}
	message := submitter.QueueMessage{
		FlagID:    fmt.Sprintf("flag-%s", payload.RunID),
		FlagValue: flagValue,
		TargetID:  payload.TargetID,
	}
	data, err := submitter.EncodeMessage(message)
	if err != nil {
		a.log.Warn("encode flag message failed",
			slog.String("run_id", payload.RunID),
			slog.Any("err", err))
		return
	}
	if _, err := a.broker.Publish(ctx, a.cfg.FlagQueue.Name, data); err != nil {
		a.log.Warn("publish flag message failed",
			slog.String("run_id", payload.RunID),
			slog.Any("err", err))
		return
	}
	a.log.Info("queued flag for submission",
		slog.String("run_id", payload.RunID),
		slog.String("flag_id", message.FlagID))
}

func (a *Agent) runnerLabels() map[string]string {
	labels := cloneLabels(a.baseLabels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels["broker"] = a.brokerStatus
	if a.hostname != "" {
		labels["hostname"] = a.hostname
	}
	if instance := a.currentInstanceID(); instance != "" {
		labels["instance"] = instance
	}
	return labels
}

func (a *Agent) heartbeatConfig() map[string]string {
	cfg := make(map[string]string)
	add := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		cfg[key] = value
	}
	if ttl := a.cfg.Runner.ArtifactCacheTTL; ttl > 0 {
		add("RUNNER_ARTIFACT_CACHE_TTL", ttl.String())
	}
	if dir := a.cfg.Runner.ArtifactCacheDir; dir != "" {
		add("RUNNER_ARTIFACT_CACHE_DIR", dir)
	}
	if maxBytes := a.cfg.Runner.ArtifactCacheMaxBytes; maxBytes > 0 {
		add("RUNNER_ARTIFACT_CACHE_MAX_BYTES", strconv.FormatInt(maxBytes, 10))
	}
	if root := a.cfg.Runner.WorkspaceRoot; root != "" {
		add("RUNNER_WORKSPACE_ROOT", root)
	}
	if interval := a.cfg.Runner.HeartbeatInterval; interval > 0 {
		add("RUNNER_HEARTBEAT_INTERVAL", interval.String())
	}
	if flush := a.cfg.Runner.LogFlushInterval; flush > 0 {
		add("RUNNER_LOG_FLUSH_INTERVAL", flush.String())
	}
	if limit := a.cfg.Runner.LogOutputLimit; limit > 0 {
		add("RUNNER_LOG_OUTPUT_LIMIT", strconv.Itoa(limit))
	}
	if timeout := a.cfg.Runner.DefaultTimeout; timeout > 0 {
		add("RUNNER_DEFAULT_TIMEOUT", timeout.String())
	}
	if chroot := a.cfg.Runner.ChrootPath; chroot != "" {
		add("RUNNER_CHROOT_PATH", chroot)
	}
	if retry := a.cfg.RunQueue.RetryDelay; retry >= 0 {
		add("RUN_QUEUE_RETRY_DELAY", retry.String())
	}
	if visibility := a.cfg.RunQueue.Visibility; visibility > 0 {
		add("RUN_QUEUE_VISIBILITY", visibility.String())
	}
	if queueName := a.runQueueName(); queueName != "" {
		add("RUN_QUEUE_NAME", queueName)
	}
	if len(a.cfg.Runner.WarningPatterns) > 0 {
		add("RUNNER_WARNING_PATTERNS", strings.Join(a.cfg.Runner.WarningPatterns, ","))
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func cloneLabels(src map[string]string) map[string]string {
	if src == nil {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type artifactPayload struct {
	inline []byte
	file   *os.File
	size   int64
}

func (a artifactPayload) open() (io.ReaderAt, int64, func(), error) {
	if len(a.inline) > 0 {
		reader := bytes.NewReader(a.inline)
		return reader, int64(len(a.inline)), func() {}, nil
	}
	if a.file != nil {
		if _, err := a.file.Seek(0, io.SeekStart); err != nil {
			return nil, 0, func() {}, err
		}
		return a.file, a.size, func() { a.file.Close() }, nil
	}
	return nil, 0, func() {}, fmt.Errorf("empty artifact payload")
}

type cachedArtifact struct {
	path string
	size int64
}

type artifactCache struct {
	ttl       time.Duration
	maxBytes  int64
	baseDir   string
	log       *slog.Logger
	mu        sync.Mutex
	items     map[string]artifactCacheEntry
	totalSize int64
}

type artifactCacheEntry struct {
	path       string
	size       int64
	expires    time.Time
	lastAccess time.Time
}

func newArtifactCache(dir string, ttl time.Duration, maxBytes int64, log *slog.Logger) (*artifactCache, error) {
	dir = strings.TrimSpace(dir)
	if ttl <= 0 || maxBytes <= 0 || dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact cache dir: %w", err)
	}
	return &artifactCache{
		ttl:      ttl,
		maxBytes: maxBytes,
		baseDir:  dir,
		log:      log,
		items:    make(map[string]artifactCacheEntry),
	}, nil
}

func (c *artifactCache) get(key string) (cachedArtifact, bool) {
	if c == nil || key == "" {
		return cachedArtifact{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked()

	entry, ok := c.items[key]
	if !ok {
		return cachedArtifact{}, false
	}
	if _, err := os.Stat(entry.path); err != nil {
		delete(c.items, key)
		c.totalSize -= entry.size
		return cachedArtifact{}, false
	}
	entry.lastAccess = time.Now()
	c.items[key] = entry
	return cachedArtifact{path: entry.path, size: entry.size}, true
}

func (c *artifactCache) store(key string, data []byte) (cachedArtifact, error) {
	if c == nil || key == "" || len(data) == 0 {
		return cachedArtifact{}, fmt.Errorf("invalid cache store request")
	}
	if int64(len(data)) > c.maxBytes {
		return cachedArtifact{}, fmt.Errorf("artifact exceeds cache capacity")
	}
	if err := os.MkdirAll(c.baseDir, 0o755); err != nil {
		return cachedArtifact{}, fmt.Errorf("ensure cache dir: %w", err)
	}
	path := filepath.Join(c.baseDir, fmt.Sprintf("%s.zip", key))
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return cachedArtifact{}, fmt.Errorf("write cache temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return cachedArtifact{}, fmt.Errorf("rename cache file: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return cachedArtifact{}, fmt.Errorf("stat cache file: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.items[key]; ok {
		c.totalSize -= entry.size
		if entry.path != path {
			os.Remove(entry.path)
		}
	}

	now := time.Now()
	entry := artifactCacheEntry{
		path:       path,
		size:       info.Size(),
		expires:    now.Add(c.ttl),
		lastAccess: now,
	}
	c.items[key] = entry
	c.totalSize += entry.size
	c.trimLocked()

	if entry, ok := c.items[key]; ok {
		return cachedArtifact{path: entry.path, size: entry.size}, nil
	}
	return cachedArtifact{}, fmt.Errorf("cache entry evicted")
}

func (c *artifactCache) remove(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[key]; ok {
		delete(c.items, key)
		c.totalSize -= entry.size
		os.Remove(entry.path)
	}
}

func (c *artifactCache) trimLocked() {
	if c.maxBytes <= 0 {
		return
	}
	for c.totalSize > c.maxBytes {
		var oldestKey string
		var oldestEntry artifactCacheEntry
		for key, entry := range c.items {
			if oldestKey == "" || entry.lastAccess.Before(oldestEntry.lastAccess) {
				oldestKey = key
				oldestEntry = entry
			}
		}
		if oldestKey == "" {
			break
		}
		delete(c.items, oldestKey)
		c.totalSize -= oldestEntry.size
		os.Remove(oldestEntry.path)
	}
}

func (c *artifactCache) evictExpiredLocked() {
	if c.ttl <= 0 {
		return
	}
	now := time.Now()
	for key, entry := range c.items {
		if now.After(entry.expires) {
			delete(c.items, key)
			c.totalSize -= entry.size
			os.Remove(entry.path)
		}
	}
}

func verifyArtifactBytesMD5(expected string, data []byte) error {
	expected = normalizeMD5(expected)
	if expected == "" {
		return nil
	}
	sum := md5.Sum(data)
	actual := hex.EncodeToString(sum[:])
	if actual != expected {
		return fmt.Errorf("artifact md5 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func verifyArtifactFileMD5(expected string, file *os.File) error {
	expected = normalizeMD5(expected)
	if expected == "" || file == nil {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek artifact file: %w", err)
	}
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash artifact file: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind artifact file: %w", err)
	}
	if actual != expected {
		return fmt.Errorf("artifact md5 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func normalizeMD5(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func (a *Agent) prepareWorkspace(runID string, artifact artifactPayload) (string, string, error) {
	base := a.cfg.Runner.WorkspaceRoot
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace root: %w", err)
	}

	dir, err := os.MkdirTemp(base, fmt.Sprintf("run-%s-", sanitize(runID)))
	if err != nil {
		return "", "", fmt.Errorf("create temp dir: %w", err)
	}

	reader, size, closeFn, err := artifact.open()
	if err != nil {
		return "", "", fmt.Errorf("open artifact: %w", err)
	}
	defer closeFn()
	if err := unzipFromReader(dir, reader, size); err != nil {
		return "", "", err
	}

	rel := strings.TrimPrefix(dir, a.cfg.Runner.ChrootPath)
	if rel == dir {
		return "", "", fmt.Errorf("workspace not under chroot: %s", dir)
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return dir, rel, nil
}

func (a *Agent) execute(ctx context.Context, relPath string, ip string, port int) ([]byte, []byte, error) {
	portStr := strconv.Itoa(port)
	cmd := exec.CommandContext(ctx,
		"chroot",
		a.cfg.Runner.ChrootPath,
		"/bin/sh",
		"-c",
		"cd \"$1\" && ./run.sh \"$2\" \"$3\"",
		"runner",
		relPath,
		ip,
		portStr,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "RUNNER_IP="+ip, "RUNNER_PORT="+portStr)
	go func() {
		<-ctx.Done()
		terminateProcessGroup(cmd)
	}()

	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}

func (a *Agent) extractFlag(workspace string, stdout []byte, payload jobs.JobPayload) (string, string, error) {
	flagPath := filepath.Join(workspace, payload.FlagFileName(a.cfg.Runner.FlagFileName))
	if data, err := os.ReadFile(flagPath); err == nil {
		flag := strings.TrimSpace(string(data))
		if flag != "" {
			return flag, "file", nil
		}
	}

	regexPattern := payload.FlagRegex
	if regexPattern == "" {
		regexPattern = a.cfg.Runner.DefaultFlagRegex
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return "", "", fmt.Errorf("compile flag regex: %w", err)
	}

	match := re.Find(stdout)
	if match == nil {
		return "", "", errors.New("flag not found in stdout or file")
	}
	return string(match), "stdout", nil
}

func unzipFromReader(dir string, reader io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	for _, file := range zr.File {
		if err := writeZipEntry(dir, file); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(base string, file *zip.File) error {
	targetPath := filepath.Join(base, file.Name)

	if !strings.HasPrefix(targetPath, filepath.Clean(base)+string(os.PathSeparator)) {
		return fmt.Errorf("zip entry escapes workspace: %s", file.Name)
	}

	if file.FileInfo().IsDir() {
		return os.MkdirAll(targetPath, file.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return err
	}
	return nil
}

func sanitize(value string) string {
	if value == "" {
		return "run"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(value)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *Agent) supportsTags(required []string) bool {
	if len(required) == 0 {
		return true
	}
	if len(a.runnerTagsSet) == 0 {
		return false
	}
	for _, tag := range required {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value == "" {
			continue
		}
		if _, ok := a.runnerTagsSet[value]; !ok {
			return false
		}
	}
	return true
}
