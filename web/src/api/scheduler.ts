import { api, fallback } from "./client";
import type { SchedulerSettings, TargetSchedule } from "@/types";

const base = "/api/v1/scheduler";

export async function fetchSchedulerSettings(): Promise<SchedulerSettings> {
  const res = await api.get<{ settings: SchedulerSettings }>(`${base}/settings`);
  return res.data.settings;
}

export async function updateSchedulerSettings(payload: {
  roundIntervalMinutes?: number;
  roundIntervalSeconds?: number;
  maxDispatchBatch?: number;
  enabled?: boolean;
  strategy?: string;
  continuousDelaySeconds?: number;
}): Promise<SchedulerSettings> {
  const res = await api.patch<{ settings: SchedulerSettings }>(`${base}/settings`, payload);
  return res.data.settings;
}

export async function fetchSchedulerTargets(): Promise<TargetSchedule[]> {
  return fallback(
    api.get<{ items: TargetSchedule[] }>(`${base}/targets`).then((res) => res.data.items),
    []
  );
}

export async function updateTargetExploits(
  targetId: string,
  statuses: { exploitId: string; enabled: boolean }[]
): Promise<TargetSchedule> {
  const res = await api.post<{ target: TargetSchedule }>(
    `${base}/targets/${targetId}/exploits`,
    { statuses }
  );
  return res.data.target;
}
