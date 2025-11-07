package submitter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"awdinfra/internal/config"
)

type HTTPPlugin struct {
	endpoint string
	client   *http.Client
}

func NewHTTPPlugin(cfg config.FlagSubmitterConfig) Plugin {
	return &HTTPPlugin{
		endpoint: cfg.Endpoint,
		client:   &http.Client{Timeout: cfg.RequestTimeout},
	}
}

func (p *HTTPPlugin) ID() string { return "http" }

func (p *HTTPPlugin) Submit(ctx context.Context, req SubmissionRequest) SubmissionResult {
	body, err := json.Marshal(map[string]string{"flag": req.FlagValue})
	if err != nil {
		return SubmissionResult{Status: SubmissionStatusUnknown, Err: err, Message: err.Error()}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return SubmissionResult{Status: SubmissionStatusUnknown, Err: err, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return SubmissionResult{Status: SubmissionStatusUnknown, Err: err, Message: err.Error(), RetryAfter: ptrInt64(10)}
	}
	defer resp.Body.Close()

	result := SubmissionResult{StatusCode: resp.StatusCode, Message: resp.Status}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.Status = SubmissionStatusAccept
		result.Message = "success"
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		result.Status = SubmissionStatusFail
	default:
		result.Status = SubmissionStatusUnknown
		retry := int64(10)
		result.RetryAfter = &retry
	}
	return result
}

func ptrInt64(v int64) *int64 { return &v }
