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
  errorMessage?: string;
}

export interface RunDetail extends RunSummary {
  stdout?: string;
  stderr?: string;
  flagId?: string;
  errorMessage?: string;
  flagValue?: string;
  flagSource?: string;
}

export interface FlagRecord {
  id: string;
  value: string;
  challengeId: string;
  status: "new" | "submitting" | "accepted" | "failed" | "duplicated" | "retry" | "dropped";
  challengeName?: string;
  flagSource?: string;
  targetId: string;
  targetLabel?: string;
  submissionCount: number;
  attempts?: SubmissionAttempt[];
  runId?: string;
  createdAt?: string;
  lastSubmittedAt?: string;
}

export interface OverviewStats {
  exploitCount: number;
  targetCount: number;
  pendingFlags: number;
  runningRuns: number;
  flagStatuses: Record<string, number>;
}

export type OverviewMatrixStatus = "success" | "failed" | "none";

export interface OverviewMatrixChallenge {
  id: string;
  name: string;
}

export interface OverviewMatrixTeam {
  name: string;
}

export interface OverviewMatrixCell {
  challengeId: string;
  teamName: string;
  targetId?: string;
  status: OverviewMatrixStatus;
  successExploits?: string[];
  failedExploits?: string[];
}

export interface OverviewMatrix {
  start: string;
  end: string;
  rangeMinutes: number;
  challenges: OverviewMatrixChallenge[];
  teams: OverviewMatrixTeam[];
  cells: OverviewMatrixCell[];
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
  lastIp?: string;
  tags?: string[];
  maxParallel: number;
  activeJobs: number;
  status: "online" | "offline" | "degraded";
  lastHeartbeat?: string;
  labels?: Record<string, string>;
  config?: Record<string, string>;
}

export interface SubmitterInfo {
  id: string;
  type?: string;
  hostname?: string;
  status: "online" | "offline" | "degraded" | string;
  concurrency: number;
  inFlight: number;
  pending: number;
  recentThroughput: number;
  avgLatencyMs: number;
  pressure: number;
  lastHeartbeat?: string;
  registeredAt?: string;
  labels?: Record<string, string>;
  config?: Record<string, string>;
  windowSeconds?: number;
  latencySumSeconds?: number;
  processedCount?: number;
}

export interface SchedulerSettings {
  roundIntervalSeconds: number;
  roundIntervalMinutes: number;
  maxDispatchBatch: number;
  continuousDelaySeconds: number;
  strategy: "interval" | "continuous" | string;
  enabled: boolean;
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
