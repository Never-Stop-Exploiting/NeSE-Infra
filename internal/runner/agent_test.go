package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
)

type noopBroker struct{}

func (noopBroker) Publish(context.Context, string, []byte) (string, error) { return "", nil }
func (noopBroker) Claim(context.Context, string, string, string, int, time.Duration) ([]queue.Message, error) {
	return nil, queue.ErrNoMessages
}
func (noopBroker) Ack(context.Context, string, string, string) error { return nil }
func (noopBroker) Nack(context.Context, string, string, string, time.Duration) error {
	return nil
}

func TestPrepareWorkspaceCreatesChrootedDirectory(t *testing.T) {
	chroot := t.TempDir()
	workspaceRoot := filepath.Join(chroot, "tmp")

	cfg := config.Config{
		Runner: config.RunnerConfig{
			ChrootPath:     chroot,
			WorkspaceRoot:  workspaceRoot,
			FlagFileName:   "flag",
			DefaultTimeout: 5 * time.Second,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	agent := New(cfg, logger, noopBroker{})

	artifact := newZipArtifact(map[string]string{
		"run.sh": "#!/bin/sh\necho ok\n",
	})

	dir, rel, err := agent.prepareWorkspace("run-123", artifact)
	if err != nil {
		t.Fatalf("prepareWorkspace failed: %v", err)
	}
	defer os.RemoveAll(dir)

	if !strings.HasPrefix(dir, workspaceRoot) {
		t.Fatalf("workspace %s not within %s", dir, workspaceRoot)
	}
	if rel == "" || rel[0] != '/' {
		t.Fatalf("relative path expected slash prefix, got %q", rel)
	}

	runScript := filepath.Join(dir, "run.sh")
	if _, err := os.Stat(runScript); err != nil {
		t.Fatalf("run.sh not extracted: %v", err)
	}
}

func TestExtractFlagPrefersFile(t *testing.T) {
	chroot := t.TempDir()
	workspaceRoot := filepath.Join(chroot, "tmp")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cfg := config.Config{
		Runner: config.RunnerConfig{
			ChrootPath:       chroot,
			WorkspaceRoot:    workspaceRoot,
			FlagFileName:     "flag",
			DefaultFlagRegex: `FLAG\{[A-Z]+\}`,
			DefaultTimeout:   5 * time.Second,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	agent := New(cfg, logger, noopBroker{})

	workspace := filepath.Join(workspaceRoot, "run-test")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "flag"), []byte(" FLAG{SUCCESS} \n"), 0o644); err != nil {
		t.Fatalf("write flag file: %v", err)
	}

	payload := jobs.JobPayload{
		RunID:          "run-test",
		TargetIP:       "1.2.3.4",
		ArtifactBase64: "Zg==", // dummy to satisfy struct
	}
	flag, err := agent.extractFlag(workspace, []byte{}, payload)
	if err != nil {
		t.Fatalf("extractFlag failed: %v", err)
	}
	if flag != "FLAG{SUCCESS}" {
		t.Fatalf("expected flag from file, got %q", flag)
	}
}

func TestExtractFlagFallsBackToRegex(t *testing.T) {
	chroot := t.TempDir()
	workspaceRoot := filepath.Join(chroot, "tmp")
	cfg := config.Config{
		Runner: config.RunnerConfig{
			ChrootPath:       chroot,
			WorkspaceRoot:    workspaceRoot,
			FlagFileName:     "flag",
			DefaultFlagRegex: `FLAG\{[0-9A-Z]+\}`,
			DefaultTimeout:   5 * time.Second,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	agent := New(cfg, logger, noopBroker{})

	payload := jobs.JobPayload{
		RunID:          "run-regex",
		TargetIP:       "1.2.3.4",
		ArtifactBase64: "Zg==",
	}

	flag, err := agent.extractFlag(filepath.Join(workspaceRoot, "run-regex"), []byte("...FLAG{ABC123}..."), payload)
	if err != nil {
		t.Fatalf("extractFlag failed: %v", err)
	}
	if flag != "FLAG{ABC123}" {
		t.Fatalf("expected regex flag, got %q", flag)
	}
}

func newZipArtifact(entries map[string]string) []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range entries {
		f, _ := writer.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		})
		_, _ = f.Write([]byte(content))
	}
	_ = writer.Close()
	return buf.Bytes()
}

func TestEnsureRunScriptCreatesFromPayload(t *testing.T) {
	agent := Agent{
		cfg: config.Config{},
	}
	workspace := t.TempDir()
	payload := jobs.JobPayload{RunScript: "#!/bin/sh\necho hello\n"}
	if err := agent.ensureRunScript(workspace, payload); err != nil {
		t.Fatalf("ensureRunScript failed: %v", err)
	}
	runPath := filepath.Join(workspace, "run.sh")
	data, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatalf("run.sh not created: %v", err)
	}
	if !strings.Contains(string(data), "echo hello") {
		t.Fatalf("unexpected run.sh content: %q", string(data))
	}
}

func TestEnsureRunScriptUsesEntrypoint(t *testing.T) {
	agent := Agent{}
	workspace := t.TempDir()
	payload := jobs.JobPayload{Entrypoint: "python exp.py"}
	if err := agent.ensureRunScript(workspace, payload); err != nil {
		t.Fatalf("ensureRunScript failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "run.sh"))
	if err != nil {
		t.Fatalf("run.sh not created: %v", err)
	}
	if !strings.Contains(string(data), "python exp.py \"$1\" \"$2\"") {
		t.Fatalf("entrypoint not rendered: %q", string(data))
	}
}
