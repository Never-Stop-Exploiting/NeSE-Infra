import { api, fallback } from "./client";
import type { RunDetail, RunSummary } from "@/types";

const base = "/api/v1/runs";

export async function fetchRuns(): Promise<RunSummary[]> {
  return fallback(api.get<{ items: RunSummary[] }>(base).then((res) => res.data.items), []);
}

export async function fetchRunDetail(runId: string): Promise<RunDetail | null> {
  return fallback(api.get<{ run: RunDetail }>(`${base}/${runId}`).then((res) => res.data.run), null);
}
