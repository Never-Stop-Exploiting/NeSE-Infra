package runner

import (
	"archive/zip"
	"bytes"
	"context"
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
	"sync/atomic"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/submitter"

	"golang.org/x/sync/errgroup"
)

// Agent consumes run jobs and executes exploits in an isolated workspace.
type Agent struct {
	cfg           config.Config
	log           *slog.Logger
	broker        queue.Broker
	controlClient *ControlPlaneClient
	hostname      string
	activeJobs    atomic.Int32
	lastHeartbeat time.Time
	brokerStatus  string
	baseLabels    map[string]string
	runnerTagsSet map[string]struct{}
}

var errJobDiscard = errors.New("runner job discarded")

// New constructs an Agent.
func New(cfg config.Config, log *slog.Logger, broker queue.Broker, control *ControlPlaneClient, brokerStatus string) *Agent {
	tagSet := make(map[string]struct{}, len(cfg.Runner.Tags))
	for _, tag := range cfg.Runner.Tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if value != "" {
			tagSet[value] = struct{}{}
		}
	}
	return &Agent{
		cfg:           cfg,
		log:           log,
		broker:        broker,
		controlClient: control,
		brokerStatus:  brokerStatus,
		baseLabels:    map[string]string{"broker": brokerStatus},
		runnerTagsSet: tagSet,
	}
}

// Run starts the main event loop until context cancellation.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.registerSelf(ctx); err != nil {
		return err
	}

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

		available := maxParallel - len(sema)
		if available <= 0 {
			time.Sleep(time.Millisecond * 50)
			continue
		}

		messages, err := a.broker.Claim(ctx, a.cfg.RunQueue.Name, group, runnerID(a.cfg.Runner.ID), available, block)
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
			sema <- struct{}{}
			go func() {
				defer func() { <-sema }()
				a.log.Debug("received job", slog.String("id", msgCopy.ID))
				a.activeJobs.Add(1)
				defer a.activeJobs.Add(-1)
				if err := a.handleMessage(ctx, msgCopy); err != nil {
					if errors.Is(err, errJobDiscard) {
						a.log.Info("job discarded", slog.String("id", msgCopy.ID))
						if ackErr := a.broker.Ack(ctx, a.cfg.RunQueue.Name, group, msgCopy.ID); ackErr != nil {
							a.log.Warn("failed to ack discarded job", slog.Any("err", ackErr), slog.String("id", msgCopy.ID))
						}
						return
					}
					a.log.Error("job failed", slog.Any("err", err), slog.String("id", msgCopy.ID))
					if err := a.broker.Nack(ctx, a.cfg.RunQueue.Name, group, msgCopy.ID, retryDelay); err != nil {
						a.log.Warn("failed to nack message", slog.Any("err", err), slog.String("id", msgCopy.ID))
					}
					return
				}
				if err := a.broker.Ack(ctx, a.cfg.RunQueue.Name, group, msgCopy.ID); err != nil {
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
	a.log.Info("workspace prepared",
		slog.String("run_id", runID),
		slog.String("workspace", workspace),
		slog.String("relative_path", relPath))
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
			a.log.Info("run canceled by control plane",
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
			slog.String("stderr", trimOutputString(stderr)))
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

	result := RunResult{
		Status:     status,
		Error:      errorMessage,
		FlagValue:  flagValue,
		FlagSource: flagSource,
		Stdout:     trimOutputString(stdout),
		Stderr:     trimOutputString(stderr),
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
		Status:      "online",
		Type:        a.cfg.Runner.Type,
		Hostname:    a.hostname,
		Tags:        a.cfg.Runner.Tags,
		MaxParallel: a.cfg.Runner.MaxParallelJobs,
		ActiveJobs:  int(a.activeJobs.Load()),
		Labels:      a.runnerLabels(),
	}
	if err := a.controlClient.Heartbeat(hbCtx, req); err != nil {
		a.log.Warn("failed to send heartbeat", slog.Any("err", err))
		return
	}
	a.lastHeartbeat = time.Now().UTC()
}

func (a *Agent) registerSelf(ctx context.Context) error {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = runnerID(a.cfg.Runner.ID)
	}
	a.hostname = host
	labels := map[string]string{
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"app":      a.cfg.AppName,
		"hostname": host,
		"broker":   a.brokerStatus,
	}
	a.baseLabels = cloneLabels(labels)
	if a.controlClient == nil {
		a.log.Warn("control plane client disabled; skipping registration",
			slog.String("runner_id", runnerID(a.cfg.Runner.ID)))
		return nil
	}
	registerCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	req := registerRunnerRequest{
		ID:          runnerID(a.cfg.Runner.ID),
		Type:        a.cfg.Runner.Type,
		Hostname:    host,
		Tags:        a.cfg.Runner.Tags,
		Labels:      labels,
		MaxParallel: a.cfg.Runner.MaxParallelJobs,
	}
	if err := a.controlClient.RegisterRunner(registerCtx, req); err != nil {
		return fmt.Errorf("register runner with control plane: %w", err)
	}
	a.log.Info("registered runner with control plane",
		slog.String("runner_id", req.ID),
		slog.String("control_plane", a.controlClient.baseURL.String()))
	a.sendHeartbeat(ctx)
	return nil
}

func (a *Agent) resolveArtifact(ctx context.Context, payload jobs.JobPayload) ([]byte, error) {
	if data, err := payload.ArtifactBytes(); err == nil {
		return data, nil
	} else if payload.ArtifactURI == "" {
		return nil, err
	}
	if a.controlClient == nil {
		return nil, fmt.Errorf("artifact uri provided but control plane client is not configured")
	}
	downloadCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	data, err := a.controlClient.DownloadArtifact(downloadCtx, payload.ArtifactURI)
	if err != nil {
		return nil, fmt.Errorf("download artifact: %w", err)
	}
	return data, nil
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

func (a *Agent) ensureRunScript(workspace string, payload jobs.JobPayload) error {
	runPath := filepath.Join(workspace, "run.sh")
	if _, err := os.Stat(runPath); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
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
	if err := a.controlClient.MarkRunStarted(startCtx, payload.RunID, runnerID); err != nil {
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
	return a.controlClient.CompleteRun(finishCtx, payload.RunID, runnerID, result)
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

func trimOutputString(data []byte) string {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	const limit = 2048
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
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
	return labels
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

func (a *Agent) prepareWorkspace(runID string, artifact []byte) (string, string, error) {
	base := a.cfg.Runner.WorkspaceRoot
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace root: %w", err)
	}

	dir, err := os.MkdirTemp(base, fmt.Sprintf("run-%s-", sanitize(runID)))
	if err != nil {
		return "", "", fmt.Errorf("create temp dir: %w", err)
	}

	if err := unzipInto(dir, artifact); err != nil {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "RUNNER_IP="+ip, "RUNNER_PORT="+portStr)

	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
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

func unzipInto(dir string, artifact []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	for _, file := range reader.File {
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
