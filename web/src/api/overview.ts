import { api, fallback } from "./client";
import type { OverviewStats, OverviewMatrix } from "@/types";

const base = "/api/v1/overview";

const defaultOverview: OverviewStats = {
  exploitCount: 0,
  targetCount: 0,
  pendingFlags: 0,
  runningRuns: 0,
  flagStatuses: {}
};

const defaultMatrix: OverviewMatrix = {
  start: new Date(0).toISOString(),
  end: new Date(0).toISOString(),
  rangeMinutes: 0,
  challenges: [],
  teams: [],
  cells: []
};

export async function fetchOverviewStats(): Promise<OverviewStats> {
  return fallback(
    api.get<{ overview: OverviewStats }>(base).then((res) => res.data.overview ?? defaultOverview),
    { ...defaultOverview }
  );
}

export async function fetchOverviewMatrix(params?: {
  start?: string;
  end?: string;
  minutes?: number;
}): Promise<OverviewMatrix> {
  const query = new URLSearchParams();
  if (params?.start) {
    query.set("start", params.start);
  }
  if (params?.end) {
    query.set("end", params.end);
  }
  if (params?.minutes) {
    query.set("minutes", params.minutes.toString());
  }
  const url = query.toString() ? `${base}/matrix?${query.toString()}` : `${base}/matrix`;
  return fallback(
    api.get<{ matrix: OverviewMatrix }>(url).then((res) => res.data.matrix ?? defaultMatrix),
    { ...defaultMatrix }
  );
}
