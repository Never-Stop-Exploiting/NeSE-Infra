package scheduler

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
)

// EnqueueRun publishes a run work item onto the run queue.
func EnqueueRun(ctx context.Context, cfg config.Config, broker queue.Broker, item domain.RunWorkItem) error {
	if len(item.Artifact) == 0 {
		return fmt.Errorf("empty artifact for run %s", item.RunID)
	}

	payload := jobs.JobPayload{
		RunID:       item.RunID,
		ExploitID:   item.Exploit.ID,
		ExploitName: item.Exploit.Name,
		TargetID:    item.Target.ID,
		TargetIP:    item.Target.IP,
		TargetPort:  item.Target.Port,
		FlagRegex:   item.FlagRegex,
		FlagFile:    item.FlagFile,
		CreatedAt:   time.Now().UTC(),
	}
	if item.Exploit.Metadata != nil {
		if script, ok := item.Exploit.Metadata["run_script"].(string); ok {
			payload.RunScript = script
		}
		if entry, ok := item.Exploit.Metadata["entrypoint"].(string); ok {
			payload.Entrypoint = entry
		}
	}
	if item.Timeout > 0 {
		payload.TimeoutSeconds = int(item.Timeout.Seconds())
	}
	if len(item.Exploit.RunnerTags) > 0 {
		payload.RunnerTags = item.Exploit.RunnerTags
	}
	sum := md5.Sum(item.Artifact)
	payload.ArtifactMD5 = hex.EncodeToString(sum[:])
	payload.ArtifactURI = fmt.Sprintf("/api/v1/exploits/%s/artifact", item.Exploit.ID)

	body, err := jobs.MarshalJobPayload(payload)
	if err != nil {
		return err
	}

	_, err = broker.Publish(ctx, cfg.RunQueue.Name, body)
	return err
}
