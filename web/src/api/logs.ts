import { api, fallback } from "./client";
import type { LogEntry } from "@/types";

const base = "/api/v1/logs";

export async function fetchLogs(params: {
  level?: string;
  source?: string;
  runId?: string;
  flagId?: string;
  limit?: number;
} = {}): Promise<LogEntry[]> {
  return fallback(api.get<{ items: LogEntry[] }>(base, { params }).then((res) => res.data.items), []);
}
