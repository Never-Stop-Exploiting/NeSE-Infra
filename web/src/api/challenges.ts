import { api, fallback } from "./client";
import type { Challenge } from "@/types";

const base = "/api/v1/challenges";

export async function fetchChallenges(): Promise<Challenge[]> {
  return fallback(api.get<{ items: Challenge[] }>(base).then((res) => res.data.items), []);
}

export async function createChallenge(payload: {
  name: string;
  category?: string;
  serviceType?: string;
  description?: string;
  metadata?: Record<string, unknown>;
}): Promise<Challenge> {
  const res = await api.post<{ challenge: Challenge }>(base, payload);
  return res.data.challenge;
}

export async function updateChallenge(
  id: string,
  payload: {
    name?: string;
    category?: string;
    serviceType?: string;
    description?: string;
    metadata?: Record<string, unknown>;
  }
): Promise<Challenge> {
  const res = await api.patch<{ challenge: Challenge }>(`${base}/${id}`, payload);
  return res.data.challenge;
}

export async function deleteChallenge(id: string): Promise<void> {
  await api.delete(`${base}/${id}`);
}
