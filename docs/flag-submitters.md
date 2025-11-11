# Flag Submitter Extension Contract

Flag submitters are pluggable adapters that translate generic flag events into platform-specific API calls. Submitters authenticate to the Control Plane using `SUBMITTER_SECRET`, register/heartbeat via REST, and call `/api/v1/flags/{id}/submit/start|finish` for each flag. The default Go service ships with an HTTP plugin, but you can implement your own adapters as long as they respect the protocol described below.

## Control Plane Contract

1. **Register**: `POST /api/v1/submitters/register` with payload `{id,type,hostname,secret,concurrency}`. Control Plane persists the submitter record.
2. **Heartbeat**: `POST /api/v1/submitters/heartbeat` every ~10s，附带 `recentThroughput`、`avgLatencyMs`、`pressure`、`inFlight`、`pending` 等指标，便于 UI 展示和超时清理。
3. **Heartbeat metrics**：payload 需额外上报 `recentThroughput`、`avgLatencyMs`、`pressure`、`windowSeconds`（采样窗口，秒）、`latencySumSeconds`（窗口内提交耗时总和）以及 `processedCount`。Control Plane 会基于这些数据计算单实例与整体压力。
4. **Flag lifecycle**:
   - `POST /api/v1/flags/{id}/submit/start`，Body `{secret, submitterId}`。成功时返回 flag 元数据（value、challengeId、targetId、runId）。若 flag 已完成或不存在则返回 404/409，submitter 应 Ack 并跳过。
   - `POST /api/v1/flags/{id}/submit/finish`，Body `{secret, submitterId, status, statusCode, message, latencyMs, retryAfterSeconds, payload}`，Control Plane 根据 status 更新 `flags`、追加 `submission_attempts`、日志化，并返回 `{flag:{...}, retry:bool}`。当 `retry=true` 时 submitter 应 `Nack` 当前队列消息并按 `retryAfterSeconds`（或本地策略）延迟重试。

所有 HTTP 请求都以 JSON 传输，`secret` 必须与 `SUBMITTER_SECRET` 一致。

## Go 插件接口

Submitter binaries are loaded at runtime by the `flag-submitter` service and must implement the following Go interface:

```go
type Submitter interface {
    // ID returns a stable identifier (e.g. "ctfzone-http") that matches Control Plane configuration.
    ID() string

    // Submit executes a single submission attempt. The provided context carries
    // cancellation, deadlines, and tracing metadata. The flag value and the originating
    // run metadata are supplied through the payload.
    Submit(ctx context.Context, payload SubmissionRequest) (SubmissionResult, error)
}
```

where:

```go
type SubmissionRequest struct {
    Flag       string
    Challenge  string
    Target     string
    RunID      string
    Metadata   map[string]any // control-plane supplied (tokens, cookies, etc.)
}

type SubmissionResult struct {
    Status   SubmissionStatus
    Message  string        // free-form text, surfaced in UI/logs
    NextTry  time.Duration // optional override for retry backoff
}

type SubmissionStatus string

const (
    SubmissionStatusAccepted   SubmissionStatus = "accepted"
    SubmissionStatusFailed     SubmissionStatus = "failed"      // permanent failure (invalid payload, etc.)
    SubmissionStatusDuplicated SubmissionStatus = "duplicated"  // duplicated flag submission
    SubmissionStatusUnknown    SubmissionStatus = "unknown"     // transient error; scheduler will retry
)
```

### Return Semantics

- `Accepted`: the flag has been confirmed by the remote platform; it will be marked as accepted and removed from retry queues.
- `Failed`: the remote platform definitively refused the flag (invalid payload, already expired, etc.). No further retries will be scheduled unless an operator manually requeues it.
- `Duplicated`: the remote platform accepted the request but reports the flag was already submitted. The flag will be marked failed and no further retries scheduled.
- `Unknown`: the submission failed for a recoverable reason (network, rate limit). The scheduler will schedule another attempt after the default backoff or the optional `NextTry` delay.

Returning an error from `Submit` is equivalent to `Unknown`; the framework records the error text and applies exponential backoff.

### Packaging Guidance

- Compile submitters as Go plugins (`.so`) or expose them over gRPC/HTTP; register them via configuration.
- Each submitter is loaded with Control Plane supplied credentials; avoid embedding secrets inside the artifact.
- Ensure implementations are idempotent—duplicate invocation may occur due to retries after partial failures.
