package submitter

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

// ControlPanelClient handles submitter interactions with the control panel REST API.
type ControlPanelClient struct {
	baseURL *url.URL
	secret  string
	client  *http.Client
	log     *slog.Logger
}

// NewControlPanelClient initializes a client using submitter configuration.
func NewControlPanelClient(cfg config.Config, log *slog.Logger) (*ControlPanelClient, error) {
	baseURL := cfg.FlagSubmitter.ControlPanelURL
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("control panel url is empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid control panel url: %w", err)
	}
	secret := cfg.SubmitterSecret
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("submitter secret is empty")
	}
	httpClient := &http.Client{Timeout: time.Second * 10}
	return &ControlPanelClient{
		baseURL: parsed,
		secret:  secret,
		client:  httpClient,
		log:     log,
	}, nil
}

type registerSubmitterRequest struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Hostname    string            `json:"hostname"`
	Secret      string            `json:"secret"`
	Concurrency int               `json:"concurrency"`
	Labels      map[string]string `json:"labels,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Status      string            `json:"status,omitempty"`
}

type heartbeatSubmitterRequest struct {
	ID                string            `json:"id"`
	Secret            string            `json:"secret"`
	Status            string            `json:"status"`
	Type              string            `json:"type,omitempty"`
	Hostname          string            `json:"hostname,omitempty"`
	Concurrency       int               `json:"concurrency"`
	InFlight          int               `json:"inFlight"`
	Pending           int               `json:"pending"`
	RecentThroughput  float64           `json:"recentThroughput"`
	AvgLatencyMs      float64           `json:"avgLatencyMs"`
	Pressure          float64           `json:"pressure"`
	WindowSeconds     float64           `json:"windowSeconds"`
	LatencySumSeconds float64           `json:"latencySumSeconds"`
	ProcessedCount    int               `json:"processedCount"`
	Labels            map[string]string `json:"labels,omitempty"`
	Config            map[string]string `json:"config,omitempty"`
}

type submitStartRequest struct {
	Secret      string `json:"secret"`
	SubmitterID string `json:"submitterId"`
}

type submitStartResponse struct {
	Flag submitFlagInfo `json:"flag"`
}

type submitFlagInfo struct {
	ID          string `json:"id"`
	Value       string `json:"value"`
	ChallengeID string `json:"challengeId"`
	TargetID    string `json:"targetId"`
	RunID       string `json:"runId"`
	Status      string `json:"status"`
}

type submitFinishRequest struct {
	Secret            string         `json:"secret"`
	SubmitterID       string         `json:"submitterId"`
	Status            string         `json:"status"`
	StatusCode        int            `json:"statusCode"`
	Message           string         `json:"message"`
	LatencyMs         float64        `json:"latencyMs"`
	RetryAfterSeconds int            `json:"retryAfterSeconds"`
	Payload           map[string]any `json:"payload"`
}

type submitFinishResponse struct {
	Flag struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"flag"`
	Retry bool `json:"retry"`
}

// APIError captures control panel HTTP errors.
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

func (c *ControlPanelClient) Register(ctx context.Context, req registerSubmitterRequest) error {
	req.Secret = c.secret
	req.Status = "online"
	return c.post(ctx, "/api/v1/submitters/register", req, nil)
}

func (c *ControlPanelClient) Heartbeat(ctx context.Context, req heartbeatSubmitterRequest) error {
	req.Secret = c.secret
	return c.post(ctx, "/api/v1/submitters/heartbeat", req, nil)
}

func (c *ControlPanelClient) SubmitStart(ctx context.Context, flagID string, submitterID string) (submitFlagInfo, error) {
	var resp submitStartResponse
	req := submitStartRequest{
		Secret:      c.secret,
		SubmitterID: submitterID,
	}
	err := c.post(ctx, fmt.Sprintf("/api/v1/flags/%s/submit/start", strings.TrimSpace(flagID)), req, &resp)
	return resp.Flag, err
}

func (c *ControlPanelClient) SubmitFinish(ctx context.Context, flagID string, payload submitFinishRequest) (submitFinishResponse, error) {
	payload.Secret = c.secret
	var resp submitFinishResponse
	err := c.post(ctx, fmt.Sprintf("/api/v1/flags/%s/submit/finish", strings.TrimSpace(flagID)), payload, &resp)
	return resp, err
}

func (c *ControlPanelClient) post(ctx context.Context, endpoint string, payload any, dest any) error {
	if c.baseURL == nil {
		return fmt.Errorf("control panel client not configured")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	u := *c.baseURL
	rel := strings.TrimPrefix(endpoint, "/")
	u.Path = path.Join(c.baseURL.Path, rel)
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	if dest != nil {
		if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
