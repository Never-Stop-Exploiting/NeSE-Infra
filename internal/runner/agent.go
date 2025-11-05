package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
)

// Agent consumes run jobs and executes exploits in an isolated workspace.
type Agent struct {
	cfg    config.Config
	log    *slog.Logger
	broker queue.Broker
}

// New constructs an Agent.
func New(cfg config.Config, log *slog.Logger, broker queue.Broker) *Agent {
	return &Agent{
		cfg:    cfg,
		log:    log,
		broker: broker,
	}
}

// Run starts the main event loop until context cancellation.
func (a *Agent) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.cfg.Runner.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("runner shutting down")
			return ctx.Err()
		default:
			if err := a.poll(ctx); err != nil {
				if errors.Is(err, queue.ErrNoMessages) {
					time.Sleep(time.Millisecond * 200)
					continue
				}
				a.log.Error("poll failed", slog.Any("err", err))
				time.Sleep(time.Second)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.sendHeartbeat()
		default:
		}
	}
}

func (a *Agent) poll(ctx context.Context) error {
	group := runnerGroup(a.cfg.Runner.Type)
	count := max(1, a.cfg.Runner.MaxParallelJobs)
	block := a.cfg.Runner.HeartbeatInterval
	if block <= 0 {
		block = time.Second
	}
	messages, err := a.broker.Claim(ctx, a.cfg.RunQueue.Name, group, runnerID(a.cfg.Runner.ID), count, block)
	if err != nil {
		if errors.Is(err, queue.ErrNoMessages) {
			return nil
		}
		return err
	}

	sema := make(chan struct{}, count)
	var wg sync.WaitGroup
	retryDelay := a.cfg.RunQueue.RetryDelay
	if retryDelay <= 0 {
		retryDelay = time.Second * 5
	}

	for _, message := range messages {
		msgCopy := message
		wg.Add(1)
		sema <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sema }()
			a.log.Debug("received job", slog.String("id", msgCopy.ID))
			if err := a.handleMessage(ctx, msgCopy); err != nil {
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

	wg.Wait()
	return nil
}

func (a *Agent) handleMessage(ctx context.Context, msg queue.Message) error {
	payload, err := jobs.DecodeJobPayload(msg.Body)
	if err != nil {
		return err
	}

	artifact, err := payload.ArtifactBytes()
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(ctx, payload.Timeout(a.cfg.Runner.DefaultTimeout))
	defer cancel()

	workspace, relPath, err := a.prepareWorkspace(payload.RunID, artifact)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			a.log.Warn("failed to cleanup workspace", slog.Any("err", err), slog.String("workspace", workspace))
		}
	}()

	if err := a.ensureRunScript(workspace, payload); err != nil {
		return fmt.Errorf("prepare run script: %w", err)
	}

	stdout, stderr, err := a.execute(runCtx, relPath, payload.TargetIP, payload.TargetPort)
	if err != nil {
		return fmt.Errorf("execute run.sh: %w", err)
	}

	flagValue, flagErr := a.extractFlag(workspace, stdout, payload)
	if flagErr != nil {
		a.log.Warn("flag extraction failed",
			slog.String("run_id", payload.RunID),
			slog.Any("err", flagErr))
	} else {
		a.log.Info("flag captured",
			slog.String("run_id", payload.RunID),
			slog.String("flag", flagValue))
	}

	if len(stderr) > 0 {
		a.log.Debug("stderr output",
			slog.String("run_id", payload.RunID),
			slog.String("stderr", string(stderr)))
	}

	return nil
}

func (a *Agent) sendHeartbeat() {
	a.log.Info("runner heartbeat", slog.String("runner_id", a.cfg.Runner.ID))
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

func (a *Agent) extractFlag(workspace string, stdout []byte, payload jobs.JobPayload) (string, error) {
	flagPath := filepath.Join(workspace, payload.FlagFileName(a.cfg.Runner.FlagFileName))
	if data, err := os.ReadFile(flagPath); err == nil {
		flag := strings.TrimSpace(string(data))
		if flag != "" {
			return flag, nil
		}
	}

	regexPattern := payload.FlagRegex
	if regexPattern == "" {
		regexPattern = a.cfg.Runner.DefaultFlagRegex
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return "", fmt.Errorf("compile flag regex: %w", err)
	}

	match := re.Find(stdout)
	if match == nil {
		return "", errors.New("flag not found in stdout or file")
	}
	return string(match), nil
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
