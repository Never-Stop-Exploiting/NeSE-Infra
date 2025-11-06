import { api, fallback } from "./client";
import type { Challenge } from "@/types";

const base = "/api/v1/challenges";

export async function fetchChallenges(): Promise<Challenge[]> {
  return fallback(api.get<{ items: Challenge[] }>(base).then((res) => res.data.items), []);
}
