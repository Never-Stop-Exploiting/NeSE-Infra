package jobs

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestMarshalAndDecodeJobPayload(t *testing.T) {
	payload := JobPayload{
		RunID:          "run-1",
		ExploitID:      "exp-1",
		ExploitName:    "sample",
		TargetID:       "target-1",
		TargetIP:       "10.0.0.1",
		TargetPort:     8080,
		TimeoutSeconds: 30,
		ArtifactBase64: base64.StdEncoding.EncodeToString([]byte("zip-bytes")),
		FlagRegex:      "FLAG{.*}",
		FlagFile:       "flag",
		RunScript:      "#!/bin/sh\necho hi\n",
		Entrypoint:     "python exp.py",
	}

	data, err := MarshalJobPayload(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	decoded, err := DecodeJobPayload(data)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded.RunID != payload.RunID {
		t.Fatalf("expected run id %q, got %q", payload.RunID, decoded.RunID)
	}
	if decoded.TimeoutSeconds != payload.TimeoutSeconds {
		t.Fatalf("expected timeout %d, got %d", payload.TimeoutSeconds, decoded.TimeoutSeconds)
	}
	if decoded.RunScript != payload.RunScript {
		t.Fatalf("expected run script preserved")
	}
	if decoded.Entrypoint != payload.Entrypoint {
		t.Fatalf("expected entrypoint preserved")
	}
	bytes, err := decoded.ArtifactBytes()
	if err != nil {
		t.Fatalf("artifact decode failed: %v", err)
	}
	if string(bytes) != "zip-bytes" {
		t.Fatalf("unexpected artifact bytes: %q", string(bytes))
	}
}

func TestMarshalJobPayloadValidation(t *testing.T) {
	_, err := MarshalJobPayload(JobPayload{
		RunID: "missing-artifact",
	})
	if err == nil {
		t.Fatalf("expected error due to missing fields")
	}
}

func TestTimeoutAndFlagDefaults(t *testing.T) {
	payload := JobPayload{
		RunID:          "run-2",
		TargetIP:       "10.0.0.2",
		ArtifactBase64: base64.StdEncoding.EncodeToString([]byte("data")),
	}

	timeout := payload.Timeout(10 * time.Second)
	if timeout != 10*time.Second {
		t.Fatalf("expected fallback timeout, got %v", timeout)
	}

	flagFile := payload.FlagFileName("flag.txt")
	if flagFile != "flag.txt" {
		t.Fatalf("expected default flag file, got %q", flagFile)
	}
}
