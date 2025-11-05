import { api, fallback } from "./client";
import type { FlagRecord } from "@/types";

const base = "/api/v1/flags";

export async function fetchFlags(): Promise<FlagRecord[]> {
  return fallback(api.get<{ items: FlagRecord[] }>(base).then((res) => res.data.items), []);
}

export async function retryFlag(flagId: string): Promise<void> {
  await api.post(`${base}/${flagId}/requeue`);
}

export async function fetchFlagLogs(flagId: string): Promise<{ logs: string[] }> {
  return fallback(api.get<{ logs: string[] }>(`${base}/${flagId}/logs`).then((res) => res.data), {
    logs: []
  });
}
