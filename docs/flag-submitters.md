# Flag Submitter Extension Contract

Flag submitters are pluggable adapters that translate generic flag events into platform-specific API calls. Submitter binaries are loaded at runtime by the `flag-submitter` service and must implement the following Go interface:

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
    SubmissionStatusAccepted SubmissionStatus = "accepted"
    SubmissionStatusRejected SubmissionStatus = "rejected"   // permanent failure (e.g. already submitted)
    SubmissionStatusRetry    SubmissionStatus = "retry"      // transient error; scheduler will retry
)
```

### Return Semantics

- `Accepted`: the flag has been confirmed by the remote platform; it will be marked as accepted and removed from retry queues.
- `Rejected`: the remote platform definitively refused the flag (duplicate/invalid). No further retries will be scheduled unless an operator manually requeues it.
- `Retry`: the submission failed for a recoverable reason (network, rate limit). The scheduler will schedule another attempt after the default backoff or the optional `NextTry` delay.

Returning an error from `Submit` is equivalent to `Retry`; the framework records the error text and applies exponential backoff.

### Packaging Guidance

- Compile submitters as Go plugins (`.so`) or expose them over gRPC/HTTP; register them via configuration.
- Each submitter is loaded with Control Plane supplied credentials; avoid embedding secrets inside the artifact.
- Ensure implementations are idempotent—duplicate invocation may occur due to retries after partial failures.
