export interface Challenge {
  id: string;
  name: string;
  category?: string;
  serviceType?: string;
}

export interface Target {
  id: string;
  challengeId: string;
  teamName: string;
  ip: string;
  port: number;
  status?: string;
  concurrencyLimit: number;
  labels?: Record<string, string>;
}

export interface Exploit {
  id: string;
  challengeId: string;
  name: string;
  description?: string;
  enabled: boolean;
  runnerTags: string[];
  scheduleWeight: number;
  timeoutSeconds?: number;
  entrypoint?: string;
  runScript?: string;
  latestRevisionId?: string;
  latestArtifactDigest?: string;
  metadata?: Record<string, unknown>;
  updatedAt?: string;
}

export interface ExploitFile {
  name: string;
  size: number;
  md5: string;
  modTime?: string;
}

export interface ExploitRevision {
  id: string;
  exploitId: string;
  digest: string;
  createdAt: string;
  notes?: string;
  artifactName?: string;
}

export interface RunSummary {
  id: string;
  exploitId: string;
  exploitName?: string;
  targetId: string;
  targetLabel?: string;
  runnerId?: string;
  status: "pending" | "running" | "succeeded" | "failed" | "timeout" | "canceled";
  queuedAt: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface RunDetail extends RunSummary {
  stdout?: string;
  stderr?: string;
  flagId?: string;
  errorMessage?: string;
}

export interface FlagRecord {
  id: string;
  value: string;
  challengeId: string;
  targetId: string;
  status: "new" | "submitting" | "accepted" | "rejected" | "retry" | "dropped";
  submissionCount: number;
  attempts: SubmissionAttempt[];
}

export interface SubmissionAttempt {
  id: string;
  submitterId: string;
  statusCode: number;
  error?: string;
  submittedAt: string;
  nextRetryAt?: string;
}

export interface RunnerInfo {
  id: string;
  type: string;
  hostname?: string;
  tags?: string[];
  maxParallel: number;
  activeJobs: number;
  status: "online" | "offline" | "degraded";
  lastHeartbeat?: string;
  labels?: Record<string, string>;
}

export interface LogEntry {
  id: string;
  timestamp: string;
  level: "debug" | "info" | "warn" | "error";
  source: string;
  message: string;
  runId?: string;
  flagId?: string;
  fields?: Record<string, unknown>;
}

export interface SchedulerSettings {
  roundIntervalSeconds: number;
  roundIntervalMinutes: number;
  maxDispatchBatch: number;
  updatedAt?: string;
}

export interface TargetExploitStatus {
  exploitId: string;
  exploitName: string;
  enabled: boolean;
  defaultEnabled: boolean;
}

export interface TargetScheduleSummary {
  total: number;
  enabled: number;
  disabled: number;
  indicators: string;
}

export interface TargetSchedule {
  target: Target;
  challenge?: {
    id: string;
    name: string;
    category?: string;
    serviceType?: string;
  };
  exploits: TargetExploitStatus[];
  summary: TargetScheduleSummary;
}
