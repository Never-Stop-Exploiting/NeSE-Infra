import { api, fallback } from "./client";
import type { Target } from "@/types";

const base = "/api/v1/targets";

export async function fetchTargets(): Promise<Target[]> {
  return fallback(api.get<{ items: Target[] }>(base).then((res) => res.data.items), []);
}

export async function createTarget(target: Omit<Target, "id">): Promise<Target> {
  const res = await api.post<{ target: Target }>(base, target);
  return res.data.target;
}

export async function importTargets(payload: {
  challengeId: string;
  baseIp: string;
  port: number;
  teamNames?: string[];
  count: number;
}): Promise<{ imported: number }> {
  const res = await api.post<{ imported: number }>(`${base}/import`, payload);
  return res.data;
}
