import { api, fallback } from "./client";
import type { RunnerInfo } from "@/types";

const base = "/api/v1/runners";

export async function fetchRunners(): Promise<RunnerInfo[]> {
  return fallback(api.get<{ items: RunnerInfo[] }>(base).then((res) => res.data.items), []);
}

export async function deleteRunner(id: string): Promise<void> {
  await api.delete(base, { params: { id } });
}
