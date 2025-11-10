import { api, fallback } from "./client";
import type { RunnerInfo, SubmitterInfo } from "@/types";

const runnerBase = "/api/v1/runners";
const submitterBase = "/api/v1/submitters";

export async function fetchRunners(): Promise<RunnerInfo[]> {
  return fallback(api.get<{ items: RunnerInfo[] }>(runnerBase).then((res) => res.data.items), []);
}

export async function deleteRunner(id: string): Promise<void> {
  await api.delete(runnerBase, { params: { id } });
}

export async function fetchSubmitters(): Promise<SubmitterInfo[]> {
  return fallback(api.get<{ items: SubmitterInfo[] }>(submitterBase).then((res) => res.data.items), []);
}

export async function deleteSubmitter(id: string): Promise<void> {
  await api.delete(`${submitterBase}/${id}`);
}
