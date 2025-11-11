package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"log/slog"

	"awdinfra/internal/config"
)

// ControlPanelClient performs authenticated requests against the control panel API.
type ControlPanelClient struct {
	baseURL *url.URL
	secret  string
	client  *http.Client
	log     *slog.Logger
}

const runnerSecretHeader = "X-Runner-Secret"

// APIError captures non-2xx responses from the control panel.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("control panel error (%d)", e.StatusCode)
	}
	return fmt.Sprintf("control panel error (%d): %s", e.StatusCode, e.Message)
}

// NewControlPanelClient constructs a client from runner configuration.
func NewControlPanelClient(cfg config.Config, log *slog.Logger) (*ControlPanelClient, error) {
	if cfg.Runner.ControlPanelURL == "" {
		return nil, fmt.Errorf("control panel url is empty")
	}
	base, err := url.Parse(cfg.Runner.ControlPanelURL)
	if err != nil {
		return nil, fmt.Errorf("invalid control panel url: %w", err)
	}
	secret := cfg.RunnerSecret
	if secret == "" {
		return nil, fmt.Errorf("runner secret is empty")
	}
	timeout := cfg.Runner.ControlPanelTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	client := &http.Client{
		Timeout: timeout,
	}
	return &ControlPanelClient{
		baseURL: base,
		secret:  secret,
		client:  client,
		log:     log,
	}, nil
}

type registerRunnerRequest struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Hostname    string            `json:"hostname"`
	Secret      string            `json:"secret"`
	Tags        []string          `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	MaxParallel int               `json:"maxParallel"`
	Status      string            `json:"status"`
	Config      map[string]string `json:"config,omitempty"`
}

type heartbeatRequest struct {
	ID          string            `json:"id"`
	Secret      string            `json:"secret"`
	Status      string            `json:"status"`
	Type        string            `json:"type,omitempty"`
	Hostname    string            `json:"hostname,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	MaxParallel int               `json:"maxParallel,omitempty"`
	ActiveJobs  int               `json:"activeJobs"`
	Config      map[string]string `json:"config,omitempty"`
}

type runStartRequest struct {
	Secret   string `json:"secret"`
	RunnerID string `json:"runnerId"`
}

type runFinishRequest struct {
	Secret     string `json:"secret"`
	RunnerID   string `json:"runnerId"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	FlagValue  string `json:"flagValue,omitempty"`
	FlagSource string `json:"flagSource,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
}

// RunResult communicates the final status of an exploit execution.
type RunResult struct {
	Status     string
	Error      string
	FlagValue  string
	FlagSource string
	Stdout     string
	Stderr     string
}

func (c *ControlPanelClient) RegisterRunner(ctx context.Context, req registerRunnerRequest) error {
	req.Secret = c.secret
	req.Status = "online"
	return c.post(ctx, "/api/v1/runners/register", req)
}

func (c *ControlPanelClient) Heartbeat(ctx context.Context, req heartbeatRequest) error {
	req.Secret = c.secret
	return c.post(ctx, "/api/v1/runners/heartbeat", req)
}

func (c *ControlPanelClient) MarkRunStarted(ctx context.Context, runID string, runnerID string) error {
	if c.baseURL == nil {
		return fmt.Errorf("control panel client not configured")
	}
	req := runStartRequest{
		RunnerID: runnerID,
		Secret:   c.secret,
	}
	return c.post(ctx, fmt.Sprintf("/api/v1/runs/%s/start", strings.TrimSpace(runID)), req)
}

func (c *ControlPanelClient) CompleteRun(ctx context.Context, runID string, runnerID string, result RunResult) error {
	if c.baseURL == nil {
		return fmt.Errorf("control panel client not configured")
	}
	req := runFinishRequest{
		RunnerID:   runnerID,
		Status:     result.Status,
		Error:      result.Error,
		FlagValue:  result.FlagValue,
		FlagSource: result.FlagSource,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		Secret:     c.secret,
	}
	return c.post(ctx, fmt.Sprintf("/api/v1/runs/%s/finish", strings.TrimSpace(runID)), req)
}

// RunInfo represents minimal run metadata fetched from the control panel.
type RunInfo struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	RunnerID string `json:"runnerId"`
}

func (c *ControlPanelClient) GetRun(ctx context.Context, runID string) (RunInfo, error) {
	if c.baseURL == nil {
		return RunInfo{}, fmt.Errorf("control panel client not configured")
	}
	u := *c.baseURL
	rel := strings.TrimPrefix(fmt.Sprintf("/api/v1/runs/%s", strings.TrimSpace(runID)), "/")
	u.Path = path.Join(c.baseURL.Path, rel)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return RunInfo{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set(runnerSecretHeader, c.secret)

	resp, err := c.client.Do(req)
	if err != nil {
		return RunInfo{}, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return RunInfo{}, &APIError{StatusCode: resp.StatusCode, Message: message}
	}

	var payload struct {
		Run RunInfo `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return RunInfo{}, fmt.Errorf("decode run payload: %w", err)
	}
	return payload.Run, nil
}

func (c *ControlPanelClient) DownloadArtifact(ctx context.Context, artifactPath string) ([]byte, error) {
	if c.baseURL == nil {
		return nil, fmt.Errorf("control panel client not configured")
	}
	u := *c.baseURL
	rel := strings.TrimPrefix(artifactPath, "/")
	u.Path = path.Join(c.baseURL.Path, rel)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set(runnerSecretHeader, c.secret)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d when downloading artifact", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read artifact body: %w", err)
	}
	return data, nil
}

func (c *ControlPanelClient) post(ctx context.Context, endpoint string, payload any) error {
	if c.baseURL == nil {
		return fmt.Errorf("control panel client not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	u := *c.baseURL
	rel := strings.TrimPrefix(endpoint, "/")
	u.Path = path.Join(c.baseURL.Path, rel)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	message := strings.TrimSpace(string(respBody))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    message,
	}
}
